package aggregator

import (
	"context"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/pkg/logging"
)

// ValkeySessionAuthStore stores per-session authentication state in Valkey hashes.
//
// Data model:
//
//	Key:    {keyPrefix}auth:{sessionID}
//	Fields: {serverName} -> "1"
//	TTL:    session-level, reset on every MarkAuthenticated via EXPIRE
type ValkeySessionAuthStore struct {
	client    valkey.Client
	ttl       time.Duration
	keyPrefix string
}

// NewValkeySessionAuthStore creates a Valkey-backed session auth store.
// keyPrefix is prepended to all Valkey keys (default "muster:" if empty).
func NewValkeySessionAuthStore(client valkey.Client, ttl time.Duration, keyPrefix string) *ValkeySessionAuthStore {
	if keyPrefix == "" {
		keyPrefix = "muster:" //nolint:goconst
	}
	return &ValkeySessionAuthStore{
		client:    client,
		ttl:       ttl,
		keyPrefix: keyPrefix,
	}
}

const valkeyAuthFieldValue = "1"

func (s *ValkeySessionAuthStore) key(sessionID string) string {
	return s.keyPrefix + "auth:" + sessionID
}

func (s *ValkeySessionAuthStore) IsAuthenticated(ctx context.Context, sessionID, serverName string) (bool, error) {
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

func (s *ValkeySessionAuthStore) MarkAuthenticated(ctx context.Context, sessionID, serverName string) error {
	key := s.key(sessionID)

	cmds := make(valkey.Commands, 0, 2)
	cmds = append(cmds, s.client.B().Hset().Key(key).FieldValue().FieldValue(serverName, valkeyAuthFieldValue).Build())
	cmds = append(cmds, s.client.B().Expire().Key(key).Seconds(int64(s.ttl.Seconds())).Build())

	for _, resp := range s.client.DoMulti(ctx, cmds...) {
		if err := resp.Error(); err != nil {
			return fmt.Errorf("valkey HSET/EXPIRE: %w", err)
		}
	}
	return nil
}

func (s *ValkeySessionAuthStore) Revoke(ctx context.Context, sessionID, serverName string) error {
	cmd := s.client.B().Hdel().Key(s.key(sessionID)).Field(serverName).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("valkey HDEL: %w", err)
	}
	return nil
}

func (s *ValkeySessionAuthStore) RevokeSession(ctx context.Context, sessionID string) error {
	cmd := s.client.B().Del().Key(s.key(sessionID)).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("valkey DEL: %w", err)
	}
	return nil
}

func (s *ValkeySessionAuthStore) RevokeServer(ctx context.Context, serverName string) error {
	scanPrefix := s.keyPrefix + "auth:*"
	var cursor uint64
	for {
		cmd := s.client.B().Scan().Cursor(cursor).Match(scanPrefix).Count(100).Build()
		result := s.client.Do(ctx, cmd)
		if err := result.Error(); err != nil {
			return fmt.Errorf("valkey SCAN: %w", err)
		}

		entry, err := result.AsScanEntry()
		if err != nil {
			return fmt.Errorf("valkey SCAN decode: %w", err)
		}

		if len(entry.Elements) > 0 {
			cmds := make(valkey.Commands, 0, len(entry.Elements))
			for _, key := range entry.Elements {
				cmds = append(cmds, s.client.B().Hdel().Key(key).Field(serverName).Build())
			}
			for i, resp := range s.client.DoMulti(ctx, cmds...) {
				if err := resp.Error(); err != nil {
					logging.Warn("AuthStore", "Failed to HDEL %s from %s: %v",
						serverName, entry.Elements[i], err)
				}
			}
		}

		cursor = entry.Cursor
		if cursor == 0 {
			break
		}
	}
	return nil
}

func (s *ValkeySessionAuthStore) Touch(ctx context.Context, sessionID string) (bool, error) {
	cmd := s.client.B().Expire().Key(s.key(sessionID)).Seconds(int64(s.ttl.Seconds())).Build()
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
