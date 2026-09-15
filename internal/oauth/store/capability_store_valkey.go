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

	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
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
//
// Decoded documents are kept in a bounded process-local cache keyed by
// reference (documentCache). Since a reference names the document's bytes, a
// cached decode is exact for as long as a session's hash points at it: a
// changed capability list is a new reference. The one divergence from reading
// Valkey every time is a document that expired there while a session's hash
// still references it -- the cache keeps serving what the server offered the
// session, where Valkey would report a miss until the session reconnects.
type ValkeyCapabilityStore struct {
	client    valkey.Client
	ttl       time.Duration
	keyPrefix string
	// decoded keeps decoded documents by reference, so a session listing
	// costs one HGETALL and fetches only the documents this process has not
	// decoded yet (giantswarm/muster#1225: a listing read every server's
	// document with its own HGET and GET, ~160 round trips per meta-tool
	// call on an installation with 81 session-authenticated servers).
	decoded *documentCache
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
		decoded:   newDocumentCache(DefaultCapabilityDocumentCacheBytes),
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
	caps, ok := docs[field]
	if !ok {
		return nil, nil
	}
	return caps.DeepCopy(), nil
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
		if isCapabilityRef(field) {
			c, ok := docs[field]
			if !ok {
				logging.Debug("CapabilityStore", "Capability document for %s/%s has expired; treating as a miss",
					logging.TruncateIdentifier(sessionID), serverName)
				continue
			}
			caps[serverName] = c.DeepCopy()
			continue
		}
		c, err := decodeCapabilities([]byte(field))
		if err != nil {
			logging.Warn("CapabilityStore", "Failed to unmarshal capabilities for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverName, err)
			continue
		}
		caps[serverName] = c
	}
	return caps, nil
}

// documents resolves references to their decoded documents: from the cache
// when this process decoded the document before, else with one pipelined GET
// per distinct missing reference in a single round trip (a multi-key command
// would need every key in one slot). A reference whose document is gone from
// Valkey is absent from the result; one that decodes to nothing is reported.
// The returned documents are shared with the cache and must be copied before
// they are modified.
func (s *ValkeyCapabilityStore) documents(ctx context.Context, refs []string) (map[string]*Capabilities, error) {
	docs := make(map[string]*Capabilities, len(refs))
	var missing []string
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		if caps, ok := s.decoded.get(ref); ok {
			docs[ref] = caps
			continue
		}
		missing = append(missing, ref)
	}
	if len(missing) == 0 {
		return docs, nil
	}
	cmds := make(valkey.Commands, 0, len(missing))
	for _, ref := range missing {
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
		caps, err := decodeCapabilities(doc)
		if err != nil {
			return nil, err
		}
		s.decoded.put(missing[i], caps, len(doc))
		docs[missing[i]] = caps
	}
	return docs, nil
}

func (s *ValkeyCapabilityStore) Set(ctx context.Context, sessionID, serverName string, caps *Capabilities) error {
	ref, doc, err := encodeCapabilities(caps)
	if err != nil {
		return err
	}
	if err := s.setReference(ctx, s.key(sessionID), serverName, ref, doc, true); err != nil {
		return err
	}
	// The next listing that meets this reference finds it decoded already.
	s.decoded.put(ref, caps.DeepCopy(), len(doc))
	return nil
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
