package credprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/valkey-io/valkey-go"
)

// The agent index finds the bindings of an agent's running Sessions, so a
// dropped grant reaches them (PATCH /session-bindings/grants). It is a sorted
// set scored by each binding's expiry: Put and Touch, which restart a
// binding's TTL, rescore its actor and drop the members already expired, and
// the set expires with its latest member, so an agent's index never outlives
// its bindings and never grows past its live Sessions (M2-S3-C-03).

func (s *ValkeyStore) agentKey(agent string) string {
	sum := sha256.Sum256([]byte(agent))
	return s.keyPrefix + "credprovider:binding-agent:" + hex.EncodeToString(sum[:])
}

// indexScript scores ARGV[1] with its expiry ARGV[2], removes the members
// that expired before ARGV[3] and lets the set live at least ARGV[4] ms.
var indexScript = valkey.NewLuaScript(`
redis.call('ZADD', KEYS[1], ARGV[2], ARGV[1])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', '(' .. ARGV[3])
if redis.call('PTTL', KEYS[1]) < tonumber(ARGV[4]) then
  redis.call('PEXPIRE', KEYS[1], ARGV[4])
end
return 1`)

// index records the actor's binding of the agent until now+ttl.
func (s *ValkeyStore) index(ctx context.Context, agent, actor string, ttl time.Duration) error {
	now := time.Now().UnixMilli()
	err := indexScript.Exec(ctx, s.client, []string{s.agentKey(agent)}, []string{
		actor, strconv.FormatInt(now+ttl.Milliseconds(), 10), strconv.FormatInt(now, 10), strconv.FormatInt(ttl.Milliseconds(), 10),
	}).Error()
	if err != nil {
		return fmt.Errorf("index binding by agent: %w", err)
	}
	return nil
}

// ActorsOfAgent implements Store: the members not yet expired, read in one
// pipeline; a member whose binding is gone or names another agent leaves the
// index.
func (s *ValkeyStore) ActorsOfAgent(ctx context.Context, agent string) ([]string, error) {
	if agent == "" {
		return nil, nil
	}
	key := s.agentKey(agent)
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	members, err := s.client.Do(ctx, s.client.B().Zrange().Key(key).Min(now).Max("+inf").Byscore().Build()).AsStrSlice()
	if err != nil {
		return nil, fmt.Errorf("read the agent's bindings: %w", err)
	}
	if len(members) == 0 {
		return nil, nil
	}
	// One pipelined GET per member rather than MGET: the bindings' keys
	// sit in different hash slots on a Valkey cluster.
	gets := make(valkey.Commands, len(members))
	for i, actor := range members {
		gets[i] = s.client.B().Get().Key(s.key(actor)).Build()
	}
	var actors, stale []string
	for i, result := range s.client.DoMulti(ctx, gets...) {
		stored, err := result.ToString()
		if valkey.IsValkeyNil(err) || stored == "" {
			stale = append(stale, members[i])
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read binding: %w", err)
		}
		b, err := s.open(stored)
		if err != nil {
			return nil, err
		}
		if b.Agent != agent {
			stale = append(stale, members[i])
			continue
		}
		actors = append(actors, members[i])
	}
	if len(stale) > 0 {
		_ = s.client.Do(ctx, s.client.B().Zrem().Key(key).Member(stale...).Build()).Error()
	}
	return actors, nil
}
