package aggregator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSOTracker_FailureTTLExpiry(t *testing.T) {
	tracker := newSSOTracker()

	t.Run("HasSSOFailed returns true immediately after marking failure", func(t *testing.T) {
		tracker.MarkSSOFailed("user1", "serverA")
		assert.True(t, tracker.HasSSOFailed("user1", "serverA"))
	})

	t.Run("HasSSOFailed returns false for unknown user/server pair", func(t *testing.T) {
		assert.False(t, tracker.HasSSOFailed("unknown-user", "serverA"))
		assert.False(t, tracker.HasSSOFailed("user1", "unknown-server"))
	})

	t.Run("HasSSOFailed respects TTL by checking with manipulated entry", func(t *testing.T) {
		tr := newSSOTracker()
		tr.MarkSSOFailed("user2", "serverB")

		// Manipulate the timestamp to simulate TTL expiry (ssoTrackerFailureTTL = 5min)
		tr.mu.Lock()
		tr.failedServers["user2"]["serverB"].failedAt = time.Now().Add(-6 * time.Minute)
		tr.mu.Unlock()

		assert.False(t, tr.HasSSOFailed("user2", "serverB"),
			"entry should be treated as expired after ssoTrackerFailureTTL")
	})

	t.Run("HasSSOFailed returns true just before TTL expiry", func(t *testing.T) {
		tr := newSSOTracker()
		tr.MarkSSOFailed("user3", "serverC")

		tr.mu.Lock()
		tr.failedServers["user3"]["serverC"].failedAt = time.Now().Add(-4 * time.Minute)
		tr.mu.Unlock()

		assert.True(t, tr.HasSSOFailed("user3", "serverC"),
			"entry should still be active before ssoTrackerFailureTTL")
	})
}

func TestSSOTracker_ClearSSOFailed(t *testing.T) {
	t.Run("clears a specific user/server failure", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.MarkSSOFailed("user1", "serverA")
		tracker.MarkSSOFailed("user1", "serverB")

		require.True(t, tracker.HasSSOFailed("user1", "serverA"))
		require.True(t, tracker.HasSSOFailed("user1", "serverB"))

		tracker.ClearSSOFailed("user1", "serverA")

		assert.False(t, tracker.HasSSOFailed("user1", "serverA"),
			"cleared server should no longer be marked as failed")
		assert.True(t, tracker.HasSSOFailed("user1", "serverB"),
			"other servers should not be affected")
	})

	t.Run("clears the user map when last server is removed", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.MarkSSOFailed("user2", "only-server")
		tracker.ClearSSOFailed("user2", "only-server")

		tracker.mu.RLock()
		_, exists := tracker.failedServers["user2"]
		tracker.mu.RUnlock()

		assert.False(t, exists, "user map should be cleaned up when last server is removed")
	})

	t.Run("no-op for unknown user/server pair", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.ClearSSOFailed("nonexistent", "nonexistent")
	})
}

func TestSSOTracker_CleanupExpired(t *testing.T) {
	t.Run("removes entries older than TTL", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.MarkSSOFailed("user1", "expired-server")
		tracker.MarkSSOFailed("user1", "fresh-server")

		// Age the "expired-server" entry past the TTL (ssoTrackerFailureTTL = 5min)
		tracker.mu.Lock()
		tracker.failedServers["user1"]["expired-server"].failedAt = time.Now().Add(-6 * time.Minute)
		tracker.mu.Unlock()

		tracker.CleanupExpired()

		assert.False(t, tracker.HasSSOFailed("user1", "expired-server"),
			"expired entry should be removed by CleanupExpired")
		assert.True(t, tracker.HasSSOFailed("user1", "fresh-server"),
			"fresh entry should not be affected by CleanupExpired")
	})

	t.Run("cleans up empty user maps after removing all expired entries", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.MarkSSOFailed("lonely-user", "only-server")

		tracker.mu.Lock()
		tracker.failedServers["lonely-user"]["only-server"].failedAt = time.Now().Add(-6 * time.Minute)
		tracker.mu.Unlock()

		tracker.CleanupExpired()

		tracker.mu.RLock()
		_, exists := tracker.failedServers["lonely-user"]
		tracker.mu.RUnlock()

		assert.False(t, exists,
			"user map should be removed when all entries for that user are expired")
	})

	t.Run("does nothing when no entries are expired", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.MarkSSOFailed("user1", "serverA")
		tracker.MarkSSOFailed("user2", "serverB")

		tracker.CleanupExpired()

		assert.True(t, tracker.HasSSOFailed("user1", "serverA"))
		assert.True(t, tracker.HasSSOFailed("user2", "serverB"))
	})

	t.Run("handles empty tracker gracefully", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.CleanupExpired()
	})
}

func TestSSOTracker_FailureBlocksRetries(t *testing.T) {
	t.Run("failed server is skipped in initSSO filter logic", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.MarkSSOFailed("user1", "server-sso-1")
		tracker.MarkSSOFailed("user1", "server-sso-2")

		serversToConnect := []string{"server-sso-1", "server-sso-2", "server-sso-3"}
		var pending []string
		for _, name := range serversToConnect {
			if !tracker.HasSSOFailed("user1", name) {
				pending = append(pending, name)
			}
		}

		assert.Equal(t, []string{"server-sso-3"}, pending,
			"only non-failed servers should be in the pending list")
	})

	t.Run("expired failure entries allow retry", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.MarkSSOFailed("user1", "server-sso-1")

		// Simulate TTL expiry (ssoTrackerFailureTTL = 5min)
		tracker.mu.Lock()
		tracker.failedServers["user1"]["server-sso-1"].failedAt = time.Now().Add(-6 * time.Minute)
		tracker.mu.Unlock()

		serversToConnect := []string{"server-sso-1"}
		var pending []string
		for _, name := range serversToConnect {
			if !tracker.HasSSOFailed("user1", name) {
				pending = append(pending, name)
			}
		}

		assert.Equal(t, []string{"server-sso-1"}, pending,
			"expired failure should allow the server to be retried")
	})
}

func TestSSOTracker_ClearAllSSOFailed(t *testing.T) {
	t.Run("clears all failures for a user", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.MarkSSOFailed("user1", "serverA")
		tracker.MarkSSOFailed("user1", "serverB")
		tracker.MarkSSOFailed("user1", "serverC")
		tracker.MarkSSOFailed("user2", "serverA")

		require.True(t, tracker.HasSSOFailed("user1", "serverA"))
		require.True(t, tracker.HasSSOFailed("user1", "serverB"))
		require.True(t, tracker.HasSSOFailed("user1", "serverC"))

		tracker.ClearAllSSOFailed("user1")

		assert.False(t, tracker.HasSSOFailed("user1", "serverA"))
		assert.False(t, tracker.HasSSOFailed("user1", "serverB"))
		assert.False(t, tracker.HasSSOFailed("user1", "serverC"))
		assert.True(t, tracker.HasSSOFailed("user2", "serverA"),
			"other users should not be affected")
	})

	t.Run("cleans up user map entry", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.MarkSSOFailed("user1", "serverA")
		tracker.ClearAllSSOFailed("user1")

		tracker.mu.RLock()
		_, exists := tracker.failedServers["user1"]
		tracker.mu.RUnlock()

		assert.False(t, exists, "user map should be removed after ClearAllSSOFailed")
	})

	t.Run("no-op for unknown user", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.ClearAllSSOFailed("nonexistent")
	})
}

func TestSSOTracker_ReLoginClearsFailure(t *testing.T) {
	tracker := newSSOTracker()
	tracker.MarkSSOFailed("user1", "server-sso-1")
	require.True(t, tracker.HasSSOFailed("user1", "server-sso-1"))

	// ClearSSOFailed is called during auth_logout, enabling fresh SSO on next login
	tracker.ClearSSOFailed("user1", "server-sso-1")
	assert.False(t, tracker.HasSSOFailed("user1", "server-sso-1"),
		"after clearing, SSO should be retryable without waiting for TTL")
}

func TestSSOTracker_PodRestartResetsState(t *testing.T) {
	// The ssoTracker is in-memory only. A pod restart creates a fresh tracker,
	// so all failure records are lost and SSO is retried for all servers.
	tracker := newSSOTracker()

	// No pre-existing failures exist in a fresh tracker
	assert.False(t, tracker.HasSSOFailed("any-user", "any-server"),
		"fresh tracker after pod restart should have no failed entries")

	tracker.mu.RLock()
	assert.Empty(t, tracker.failedServers,
		"fresh tracker should have empty failedServers map")
	assert.Empty(t, tracker.pendingServers,
		"fresh tracker should have empty pendingServers map")
	tracker.mu.RUnlock()
}

func TestSSOBackoffDuration(t *testing.T) {
	t.Run("first failure uses base TTL", func(t *testing.T) {
		assert.Equal(t, ssoTrackerFailureTTL, ssoBackoffDuration(1))
	})

	t.Run("second failure doubles the base TTL", func(t *testing.T) {
		assert.Equal(t, 2*ssoTrackerFailureTTL, ssoBackoffDuration(2))
	})

	t.Run("third failure quadruples the base TTL", func(t *testing.T) {
		assert.Equal(t, 4*ssoTrackerFailureTTL, ssoBackoffDuration(3))
	})

	t.Run("caps at ssoBackoffMaxTTL", func(t *testing.T) {
		assert.Equal(t, ssoBackoffMaxTTL, ssoBackoffDuration(10))
		assert.Equal(t, ssoBackoffMaxTTL, ssoBackoffDuration(100))
	})

	t.Run("zero or negative count uses base TTL", func(t *testing.T) {
		assert.Equal(t, ssoTrackerFailureTTL, ssoBackoffDuration(0))
		assert.Equal(t, ssoTrackerFailureTTL, ssoBackoffDuration(-1))
	})
}

func TestSSOTracker_ExponentialBackoff(t *testing.T) {
	t.Run("consecutive failures increase backoff", func(t *testing.T) {
		tracker := newSSOTracker()

		// First failure: failureCount=1, backoff=5min
		tracker.MarkSSOFailed("user1", "serverA")
		assert.Equal(t, 1, tracker.GetFailureCount("user1", "serverA"))
		assert.True(t, tracker.HasSSOFailed("user1", "serverA"))

		// Second failure (while first is still active): failureCount=2, backoff=10min
		tracker.MarkSSOFailed("user1", "serverA")
		assert.Equal(t, 2, tracker.GetFailureCount("user1", "serverA"))

		// Third failure: failureCount=3, backoff=20min
		tracker.MarkSSOFailed("user1", "serverA")
		assert.Equal(t, 3, tracker.GetFailureCount("user1", "serverA"))
	})

	t.Run("expired failure resets count to 1", func(t *testing.T) {
		tracker := newSSOTracker()

		tracker.MarkSSOFailed("user1", "serverA")
		tracker.MarkSSOFailed("user1", "serverA")
		require.Equal(t, 2, tracker.GetFailureCount("user1", "serverA"))

		// Simulate expiry of the 2nd failure (backoff=10min)
		tracker.mu.Lock()
		tracker.failedServers["user1"]["serverA"].failedAt = time.Now().Add(-11 * time.Minute)
		tracker.mu.Unlock()

		// Next failure should reset to count=1
		tracker.MarkSSOFailed("user1", "serverA")
		assert.Equal(t, 1, tracker.GetFailureCount("user1", "serverA"))
	})

	t.Run("second failure blocked for longer than first", func(t *testing.T) {
		tracker := newSSOTracker()

		// First failure, failureCount=1 (backoff=5min)
		tracker.MarkSSOFailed("user1", "serverA")

		// Simulate 6 minutes passing (past first-failure backoff)
		tracker.mu.Lock()
		tracker.failedServers["user1"]["serverA"].failedAt = time.Now().Add(-6 * time.Minute)
		tracker.mu.Unlock()

		assert.False(t, tracker.HasSSOFailed("user1", "serverA"),
			"first failure should have expired after 6 minutes")

		// Retry triggers second failure (while previous entry is expired, so resets to 1)
		tracker.MarkSSOFailed("user1", "serverA")
		assert.Equal(t, 1, tracker.GetFailureCount("user1", "serverA"),
			"count resets when previous entry has expired")

		// Now simulate consecutive failures within TTL
		tracker.MarkSSOFailed("user1", "serverA")
		assert.Equal(t, 2, tracker.GetFailureCount("user1", "serverA"))

		// With failureCount=2, backoff is 10min; 6min elapsed should still be active
		tracker.mu.Lock()
		tracker.failedServers["user1"]["serverA"].failedAt = time.Now().Add(-6 * time.Minute)
		tracker.mu.Unlock()

		assert.True(t, tracker.HasSSOFailed("user1", "serverA"),
			"second failure should still be active after 6 minutes (backoff=10min)")

		// But after 11 minutes it should expire
		tracker.mu.Lock()
		tracker.failedServers["user1"]["serverA"].failedAt = time.Now().Add(-11 * time.Minute)
		tracker.mu.Unlock()

		assert.False(t, tracker.HasSSOFailed("user1", "serverA"),
			"second failure should expire after 11 minutes (backoff=10min)")
	})

	t.Run("CleanupExpired respects backoff duration", func(t *testing.T) {
		tracker := newSSOTracker()

		// Create entry with failureCount=2 (backoff=10min)
		tracker.MarkSSOFailed("user1", "serverA")
		tracker.MarkSSOFailed("user1", "serverA")
		require.Equal(t, 2, tracker.GetFailureCount("user1", "serverA"))

		// 6 minutes passed — should NOT be cleaned up (backoff=10min)
		tracker.mu.Lock()
		tracker.failedServers["user1"]["serverA"].failedAt = time.Now().Add(-6 * time.Minute)
		tracker.mu.Unlock()

		tracker.CleanupExpired()
		assert.Equal(t, 2, tracker.GetFailureCount("user1", "serverA"),
			"entry with 10min backoff should survive cleanup at 6 minutes")

		// 11 minutes passed — should be cleaned up
		tracker.mu.Lock()
		tracker.failedServers["user1"]["serverA"].failedAt = time.Now().Add(-11 * time.Minute)
		tracker.mu.Unlock()

		tracker.CleanupExpired()
		assert.Equal(t, 0, tracker.GetFailureCount("user1", "serverA"),
			"entry should be cleaned up after backoff duration elapses")
	})
}

func TestSSOTracker_GetFailureCount(t *testing.T) {
	t.Run("returns 0 for unknown entries", func(t *testing.T) {
		tracker := newSSOTracker()
		assert.Equal(t, 0, tracker.GetFailureCount("unknown", "unknown"))
	})

	t.Run("returns correct count after multiple failures", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.MarkSSOFailed("user1", "serverA")
		assert.Equal(t, 1, tracker.GetFailureCount("user1", "serverA"))
		tracker.MarkSSOFailed("user1", "serverA")
		assert.Equal(t, 2, tracker.GetFailureCount("user1", "serverA"))
	})

	t.Run("independent per server", func(t *testing.T) {
		tracker := newSSOTracker()
		tracker.MarkSSOFailed("user1", "serverA")
		tracker.MarkSSOFailed("user1", "serverA")
		tracker.MarkSSOFailed("user1", "serverB")

		assert.Equal(t, 2, tracker.GetFailureCount("user1", "serverA"))
		assert.Equal(t, 1, tracker.GetFailureCount("user1", "serverB"))
	})
}
