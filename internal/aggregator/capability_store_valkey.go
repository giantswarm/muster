package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/pkg/logging"
)

// valkeyKeyPrefix is the key prefix for capability store hashes.
const valkeyKeyPrefix = "muster:cap:"

// ValkeyCapabilityStore stores per-session capabilities in Valkey hashes.
//
// Data model:
//
//	Key:    muster:cap:{sessionID}
//	Fields: {serverName} -> JSON{tools, resources, prompts}
//	TTL:    session-level, reset on every Set via EXPIRE
type ValkeyCapabilityStore struct {
	client valkey.Client
	ttl    time.Duration
}

// NewValkeyCapabilityStore creates a Valkey-backed capability store.
func NewValkeyCapabilityStore(client valkey.Client, ttl time.Duration) *ValkeyCapabilityStore {
	return &ValkeyCapabilityStore{
		client: client,
		ttl:    ttl,
	}
}

func (s *ValkeyCapabilityStore) key(sessionID string) string {
	return valkeyKeyPrefix + sessionID
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

	data, err := result.AsBytes()
	if err != nil {
		return nil, fmt.Errorf("valkey HGET decode: %w", err)
	}

	var caps Capabilities
	if err := json.Unmarshal(data, &caps); err != nil {
		return nil, fmt.Errorf("unmarshal capabilities: %w", err)
	}
	return &caps, nil
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

	m, err := result.AsStrMap()
	if err != nil {
		return nil, fmt.Errorf("valkey HGETALL decode: %w", err)
	}
	if len(m) == 0 {
		return nil, nil
	}

	caps := make(map[string]*Capabilities, len(m))
	for serverName, data := range m {
		var c Capabilities
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			logging.Warn("CapabilityStore", "Failed to unmarshal capabilities for %s/%s: %v",
				sessionID, serverName, err)
			continue
		}
		caps[serverName] = &c
	}
	return caps, nil
}

func (s *ValkeyCapabilityStore) Set(ctx context.Context, sessionID, serverName string, caps *Capabilities) error {
	data, err := json.Marshal(caps)
	if err != nil {
		return fmt.Errorf("marshal capabilities: %w", err)
	}

	key := s.key(sessionID)

	cmds := make(valkey.Commands, 0, 2)
	cmds = append(cmds, s.client.B().Hset().Key(key).FieldValue().FieldValue(serverName, string(data)).Build())
	cmds = append(cmds, s.client.B().Expire().Key(key).Seconds(int64(s.ttl.Seconds())).Build())

	for _, resp := range s.client.DoMulti(ctx, cmds...) {
		if err := resp.Error(); err != nil {
			return fmt.Errorf("valkey HSET/EXPIRE: %w", err)
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
	var cursor uint64
	for {
		cmd := s.client.B().Scan().Cursor(cursor).Match(valkeyKeyPrefix + "*").Count(100).Build()
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
					logging.Warn("CapabilityStore", "Failed to HDEL %s from %s: %v",
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

func (s *ValkeyCapabilityStore) Touch(ctx context.Context, sessionID string) (bool, error) {
	cmd := s.client.B().Expire().Key(s.key(sessionID)).Seconds(int64(s.ttl.Seconds())).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		return false, fmt.Errorf("valkey EXPIRE: %w", err)
	}
	b, err := result.AsBool()
	if err != nil {
		return false, nil
	}
	return b, nil
}

func (s *ValkeyCapabilityStore) Exists(ctx context.Context, sessionID, serverName string) (bool, error) {
	cmd := s.client.B().Hexists().Key(s.key(sessionID)).Field(serverName).Build()
	result := s.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		return false, fmt.Errorf("valkey HEXISTS: %w", err)
	}
	b, err := result.AsBool()
	if err != nil {
		return false, nil
	}
	return b, nil
}
