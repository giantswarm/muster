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
