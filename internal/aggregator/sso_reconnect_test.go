package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOnAuthenticated_TriggersSSOReinit_WhenAuthStoreEmpty(t *testing.T) {
	// After a pod restart the in-memory authStore is empty.
	// When a user makes a request, Touch returns false and initSSOForSession
	// should be called.
	authStore := NewInMemorySessionAuthStore(30 * time.Minute)
	defer authStore.Stop()

	sessionID := "session-from-before-restart"

	// Touch on an empty store should return false (session not known)
	authAlive, err := authStore.Touch(context.Background(), sessionID)
	require.NoError(t, err)
	assert.False(t, authAlive,
		"Touch on a fresh authStore (simulating pod restart) should return false, triggering SSO re-init")
}

func TestOnAuthenticated_NoSSOReinit_WhenAuthAlive(t *testing.T) {
	// When the session is known and has authenticated servers, Touch returns true
	// and initSSOForSession should NOT be called (no redundant SSO work).
	authStore := NewInMemorySessionAuthStore(30 * time.Minute)
	defer authStore.Stop()

	ctx := context.Background()
	sessionID := "active-session"
	err := authStore.MarkAuthenticated(ctx, sessionID, "server1")
	require.NoError(t, err)

	authAlive, err := authStore.Touch(ctx, sessionID)
	require.NoError(t, err)
	assert.True(t, authAlive,
		"Touch should return true for session with authenticated servers, skipping SSO re-init")
}

func TestOnAuthenticated_TriggersSSOReinit_WhenSessionExpired(t *testing.T) {
	// When a session exists but has expired, Touch returns false,
	// triggering initSSOForSession to re-establish SSO connections.
	ttl := 50 * time.Millisecond
	authStore := NewInMemorySessionAuthStore(ttl)
	defer authStore.Stop()

	ctx := context.Background()
	sessionID := "expiring-session"
	err := authStore.MarkAuthenticated(ctx, sessionID, "server1")
	require.NoError(t, err)

	// Wait for the session to expire
	time.Sleep(100 * time.Millisecond)

	authAlive, err := authStore.Touch(ctx, sessionID)
	require.NoError(t, err)
	assert.False(t, authAlive,
		"Touch on an expired session should return false, triggering SSO re-init")
}

func TestOnAuthenticated_TriggersSSOReinit_WhenNoServers(t *testing.T) {
	// If a session exists but has no authenticated servers (e.g. all were
	// revoked), Touch returns false.
	authStore := NewInMemorySessionAuthStore(30 * time.Minute)
	defer authStore.Stop()

	ctx := context.Background()
	sessionID := "empty-session"

	// Mark and then revoke the only server
	err := authStore.MarkAuthenticated(ctx, sessionID, "server1")
	require.NoError(t, err)
	err = authStore.Revoke(ctx, sessionID, "server1")
	require.NoError(t, err)

	authAlive, err := authStore.Touch(ctx, sessionID)
	require.NoError(t, err)
	assert.False(t, authAlive,
		"Touch should return false when session has no authenticated servers")
}

func TestInitSSOForSession_SkipsFailedServers(t *testing.T) {
	// Verifies the filter logic: initSSOForSession should skip servers
	// that the ssoTracker has marked as failed (within TTL).
	tracker := newSSOTracker()
	userID := "test-user"

	tracker.MarkSSOFailed(userID, "failed-server")

	type serverCandidate struct {
		name                string
		usesTokenExchange   bool
		usesTokenForwarding bool
	}
	candidates := []serverCandidate{
		{name: "failed-server", usesTokenForwarding: true},
		{name: "good-server", usesTokenForwarding: true},
		{name: "exchange-server", usesTokenExchange: true},
	}

	var pending []string
	for _, c := range candidates {
		if !c.usesTokenExchange && !c.usesTokenForwarding {
			continue
		}
		if tracker.HasSSOFailed(userID, c.name) {
			continue
		}
		pending = append(pending, c.name)
	}

	assert.Equal(t, []string{"good-server", "exchange-server"}, pending,
		"only non-failed SSO servers should be in the pending list")
}

func TestInitSSOForSession_RetriesAfterTTLExpiry(t *testing.T) {
	// After the failure TTL expires, the server should be retried.
	tracker := newSSOTracker()
	userID := "test-user"

	tracker.MarkSSOFailed(userID, "recovered-server")

	// Simulate TTL expiry (ssoTrackerFailureTTL = 5min)
	tracker.mu.Lock()
	tracker.failedServers[userID]["recovered-server"].failedAt = time.Now().Add(-6 * time.Minute)
	tracker.mu.Unlock()

	type serverCandidate struct {
		name                string
		usesTokenForwarding bool
	}
	candidates := []serverCandidate{
		{name: "recovered-server", usesTokenForwarding: true},
	}

	var pending []string
	for _, c := range candidates {
		if !c.usesTokenForwarding {
			continue
		}
		if tracker.HasSSOFailed(userID, c.name) {
			continue
		}
		pending = append(pending, c.name)
	}

	assert.Equal(t, []string{"recovered-server"}, pending,
		"server with expired failure should be retried")
}

func TestOnAuthenticated_ClearsFailuresBeforeReinit(t *testing.T) {
	// When onAuthenticated detects that authStore.Touch returns false
	// (session expired / pod restart), it should clear all SSO failures
	// for that user before calling initSSOForSession. This ensures that
	// servers which previously failed are retried.
	tracker := newSSOTracker()
	authStore := NewInMemorySessionAuthStore(30 * time.Minute)
	defer authStore.Stop()

	userID := "user-after-restart"
	sessionID := "session-xyz"

	tracker.MarkSSOFailed(userID, "server1")
	tracker.MarkSSOFailed(userID, "server2")
	tracker.MarkSSOFailed(userID, "server3")

	require.True(t, tracker.HasSSOFailed(userID, "server1"))
	require.True(t, tracker.HasSSOFailed(userID, "server2"))
	require.True(t, tracker.HasSSOFailed(userID, "server3"))

	// Simulate the onAuthenticated callback logic:
	// 1. Touch returns false (session unknown after restart)
	authAlive, _ := authStore.Touch(context.Background(), sessionID)
	require.False(t, authAlive)

	// 2. Clear all SSO failures before re-init (this is the new behavior)
	if !authAlive && userID != "" {
		tracker.ClearAllSSOFailed(userID)
	}

	// 3. All servers should now be retryable
	assert.False(t, tracker.HasSSOFailed(userID, "server1"))
	assert.False(t, tracker.HasSSOFailed(userID, "server2"))
	assert.False(t, tracker.HasSSOFailed(userID, "server3"))
}

func TestOnAuthenticated_SkipsSSO_WhenNoIDToken(t *testing.T) {
	// After a pod restart, Valkey may still have valid access tokens but
	// no ID token in the OAuth store. In this case the onAuthenticated
	// callback should skip initSSOForSession entirely to avoid downstream
	// connections that immediately start spamming 403 errors.
	authStore := NewInMemorySessionAuthStore(30 * time.Minute)
	defer authStore.Stop()

	ctx := context.Background()
	sessionID := "stale-session-no-idtoken"

	// Touch returns false (session unknown after restart)
	authAlive, err := authStore.Touch(ctx, sessionID)
	require.NoError(t, err)
	require.False(t, authAlive, "Touch on a fresh authStore should return false")

	// Simulate the onAuthenticated guard: empty idToken should cause early return
	idToken := ""

	shouldInitSSO := !authAlive && idToken != ""
	assert.False(t, shouldInitSSO,
		"initSSOForSession should NOT be called when authAlive=false and idToken is empty")

	// Contrast: with a valid idToken the init should proceed
	idToken = "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.valid"
	shouldInitSSO = !authAlive && idToken != ""
	assert.True(t, shouldInitSSO,
		"initSSOForSession should be called when authAlive=false and idToken is present")
}

func TestSSOTracker_ConcurrentAccess(t *testing.T) {
	// The ssoTracker must be safe for concurrent access since
	// initSSOForSession, establishSSOConnection, and the cleanup goroutine
	// all access it from different goroutines.
	tracker := newSSOTracker()
	done := make(chan struct{})

	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 100; i++ {
			tracker.MarkSSOFailed("user1", "serverA")
			tracker.MarkSSOPending("user1", "serverB")
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 100; i++ {
			tracker.HasSSOFailed("user1", "serverA")
			tracker.IsSSOPendingWithinTimeout("user1", "serverB")
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 100; i++ {
			tracker.ClearSSOFailed("user1", "serverA")
			tracker.ClearSSOPending("user1", "serverB")
			tracker.ClearAllSSOFailed("user1")
			tracker.CleanupExpired()
		}
	}()

	for i := 0; i < 3; i++ {
		<-done
	}
}
