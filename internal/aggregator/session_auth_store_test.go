package aggregator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemorySessionAuthStore_MarkAndCheck(t *testing.T) {
	store := NewInMemorySessionAuthStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	authed, err := store.IsAuthenticated(ctx, "session1", "server1")
	require.NoError(t, err)
	assert.False(t, authed, "should not be authenticated before MarkAuthenticated")

	err = store.MarkAuthenticated(ctx, "session1", "server1")
	require.NoError(t, err)

	authed, err = store.IsAuthenticated(ctx, "session1", "server1")
	require.NoError(t, err)
	assert.True(t, authed, "should be authenticated after MarkAuthenticated")
}

func TestInMemorySessionAuthStore_IsAuthenticatedNonexistent(t *testing.T) {
	store := NewInMemorySessionAuthStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	authed, err := store.IsAuthenticated(ctx, "nouser", "noserver")
	assert.NoError(t, err)
	assert.False(t, authed)
}

func TestInMemorySessionAuthStore_MultipleServers(t *testing.T) {
	store := NewInMemorySessionAuthStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "session1", "server1")
	_ = store.MarkAuthenticated(ctx, "session1", "server2")

	authed, _ := store.IsAuthenticated(ctx, "session1", "server1")
	assert.True(t, authed)

	authed, _ = store.IsAuthenticated(ctx, "session1", "server2")
	assert.True(t, authed)

	authed, _ = store.IsAuthenticated(ctx, "session1", "server3")
	assert.False(t, authed, "server3 was never authenticated")
}

func TestInMemorySessionAuthStore_TTLExpiry(t *testing.T) {
	ttl := 50 * time.Millisecond
	store := NewInMemorySessionAuthStore(ttl)
	defer store.Stop()
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "session1", "server1")

	authed, err := store.IsAuthenticated(ctx, "session1", "server1")
	require.NoError(t, err)
	assert.True(t, authed)

	require.Eventually(t, func() bool {
		authed, _ := store.IsAuthenticated(ctx, "session1", "server1")
		return !authed
	}, 5*time.Second, 5*time.Millisecond, "auth should expire after TTL")
}

func TestInMemorySessionAuthStore_TTLResetOnMark(t *testing.T) {
	ttl := 100 * time.Millisecond
	store := NewInMemorySessionAuthStore(ttl)
	defer store.Stop()
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "session1", "server1")

	time.Sleep(70 * time.Millisecond)
	_ = store.MarkAuthenticated(ctx, "session1", "server2")

	time.Sleep(70 * time.Millisecond)
	authed, err := store.IsAuthenticated(ctx, "session1", "server1")
	require.NoError(t, err)
	assert.True(t, authed, "server1 should still be authenticated after TTL reset by server2 MarkAuthenticated")
}

func TestInMemorySessionAuthStore_Revoke(t *testing.T) {
	store := NewInMemorySessionAuthStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "session-A", "server1")
	_ = store.MarkAuthenticated(ctx, "session-A", "server2")

	err := store.Revoke(ctx, "session-A", "server1")
	require.NoError(t, err)

	authed, _ := store.IsAuthenticated(ctx, "session-A", "server1")
	assert.False(t, authed, "server1 should be revoked")

	authed, _ = store.IsAuthenticated(ctx, "session-A", "server2")
	assert.True(t, authed, "server2 should not be affected")
}

func TestInMemorySessionAuthStore_RevokeSession(t *testing.T) {
	store := NewInMemorySessionAuthStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "session-A", "server1")
	_ = store.MarkAuthenticated(ctx, "session-A", "server2")
	_ = store.MarkAuthenticated(ctx, "session-B", "server1")

	err := store.RevokeSession(ctx, "session-A")
	require.NoError(t, err)

	authed, _ := store.IsAuthenticated(ctx, "session-A", "server1")
	assert.False(t, authed, "session-A/server1 should be revoked")

	authed, _ = store.IsAuthenticated(ctx, "session-A", "server2")
	assert.False(t, authed, "session-A/server2 should be revoked")

	authed, _ = store.IsAuthenticated(ctx, "session-B", "server1")
	assert.True(t, authed, "session-B should not be affected")
}

func TestInMemorySessionAuthStore_RevokeServer(t *testing.T) {
	store := NewInMemorySessionAuthStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "session-A", "server1")
	_ = store.MarkAuthenticated(ctx, "session-B", "server1")
	_ = store.MarkAuthenticated(ctx, "session-A", "server2")

	err := store.RevokeServer(ctx, "server1")
	require.NoError(t, err)

	authed, _ := store.IsAuthenticated(ctx, "session-A", "server1")
	assert.False(t, authed, "session-A/server1 should be revoked")

	authed, _ = store.IsAuthenticated(ctx, "session-B", "server1")
	assert.False(t, authed, "session-B/server1 should be revoked")

	authed, _ = store.IsAuthenticated(ctx, "session-A", "server2")
	assert.True(t, authed, "server2 should not be affected")
}

func TestInMemorySessionAuthStore_TouchExtendsTTL(t *testing.T) {
	ttl := 100 * time.Millisecond
	store := NewInMemorySessionAuthStore(ttl)
	defer store.Stop()
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "session1", "server1")

	time.Sleep(70 * time.Millisecond)
	touched, err := store.Touch(ctx, "session1")
	require.NoError(t, err)
	assert.True(t, touched, "Touch should return true for existing session")

	time.Sleep(70 * time.Millisecond)
	authed, err := store.IsAuthenticated(ctx, "session1", "server1")
	require.NoError(t, err)
	assert.True(t, authed, "auth should still be valid after Touch")
}

func TestInMemorySessionAuthStore_TouchNonexistent(t *testing.T) {
	store := NewInMemorySessionAuthStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	touched, err := store.Touch(ctx, "nonexistent")
	assert.NoError(t, err)
	assert.False(t, touched, "Touch should return false for nonexistent session")
}

func TestInMemorySessionAuthStore_TouchExpiredSession(t *testing.T) {
	ttl := 50 * time.Millisecond
	store := NewInMemorySessionAuthStore(ttl)
	defer store.Stop()
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "session1", "server1")

	require.Eventually(t, func() bool {
		authed, _ := store.IsAuthenticated(ctx, "session1", "server1")
		return !authed
	}, 5*time.Second, 5*time.Millisecond, "auth should expire")

	touched, err := store.Touch(ctx, "session1")
	assert.NoError(t, err)
	assert.False(t, touched, "Touch should return false for expired session")
}

func TestInMemorySessionAuthStore_TouchEmptySession(t *testing.T) {
	store := NewInMemorySessionAuthStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "session1", "server1")
	_ = store.Revoke(ctx, "session1", "server1")

	touched, err := store.Touch(ctx, "session1")
	assert.NoError(t, err)
	assert.False(t, touched, "Touch should return false when no servers are authenticated")
}

func TestInMemorySessionAuthStore_ReauthenticateAfterExpiry(t *testing.T) {
	ttl := 50 * time.Millisecond
	store := NewInMemorySessionAuthStore(ttl)
	defer store.Stop()
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "session1", "server1")

	require.Eventually(t, func() bool {
		authed, _ := store.IsAuthenticated(ctx, "session1", "server1")
		return !authed
	}, 5*time.Second, 5*time.Millisecond, "auth should expire")

	_ = store.MarkAuthenticated(ctx, "session1", "server1")
	authed, err := store.IsAuthenticated(ctx, "session1", "server1")
	require.NoError(t, err)
	assert.True(t, authed, "should be authenticated after re-marking")
}

func TestInMemorySessionAuthStore_ConcurrentAccess(t *testing.T) {
	store := NewInMemorySessionAuthStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	var wg sync.WaitGroup
	const goroutines = 50

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessionID := "session-A"
			server := "server"
			if i%2 == 0 {
				sessionID = "session-B"
				server = "server2"
			}
			_ = store.MarkAuthenticated(ctx, sessionID, server)
		}(i)
	}

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.IsAuthenticated(ctx, "session-A", "server")
			_, _ = store.IsAuthenticated(ctx, "session-B", "server2")
		}()
	}

	for range goroutines / 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.Revoke(ctx, "session-A", "server")
			_ = store.RevokeSession(ctx, "session-A")
			_ = store.RevokeServer(ctx, "server2")
		}()
	}

	wg.Wait()
}

func TestInMemorySessionAuthStore_StopCleansTimers(t *testing.T) {
	store := NewInMemorySessionAuthStore(50 * time.Millisecond)
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "s1", "srv1")
	_ = store.MarkAuthenticated(ctx, "s2", "srv2")

	done := make(chan struct{})
	go func() {
		store.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return in time")
	}
}

func TestInMemorySessionAuthStore_RevokeNonexistent(t *testing.T) {
	store := NewInMemorySessionAuthStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	err := store.Revoke(ctx, "nonexistent", "server1")
	assert.NoError(t, err, "revoking from nonexistent session should not error")

	err = store.RevokeSession(ctx, "nonexistent")
	assert.NoError(t, err, "revoking nonexistent session should not error")

	err = store.RevokeServer(ctx, "nonexistent")
	assert.NoError(t, err, "revoking nonexistent server should not error")
}

func TestInMemorySessionAuthStore_MarkIdempotent(t *testing.T) {
	store := NewInMemorySessionAuthStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.MarkAuthenticated(ctx, "session1", "server1")
	_ = store.MarkAuthenticated(ctx, "session1", "server1")

	authed, err := store.IsAuthenticated(ctx, "session1", "server1")
	require.NoError(t, err)
	assert.True(t, authed, "double MarkAuthenticated should still be authenticated")
}
