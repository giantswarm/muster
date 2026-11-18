package aggregator

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/valkey-io/valkey-go"
)

// bearerExpiryGrace keeps a session's newest bearer exp past that exp, so a
// replica whose timer fires a little late still reads it.
const bearerExpiryGrace = time.Minute

// bearerExpiryStore is the record the replicas share of the latest exp among
// the validated resource bearers each one saw for a session. A replica whose
// requests all carried an older bearer reads it before it ends the session:
// the person's newer bearer may have reached only the other replica.
type bearerExpiryStore interface {
	// extend records exp as the session's latest bearer exp unless a later
	// one is recorded.
	extend(ctx context.Context, sessionID string, exp time.Time) error
	// latest returns the session's latest recorded bearer exp, the zero time
	// when none is recorded.
	latest(ctx context.Context, sessionID string) (time.Time, error)
}

// valkeyBearerExpiryStore is the bearerExpiryStore the replicas share.
//
// Data model:
//
//	{keyPrefix}bearer-exp:{sessionID}  the exp in Unix milliseconds, TTL to that exp plus bearerExpiryGrace
type valkeyBearerExpiryStore struct {
	client    valkey.Client
	keyPrefix string
}

func newValkeyBearerExpiryStore(client valkey.Client, keyPrefix string) *valkeyBearerExpiryStore {
	return &valkeyBearerExpiryStore{client: client, keyPrefix: keyPrefix}
}

func (s *valkeyBearerExpiryStore) key(sessionID string) string {
	return s.keyPrefix + "bearer-exp:" + sessionID
}

// extendScript sets KEYS[1] to ARGV[1] for ARGV[2] ms unless it holds a
// number not below ARGV[1]: the record only moves forward, so an older
// bearer replayed to one replica never shortens it.
var extendScript = valkey.NewLuaScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if tonumber(ARGV[1]) > cur then
  redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
end
return 1`)

func (s *valkeyBearerExpiryStore) extend(ctx context.Context, sessionID string, exp time.Time) error {
	ttl := max(time.Until(exp)+bearerExpiryGrace, time.Second)
	err := extendScript.Exec(ctx, s.client, []string{s.key(sessionID)},
		[]string{strconv.FormatInt(exp.UnixMilli(), 10), strconv.FormatInt(ttl.Milliseconds(), 10)}).Error()
	if err != nil {
		return fmt.Errorf("record bearer exp: %w", err)
	}
	return nil
}

func (s *valkeyBearerExpiryStore) latest(ctx context.Context, sessionID string) (time.Time, error) {
	ms, err := s.client.Do(ctx, s.client.B().Get().Key(s.key(sessionID)).Build()).AsInt64()
	if valkey.IsValkeyNil(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read bearer exp: %w", err)
	}
	return time.UnixMilli(ms), nil
}
