package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"
)

const (
	// grantRefreshLockTTL bounds how long one replica holds the refresh lock
	// of a grant: the refresh round trip (subjectGrantRefreshTimeout) plus
	// the store writes after it. A replica that dies mid-refresh releases
	// the lock by expiry.
	grantRefreshLockTTL = subjectGrantRefreshTimeout + 15*time.Second

	// grantRefreshLockRetryMin and grantRefreshLockRetryMax bound the pause
	// between two attempts to take a lock another replica holds.
	grantRefreshLockRetryMin = 25 * time.Millisecond
	grantRefreshLockRetryMax = 500 * time.Millisecond
)

// grantRefreshLocker serializes the refresh of one person's grant across
// muster replicas that share a token store. A token store that is shared
// between processes implements it; the in-memory store does not need to,
// since the in-process single-flight already covers its only process.
type grantRefreshLocker interface {
	// LockGrantRefresh blocks until the caller holds the refresh lock for
	// the person and issuer, or ctx ends. The returned function releases
	// the lock; it is a no-op once the lock has expired and another holder
	// took it.
	LockGrantRefresh(ctx context.Context, userID, issuer string) (unlock func(), err error)
}

// releaseLockScript deletes the lock only while it still holds the
// caller's owner token (ARGV[1]), so a holder whose lock expired never
// releases the next holder's.
var releaseLockScript = valkey.NewLuaScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`)

func (s *ValkeyTokenStore) grantRefreshLockKey(userID, issuer string) string {
	return s.keyPrefix + "oauth:refresh-lock:" + userID + valkeyTokenFieldSep + issuer
}

// LockGrantRefresh implements grantRefreshLocker with SET NX PX on a key per
// person and issuer, retried with a growing pause while another replica
// holds it.
func (s *ValkeyTokenStore) LockGrantRefresh(ctx context.Context, userID, issuer string) (func(), error) {
	owner, err := newLockOwner()
	if err != nil {
		return nil, err
	}
	key := s.grantRefreshLockKey(userID, issuer)

	pause := grantRefreshLockRetryMin
	for {
		// Built per attempt: the client recycles a command once done.
		set := s.client.B().Set().Key(key).Value(owner).Nx().PxMilliseconds(grantRefreshLockTTL.Milliseconds()).Build()
		err := s.client.Do(ctx, set).Error()
		if err == nil {
			return func() {
				// Detached: the lock must be released even when the
				// refresh ran out its context.
				_ = releaseLockScript.Exec(context.Background(), s.client, []string{key}, []string{owner}).Error()
			}, nil
		}
		if !valkey.IsValkeyNil(err) {
			return nil, fmt.Errorf("take grant refresh lock: %w", err)
		}
		// NX refused: another replica refreshes this grant right now.
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait for grant refresh lock: %w", ctx.Err())
		case <-timer.C:
		}
		pause = min(pause*2, grantRefreshLockRetryMax)
	}
}

// newLockOwner returns a random token that identifies one lock holder.
func newLockOwner() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate lock owner: %w", err)
	}
	return hex.EncodeToString(b), nil
}
