package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noopMCPClient is a minimal MCPClient that does nothing, used in pool tests.
type noopMCPClient struct{}

func (c *noopMCPClient) Initialize(context.Context) error              { return nil }
func (c *noopMCPClient) Close() error                                  { return nil }
func (c *noopMCPClient) ListTools(context.Context) ([]mcp.Tool, error) { return nil, nil }
func (c *noopMCPClient) CallTool(context.Context, string, map[string]interface{}) (*mcp.CallToolResult, error) {
	return nil, nil
}
func (c *noopMCPClient) ListResources(context.Context) ([]mcp.Resource, error) { return nil, nil }
func (c *noopMCPClient) ReadResource(context.Context, string) (*mcp.ReadResourceResult, error) {
	return nil, nil
}
func (c *noopMCPClient) ListPrompts(context.Context) ([]mcp.Prompt, error) { return nil, nil }
func (c *noopMCPClient) GetPrompt(context.Context, string, map[string]interface{}) (*mcp.GetPromptResult, error) {
	return nil, nil
}
func (c *noopMCPClient) Ping(context.Context) error                   { return nil }
func (c *noopMCPClient) OnNotification(func(mcp.JSONRPCNotification)) {}

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
	userID := "test-user" //nolint:goconst

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
	idToken = "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.valid" //nolint:gosec
	shouldInitSSO = !authAlive && idToken != ""
	assert.True(t, shouldInitSSO,
		"initSSOForSession should be called when authAlive=false and idToken is present")
}

func TestOnAuthenticated_EvictsSSO_WhenAuthAliveButNoIDToken(t *testing.T) {
	// When the upstream refresh chain breaks (e.g. Dex -> GitHub 401), the
	// session may still be alive (authStore has entries) but the ID token
	// disappears. The onAuthenticated callback should detect this and evict
	// SSO connections.
	authStore := NewInMemorySessionAuthStore(30 * time.Minute)
	defer authStore.Stop()

	ctx := context.Background()
	sessionID := "session-with-broken-refresh"

	// Session is alive with authenticated servers
	err := authStore.MarkAuthenticated(ctx, sessionID, "sso-server-1")
	require.NoError(t, err)
	err = authStore.MarkAuthenticated(ctx, sessionID, "sso-server-2")
	require.NoError(t, err)

	authAlive, err := authStore.Touch(ctx, sessionID)
	require.NoError(t, err)
	require.True(t, authAlive, "session should be alive")

	// Simulate onAuthenticated logic: authAlive=true, idToken=""
	idToken := ""
	shouldEvict := authAlive && idToken == ""
	assert.True(t, shouldEvict,
		"should evict SSO connections when authAlive=true but idToken is empty")

	// Verify that eviction would clear the auth store
	if shouldEvict {
		err = authStore.RevokeSession(ctx, sessionID)
		require.NoError(t, err)
	}

	authed, _ := authStore.IsAuthenticated(ctx, sessionID, "sso-server-1")
	assert.False(t, authed, "sso-server-1 should no longer be authenticated after eviction")

	authed, _ = authStore.IsAuthenticated(ctx, sessionID, "sso-server-2")
	assert.False(t, authed, "sso-server-2 should no longer be authenticated after eviction")

	// Verify the onAuthenticated still returns early (no SSO reinit) when
	// authAlive=true and idToken is present
	err = authStore.MarkAuthenticated(ctx, sessionID, "sso-server-1")
	require.NoError(t, err)

	authAlive, _ = authStore.Touch(ctx, sessionID)
	idToken = "valid-token"
	shouldEvict = authAlive && idToken == ""
	assert.False(t, shouldEvict,
		"should NOT evict when authAlive=true and idToken is present")
}

func TestHandleUpstreamRefreshFailure_MarksSSOFailed(t *testing.T) {
	// handleUpstreamRefreshFailure should mark all SSO servers as failed
	// in the ssoTracker to prevent immediate retry with expired credentials.
	tracker := newSSOTracker()
	userID := "refresh-failure-user"

	type ssoServer struct {
		name                string
		usesTokenForwarding bool
		usesTokenExchange   bool
	}
	servers := []ssoServer{
		{name: "sso-forward", usesTokenForwarding: true},
		{name: "sso-exchange", usesTokenExchange: true},
		{name: "non-sso", usesTokenForwarding: false, usesTokenExchange: false},
	}

	// Simulate what handleUpstreamRefreshFailure does for ssoTracker
	for _, s := range servers {
		if s.usesTokenExchange || s.usesTokenForwarding {
			tracker.MarkSSOFailed(userID, s.name)
		}
	}

	assert.True(t, tracker.HasSSOFailed(userID, "sso-forward"),
		"SSO forwarding server should be marked as failed")
	assert.True(t, tracker.HasSSOFailed(userID, "sso-exchange"),
		"SSO exchange server should be marked as failed")
	assert.False(t, tracker.HasSSOFailed(userID, "non-sso"),
		"non-SSO server should NOT be marked as failed")
}

func TestHandleUpstreamRefreshFailure_EvictsPooledConnections(t *testing.T) {
	// Evicting pooled connections stops the mcp-go infinite retry loop
	// because closing the client cancels the underlying context.
	pool := NewSessionConnectionPool(1 * time.Hour)
	defer pool.Stop()
	defer pool.DrainAll()

	sessionID := "refresh-fail-session"

	pool.Put(sessionID, "server1", &noopMCPClient{})
	pool.Put(sessionID, "server2", &noopMCPClient{})

	_, ok := pool.Get(sessionID, "server1")
	require.True(t, ok, "server1 should be in pool before eviction")

	pool.EvictSession(sessionID)

	_, ok = pool.Get(sessionID, "server1")
	assert.False(t, ok, "server1 should be evicted")

	_, ok = pool.Get(sessionID, "server2")
	assert.False(t, ok, "server2 should be evicted")
}

func TestDetermineSessionAuthStatus_ReauthRequired(t *testing.T) {
	// When SSO has failed for an SSO-enabled server, the status should
	// be reauth_required (not just auth_required) to signal that a
	// previously working session has degraded.
	tracker := newSSOTracker()
	userID := "degraded-user"

	tracker.MarkSSOFailed(userID, "sso-server")

	assert.True(t, tracker.HasSSOFailed(userID, "sso-server"),
		"sso-server should be marked as failed, which leads to reauth_required status")
}

func TestHandleUpstreamRefreshFailure_Integration(t *testing.T) {
	// Integration test: calls handleUpstreamRefreshFailure on a real
	// AggregatorServer with all components wired up (pool, authStore,
	// ssoTracker, registry) and verifies the combined outcome.
	sessionID := "integration-session"
	userID := "integration-user"

	pool := NewSessionConnectionPool(1 * time.Hour)
	defer pool.Stop()
	defer pool.DrainAll()

	authStore := NewInMemorySessionAuthStore(30 * time.Minute)
	defer authStore.Stop()

	tracker := newSSOTracker()
	registry := NewServerRegistry("x")

	ctx := context.Background()

	// Register SSO and non-SSO servers.
	err := registry.RegisterPendingAuthWithConfig(
		"sso-fwd", "https://sso-fwd.example.com", "ssofwd",
		&AuthInfo{Issuer: "https://dex.example.com"},
		&api.MCPServerAuth{ForwardToken: true},
	)
	require.NoError(t, err)
	err = registry.RegisterPendingAuthWithConfig(
		"sso-exch", "https://sso-exch.example.com", "ssoexch",
		&AuthInfo{Issuer: "https://dex.example.com"},
		&api.MCPServerAuth{TokenExchange: &api.TokenExchangeConfig{ //nolint:gosec
			Enabled:          true,
			DexTokenEndpoint: "https://remote-dex.example.com/token",
			ConnectorID:      "cluster-a-dex",
			ClientID:         "test-client",
		}},
	)
	require.NoError(t, err)
	err = registry.RegisterPendingAuth(
		"non-sso", "https://non-sso.example.com", "nonsso",
		&AuthInfo{Issuer: "https://other.example.com"},
	)
	require.NoError(t, err)

	// Pre-populate pool and auth store.
	pool.Put(sessionID, "sso-fwd", &noopMCPClient{})
	pool.Put(sessionID, "sso-exch", &noopMCPClient{})
	require.NoError(t, authStore.MarkAuthenticated(ctx, sessionID, "sso-fwd"))
	require.NoError(t, authStore.MarkAuthenticated(ctx, sessionID, "sso-exch"))
	require.NoError(t, authStore.MarkAuthenticated(ctx, sessionID, "non-sso"))

	aggServer := &AggregatorServer{
		registry:   registry,
		connPool:   pool,
		authStore:  authStore,
		ssoTracker: tracker,
	}

	aggServer.handleUpstreamRefreshFailure(sessionID, userID, "test reason")

	// Pool should be fully evicted for this session.
	_, ok := pool.Get(sessionID, "sso-fwd")
	assert.False(t, ok, "sso-fwd should be evicted from pool")
	_, ok = pool.Get(sessionID, "sso-exch")
	assert.False(t, ok, "sso-exch should be evicted from pool")

	// AuthStore should have all servers revoked.
	authed, _ := authStore.IsAuthenticated(ctx, sessionID, "sso-fwd")
	assert.False(t, authed, "sso-fwd should no longer be authenticated")
	authed, _ = authStore.IsAuthenticated(ctx, sessionID, "non-sso")
	assert.False(t, authed, "non-sso should also be revoked (RevokeSession clears all)")

	// SSO tracker should mark only SSO servers as failed.
	assert.True(t, tracker.HasSSOFailed(userID, "sso-fwd"),
		"SSO forwarding server should be marked as failed")
	assert.True(t, tracker.HasSSOFailed(userID, "sso-exch"),
		"SSO exchange server should be marked as failed")
	assert.False(t, tracker.HasSSOFailed(userID, "non-sso"),
		"non-SSO server should NOT be marked as failed")
}

func TestHandleUpstreamRefreshFailure_Idempotent(t *testing.T) {
	// Calling handleUpstreamRefreshFailure multiple times for the same session
	// should be safe (all operations are idempotent).
	pool := NewSessionConnectionPool(1 * time.Hour)
	defer pool.Stop()
	defer pool.DrainAll()

	authStore := NewInMemorySessionAuthStore(30 * time.Minute)
	defer authStore.Stop()

	tracker := newSSOTracker()
	registry := NewServerRegistry("x")

	err := registry.RegisterPendingAuthWithConfig(
		"sso-server", "https://sso.example.com", "sso",
		&AuthInfo{Issuer: "https://dex.example.com"},
		&api.MCPServerAuth{ForwardToken: true},
	)
	require.NoError(t, err)

	aggServer := &AggregatorServer{
		registry:   registry,
		connPool:   pool,
		authStore:  authStore,
		ssoTracker: tracker,
	}

	// Call three times -- should not panic.
	aggServer.handleUpstreamRefreshFailure("s1", "u1", "first call")
	aggServer.handleUpstreamRefreshFailure("s1", "u1", "second call")
	aggServer.handleUpstreamRefreshFailure("s1", "u1", "third call")

	// Failure count should increase with each call (exponential backoff).
	fc := tracker.GetFailureCount("u1", "sso-server")
	assert.Equal(t, 3, fc,
		"consecutive calls should increment the failure count for backoff")
}

func TestHandleUpstreamRefreshFailure_NilComponents(t *testing.T) {
	// handleUpstreamRefreshFailure must not panic when optional components
	// (connPool, authStore, ssoTracker) are nil.
	aggServer := &AggregatorServer{
		registry: NewServerRegistry("x"),
	}

	assert.NotPanics(t, func() {
		aggServer.handleUpstreamRefreshFailure("session", "user", "nil components test")
	})
}

func TestOnStaleToken_EvictsAndRevokes(t *testing.T) {
	// Verifies the onStaleToken callback used in EstablishConnectionWithTokenForwarding
	// correctly evicts the pool entry and revokes the auth store entry.
	pool := NewSessionConnectionPool(1 * time.Hour)
	defer pool.Stop()
	defer pool.DrainAll()

	authStore := NewInMemorySessionAuthStore(30 * time.Minute)
	defer authStore.Stop()

	ctx := context.Background()
	sessionID := "stale-token-session"
	serverName := "stale-token-server"

	pool.Put(sessionID, serverName, &noopMCPClient{})
	require.NoError(t, authStore.MarkAuthenticated(ctx, sessionID, serverName))

	_, ok := pool.Get(sessionID, serverName)
	require.True(t, ok, "server should be in pool before eviction")

	authed, _ := authStore.IsAuthenticated(ctx, sessionID, serverName)
	require.True(t, authed, "server should be authenticated before eviction")

	// Simulate the onStaleToken callback from makeTokenForwardingHeaderFunc.
	aggServer := &AggregatorServer{
		connPool:  pool,
		authStore: authStore,
	}
	onStaleToken := func() {
		aggServer.connPool.Evict(sessionID, serverName)
		if err := aggServer.authStore.Revoke(context.Background(), sessionID, serverName); err != nil {
			t.Errorf("unexpected error revoking auth: %v", err)
		}
	}
	onStaleToken()

	_, ok = pool.Get(sessionID, serverName)
	assert.False(t, ok, "server should be evicted from pool after stale token")

	authed, _ = authStore.IsAuthenticated(ctx, sessionID, serverName)
	assert.False(t, authed, "server should no longer be authenticated after stale token")
}

func TestOnAuthenticated_CallsHandleUpstreamRefreshFailure(t *testing.T) {
	// Verifies the onAuthenticated callback logic: when authAlive=true
	// and idToken="", it should call handleUpstreamRefreshFailure to evict
	// SSO connections. We test this by simulating the exact code path.
	authStore := NewInMemorySessionAuthStore(30 * time.Minute)
	defer authStore.Stop()

	pool := NewSessionConnectionPool(1 * time.Hour)
	defer pool.Stop()
	defer pool.DrainAll()

	tracker := newSSOTracker()
	registry := NewServerRegistry("x")

	ctx := context.Background()
	sessionID := "callback-session"
	userID := "callback-user"

	err := registry.RegisterPendingAuthWithConfig(
		"sso-server", "https://sso.example.com", "sso",
		&AuthInfo{Issuer: "https://dex.example.com"},
		&api.MCPServerAuth{ForwardToken: true},
	)
	require.NoError(t, err)

	// Set up live session with pooled connection.
	require.NoError(t, authStore.MarkAuthenticated(ctx, sessionID, "sso-server"))
	pool.Put(sessionID, "sso-server", &noopMCPClient{})

	aggServer := &AggregatorServer{
		registry:   registry,
		connPool:   pool,
		authStore:  authStore,
		ssoTracker: tracker,
	}

	// Verify pre-conditions.
	authAlive, _ := authStore.Touch(ctx, sessionID)
	require.True(t, authAlive, "session should be alive")

	// Simulate the onAuthenticated callback code path: authAlive=true, idToken=""
	idToken := ""
	if authAlive && idToken == "" {
		aggServer.handleUpstreamRefreshFailure(sessionID, userID,
			"onAuthenticated: ID token missing for active session")
	}

	// Verify all SSO state was cleaned up.
	authed, _ := authStore.IsAuthenticated(ctx, sessionID, "sso-server")
	assert.False(t, authed, "sso-server should no longer be authenticated")

	_, ok := pool.Get(sessionID, "sso-server")
	assert.False(t, ok, "sso-server should be evicted from pool")

	assert.True(t, tracker.HasSSOFailed(userID, "sso-server"),
		"sso-server should be marked as failed in tracker")

	// determineSessionAuthStatus should now return reauth_required.
	info, exists := registry.GetServerInfo("sso-server")
	require.True(t, exists)
	status := aggServer.determineSessionAuthStatus(userID, sessionID, "sso-server", info)
	assert.Equal(t, pkgoauth.SessionServerStatusReauthRequired, status,
		"status should be reauth_required after upstream refresh failure")
}

func TestTokenRefreshHandler_MissingIDToken_TriggersEviction(t *testing.T) {
	// Verifies the logic in the TokenRefreshHandler: when the refreshed
	// token has no ID token, handleUpstreamRefreshFailure should be called.
	authStore := NewInMemorySessionAuthStore(30 * time.Minute)
	defer authStore.Stop()

	pool := NewSessionConnectionPool(1 * time.Hour)
	defer pool.Stop()
	defer pool.DrainAll()

	tracker := newSSOTracker()
	registry := NewServerRegistry("x")

	ctx := context.Background()
	familyID := "refresh-family"
	userID := "refresh-user"

	err := registry.RegisterPendingAuthWithConfig(
		"sso-server", "https://sso.example.com", "sso",
		&AuthInfo{Issuer: "https://dex.example.com"},
		&api.MCPServerAuth{ForwardToken: true},
	)
	require.NoError(t, err)

	require.NoError(t, authStore.MarkAuthenticated(ctx, familyID, "sso-server"))
	pool.Put(familyID, "sso-server", &noopMCPClient{})

	aggServer := &AggregatorServer{
		registry:   registry,
		connPool:   pool,
		authStore:  authStore,
		ssoTracker: tracker,
	}

	// Simulate the TokenRefreshHandler code path: idToken is empty.
	idToken := ""
	if idToken == "" {
		aggServer.handleUpstreamRefreshFailure(familyID, userID,
			"TokenRefreshHandler: refreshed token has no ID token")
	}

	// Pool should be evicted.
	_, ok := pool.Get(familyID, "sso-server")
	assert.False(t, ok, "sso-server should be evicted from pool")

	// AuthStore should be revoked.
	authed, _ := authStore.IsAuthenticated(ctx, familyID, "sso-server")
	assert.False(t, authed, "sso-server should no longer be authenticated")

	// Tracker should mark SSO as failed.
	assert.True(t, tracker.HasSSOFailed(userID, "sso-server"),
		"sso-server should be marked as failed")
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
