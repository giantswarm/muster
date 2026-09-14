package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
)

// capabilityRefPrefix marks a hash field that points at a shared document
// instead of carrying the document inline.
const capabilityRefPrefix = "sha256:"

// ValkeyCapabilityStore stores per-session capabilities in Valkey.
//
// Data model:
//
//	Key:    {keyPrefix}cap:{sessionID}
//	Fields: {serverName} -> "sha256:{hex}" — a reference to the document below
//	TTL:    session-level, reset on every Set via EXPIRE
//
//	Key:    {keyPrefix}capblob:{hex}
//	Value:  JSON{tools, resources, prompts}, content-addressed by the SHA-256
//	        of its bytes
//	TTL:    the store TTL, refreshed on every Set that references the document
//
// A capability document (an upstream server's full tool, resource and prompt
// list, with every input schema) is the same for every session that connects
// the same server the same way, while a session comes and goes with every
// forwarded bearer. Storing the document once and a 71-byte reference per
// session keeps the store proportional to the number of distinct documents,
// not to sessions × servers (giantswarm/muster#1217: 448 sessions × ~1 MB
// filled the store on one installation).
//
// A reference whose document has expired reads as a cache miss (nil), which
// every caller treats as "list the server's capabilities again on the next
// connect" — the same as a session whose hash expired. A field that still
// carries a document inline (written before this layout) reads as before;
// MigrateInlineEntries rewrites such fields to references.
type ValkeyCapabilityStore struct {
	client    valkey.Client
	ttl       time.Duration
	keyPrefix string
}

// NewValkeyCapabilityStore creates a Valkey-backed capability store.
// keyPrefix is prepended to all Valkey keys (default "muster:" if empty).
func NewValkeyCapabilityStore(client valkey.Client, ttl time.Duration, keyPrefix string) *ValkeyCapabilityStore {
	if keyPrefix == "" {
		keyPrefix = config.DefaultValkeyKeyPrefix
	}
	return &ValkeyCapabilityStore{
		client:    client,
		ttl:       ttl,
		keyPrefix: keyPrefix,
	}
}

func (s *ValkeyCapabilityStore) key(sessionID string) string {
	return s.keyPrefix + "cap:" + sessionID
}

func (s *ValkeyCapabilityStore) blobKey(digest string) string {
	return s.keyPrefix + "capblob:" + digest
}

func (s *ValkeyCapabilityStore) ttlSeconds() int64 {
	return int64(s.ttl.Seconds())
}

// encodeCapabilities marshals caps and returns the document bytes with the
// reference that names them.
func encodeCapabilities(caps *Capabilities) (ref string, doc []byte, err error) {
	doc, err = json.Marshal(caps)
	if err != nil {
		return "", nil, fmt.Errorf("marshal capabilities: %w", err)
	}
	sum := sha256.Sum256(doc)
	return capabilityRefPrefix + hex.EncodeToString(sum[:]), doc, nil
}

// isCapabilityRef reports whether a hash field value is a reference to a
// document rather than the document itself.
func isCapabilityRef(field string) bool {
	return strings.HasPrefix(field, capabilityRefPrefix)
}

func decodeCapabilities(doc []byte) (*Capabilities, error) {
	var caps Capabilities
	if err := json.Unmarshal(doc, &caps); err != nil {
		return nil, fmt.Errorf("unmarshal capabilities: %w", err)
	}
	return &caps, nil
}

func (s *ValkeyCapabilityStore) Get(ctx context.Context, sessionID, serverName string) (*Capabilities, error) {
	cmd := s.client.B().Hget().Key(s.key(sessionID)).Field(serverName).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("valkey HGET: %w", err)
	}

	field, err := result.ToString()
	if err != nil {
		return nil, fmt.Errorf("valkey HGET decode: %w", err)
	}
	if !isCapabilityRef(field) {
		return decodeCapabilities([]byte(field))
	}

	docs, err := s.documents(ctx, []string{field})
	if err != nil {
		return nil, err
	}
	doc, ok := docs[field]
	if !ok {
		return nil, nil
	}
	return decodeCapabilities(doc)
}

func (s *ValkeyCapabilityStore) GetAll(ctx context.Context, sessionID string) (map[string]*Capabilities, error) {
	cmd := s.client.B().Hgetall().Key(s.key(sessionID)).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("valkey HGETALL: %w", err)
	}

	fields, err := result.AsStrMap()
	if err != nil {
		return nil, fmt.Errorf("valkey HGETALL decode: %w", err)
	}
	if len(fields) == 0 {
		return nil, nil
	}

	refs := make([]string, 0, len(fields))
	for _, field := range fields {
		if isCapabilityRef(field) {
			refs = append(refs, field)
		}
	}
	docs, err := s.documents(ctx, refs)
	if err != nil {
		return nil, err
	}

	caps := make(map[string]*Capabilities, len(fields))
	for serverName, field := range fields {
		doc := []byte(field)
		if isCapabilityRef(field) {
			var ok bool
			if doc, ok = docs[field]; !ok {
				logging.Debug("CapabilityStore", "Capability document for %s/%s has expired; treating as a miss",
					logging.TruncateIdentifier(sessionID), serverName)
				continue
			}
		}
		c, err := decodeCapabilities(doc)
		if err != nil {
			logging.Warn("CapabilityStore", "Failed to unmarshal capabilities for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverName, err)
			continue
		}
		caps[serverName] = c
	}
	return caps, nil
}

// documents resolves references to their documents in one round trip (one
// pipelined GET per distinct reference — a multi-key command would need every
// key in one slot). A reference whose document is gone is absent from the
// result.
func (s *ValkeyCapabilityStore) documents(ctx context.Context, refs []string) (map[string][]byte, error) {
	docs := make(map[string][]byte, len(refs))
	if len(refs) == 0 {
		return docs, nil
	}
	// De-duplicate: several servers of one session may share a document.
	unique := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		unique = append(unique, ref)
	}
	cmds := make(valkey.Commands, 0, len(unique))
	for _, ref := range unique {
		cmds = append(cmds, s.client.B().Get().Key(s.blobKey(strings.TrimPrefix(ref, capabilityRefPrefix))).Build())
	}
	for i, resp := range s.client.DoMulti(ctx, cmds...) {
		if err := resp.Error(); err != nil {
			if valkey.IsValkeyNil(err) {
				continue
			}
			return nil, fmt.Errorf("valkey GET capability document: %w", err)
		}
		doc, err := resp.AsBytes()
		if err != nil {
			return nil, fmt.Errorf("valkey GET capability document decode: %w", err)
		}
		docs[unique[i]] = doc
	}
	return docs, nil
}

func (s *ValkeyCapabilityStore) Set(ctx context.Context, sessionID, serverName string, caps *Capabilities) error {
	ref, doc, err := encodeCapabilities(caps)
	if err != nil {
		return err
	}
	return s.setReference(ctx, s.key(sessionID), serverName, ref, doc, true)
}

// setReference writes the document under its content address (refreshing its
// TTL), points the session's field at it and, when refreshSession is set,
// resets the session TTL. The three commands travel in one pipeline.
func (s *ValkeyCapabilityStore) setReference(ctx context.Context, key, serverName, ref string, doc []byte, refreshSession bool) error {
	cmds := make(valkey.Commands, 0, 3)
	cmds = append(cmds,
		s.client.B().Set().Key(s.blobKey(strings.TrimPrefix(ref, capabilityRefPrefix))).Value(valkey.BinaryString(doc)).ExSeconds(s.ttlSeconds()).Build(),
		s.client.B().Hset().Key(key).FieldValue().FieldValue(serverName, ref).Build(),
	)
	if refreshSession {
		cmds = append(cmds, s.client.B().Expire().Key(key).Seconds(s.ttlSeconds()).Build())
	}
	for _, resp := range s.client.DoMulti(ctx, cmds...) {
		if err := resp.Error(); err != nil {
			return fmt.Errorf("valkey SET/HSET/EXPIRE: %w", err)
		}
	}
	return nil
}

func (s *ValkeyCapabilityStore) Delete(ctx context.Context, sessionID string) error {
	cmd := s.client.B().Del().Key(s.key(sessionID)).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("valkey DEL: %w", err)
	}
	return nil
}

func (s *ValkeyCapabilityStore) DeleteEntry(ctx context.Context, sessionID, serverName string) error {
	cmd := s.client.B().Hdel().Key(s.key(sessionID)).Field(serverName).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("valkey HDEL: %w", err)
	}
	return nil
}

func (s *ValkeyCapabilityStore) DeleteServer(ctx context.Context, serverName string) error {
	return s.scanSessions(ctx, 100, func(keys []string) error {
		cmds := make(valkey.Commands, 0, len(keys))
		for _, key := range keys {
			cmds = append(cmds, s.client.B().Hdel().Key(key).Field(serverName).Build())
		}
		for i, resp := range s.client.DoMulti(ctx, cmds...) {
			if err := resp.Error(); err != nil {
				logging.Warn("CapabilityStore", "Failed to HDEL %s from %s: %v",
					serverName, keys[i], err)
			}
		}
		return nil
	})
}

func (s *ValkeyCapabilityStore) Touch(ctx context.Context, sessionID string) (bool, error) {
	cmd := s.client.B().Expire().Key(s.key(sessionID)).Seconds(s.ttlSeconds()).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		return false, fmt.Errorf("valkey EXPIRE: %w", err)
	}
	b, err := result.AsBool()
	if err != nil {
		return false, fmt.Errorf("valkey EXPIRE decode: %w", err)
	}
	return b, nil
}

// ListSessions returns every sessionID with a capability entry.
func (s *ValkeyCapabilityStore) ListSessions(ctx context.Context) ([]string, error) {
	prefix := s.keyPrefix + "cap:"
	var out []string
	err := s.scanSessions(ctx, 500, func(keys []string) error {
		for _, k := range keys {
			out = append(out, k[len(prefix):])
		}
		return nil
	})
	return out, err
}

// scanSessions walks every session hash in batches and hands each batch of
// keys to visit.
func (s *ValkeyCapabilityStore) scanSessions(ctx context.Context, count int64, visit func(keys []string) error) error {
	match := s.keyPrefix + "cap:*"
	var cursor uint64
	for {
		cmd := s.client.B().Scan().Cursor(cursor).Match(match).Count(count).Build()
		result := s.client.Do(ctx, cmd)
		if err := result.Error(); err != nil {
			return fmt.Errorf("valkey SCAN: %w", err)
		}
		entry, err := result.AsScanEntry()
		if err != nil {
			return fmt.Errorf("valkey SCAN decode: %w", err)
		}
		if len(entry.Elements) > 0 {
			if err := visit(entry.Elements); err != nil {
				return err
			}
		}
		cursor = entry.Cursor
		if cursor == 0 {
			return nil
		}
	}
}

func (s *ValkeyCapabilityStore) Exists(ctx context.Context, sessionID, serverName string) (bool, error) {
	cmd := s.client.B().Hexists().Key(s.key(sessionID)).Field(serverName).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		return false, fmt.Errorf("valkey HEXISTS: %w", err)
	}
	b, err := result.AsBool()
	if err != nil {
		return false, fmt.Errorf("valkey HEXISTS decode: %w", err)
	}
	return b, nil
}

// MigrationReport counts what MigrateInlineEntries found and changed.
type MigrationReport struct {
	// Sessions is the number of session hashes visited.
	Sessions int
	// Migrated is the number of fields rewritten from an inline document to a
	// reference.
	Migrated int
	// Documents is the number of distinct documents the migrated fields now
	// share.
	Documents int
	// InlineBytes is the size of the inline documents that were replaced.
	InlineBytes int
}

// MigrateInlineEntries rewrites every session field that still carries its
// capability document inline (the layout before content addressing) to a
// reference, writing each distinct document once. Session TTLs are left as
// they are; the documents take the store TTL. Idempotent: a field that is
// already a reference is left alone, so a store that has been migrated costs
// one SCAN.
func (s *ValkeyCapabilityStore) MigrateInlineEntries(ctx context.Context) (MigrationReport, error) {
	var report MigrationReport
	documents := make(map[string]struct{})
	err := s.scanSessions(ctx, 100, func(keys []string) error {
		for _, key := range keys {
			report.Sessions++
			result := s.client.Do(ctx, s.client.B().Hgetall().Key(key).Build())
			if err := result.Error(); err != nil {
				if valkey.IsValkeyNil(err) {
					continue
				}
				return fmt.Errorf("valkey HGETALL %s: %w", key, err)
			}
			fields, err := result.AsStrMap()
			if err != nil {
				return fmt.Errorf("valkey HGETALL %s decode: %w", key, err)
			}
			for serverName, field := range fields {
				if isCapabilityRef(field) {
					continue
				}
				caps, err := decodeCapabilities([]byte(field))
				if err != nil {
					logging.Warn("CapabilityStore", "Migration: field %s of %s is neither a reference nor a capability document, left as is: %v",
						serverName, key, err)
					continue
				}
				ref, doc, err := encodeCapabilities(caps)
				if err != nil {
					return err
				}
				if err := s.setReference(ctx, key, serverName, ref, doc, false); err != nil {
					return fmt.Errorf("migrate %s/%s: %w", key, serverName, err)
				}
				documents[ref] = struct{}{}
				report.Migrated++
				report.InlineBytes += len(field)
			}
		}
		return nil
	})
	report.Documents = len(documents)
	return report, err
}
