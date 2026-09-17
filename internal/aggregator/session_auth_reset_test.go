package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
)

func TestSessionAuthInvalidated(t *testing.T) {
	forward := &api.MCPServerAuth{Type: "oauth", ForwardToken: true}
	pin := func(issuer, grantScope string, secret *api.ClientCredentialsSecretRef) *api.MCPServerAuth {
		return &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
			Issuer: issuer, GrantScope: grantScope, ClientCredentialsSecretRef: secret,
		}}
	}
	exchange := func(endpoint string) *api.MCPServerAuth {
		return &api.MCPServerAuth{Type: "oauth", TokenExchange: &api.TokenExchangeConfig{Enabled: true, DexTokenEndpoint: endpoint, ConnectorID: "ldap"}}
	}

	tests := []struct {
		name        string
		previous    *api.MCPServerAuth
		current     *api.MCPServerAuth
		invalidated bool
		changed     string
	}{
		{"first registration invalidates nothing", nil, pin("https://as.example.com", "subject", nil), false, ""},
		{"unchanged forwardToken", forward, &api.MCPServerAuth{Type: "oauth", ForwardToken: true}, false, ""},
		{"unchanged pin", pin("https://as.example.com", "subject", nil), pin("https://as.example.com", "subject", nil), false, ""},
		{"pin with a trailing slash is the same pin", pin("https://as.example.com/", "", nil), pin("https://as.example.com", "", nil), false, ""},
		{"nil current reads as empty", &api.MCPServerAuth{}, nil, false, ""},
		{"forwardToken to a pinned authorization server", forward, pin("https://as.example.com", "subject", nil), true, "forwardToken"},
		{"pinned authorization server to forwardToken", pin("https://as.example.com", "subject", nil), forward, true, "forwardToken"},
		{"another issuer", pin("https://as.example.com", "subject", nil), pin("https://other.example.com", "subject", nil), true, "authorizationServer"},
		{"another grant scope", pin("https://as.example.com", "", nil), pin("https://as.example.com", "subject", nil), true, "authorizationServer"},
		{"another client Secret", pin("https://as.example.com", "subject", &api.ClientCredentialsSecretRef{Name: "a"}), pin("https://as.example.com", "subject", &api.ClientCredentialsSecretRef{Name: "b"}), true, "authorizationServer"},
		{"expectedIssuer added", pin("https://as.example.com", "subject", nil), &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
			Issuer: "https://as.example.com", GrantScope: "subject", ExpectedIssuer: "https://login.example.com",
			AuthorizationEndpoint: "https://as.example.com/authorize", TokenEndpoint: "https://as.example.com/token",
		}}, true, "authorizationServer"},
		{"pin removed", pin("https://as.example.com", "", nil), &api.MCPServerAuth{Type: "oauth"}, true, "authorizationServer"},
		{"token exchange enabled", forward, exchange("https://dex.example.com/token"), true, "forwardToken"},
		{"token exchange endpoint changed", exchange("https://dex.example.com/token"), exchange("https://dex2.example.com/token"), true, "tokenExchange"},
		{"token exchange unchanged", exchange("https://dex.example.com/token"), exchange("https://dex.example.com/token"), false, ""},
		{"required audiences changed", forward, &api.MCPServerAuth{Type: "oauth", ForwardToken: true, RequiredAudiences: []string{"kubernetes"}}, true, "requiredAudiences"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invalidated, changed := sessionAuthInvalidated(tt.previous, tt.current)
			assert.Equal(t, tt.invalidated, invalidated)
			assert.Equal(t, tt.changed, changed)
		})
	}
}

// resetFixture is an aggregator manager with one pending-auth server two
// sessions are connected to (authenticated mark, cached capabilities, pooled
// client), plus a second server one of them is connected to as a control.
type resetFixture struct {
	am      *AggregatorManager
	agg     *AggregatorServer
	clients map[string]*poolTestClient // "<session>/<server>"
}

func newResetFixture(t *testing.T, auth *api.MCPServerAuth) *resetFixture {
	t.Helper()

	reg := NewServerRegistry("x")
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "svc", ToolPrefix: "svc"},
		URL:                "https://svc.example.com/mcp",
		AuthConfig:         auth,
	}))
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "other", ToolPrefix: "other"},
		URL:                "https://other.example.com/mcp",
		AuthConfig:         &api.MCPServerAuth{Type: "oauth"},
	}))

	authStore := oauthstore.NewInMemorySessionAuthStore(time.Hour)
	t.Cleanup(authStore.Stop)
	capStore := oauthstore.NewInMemoryCapabilityStore(time.Hour)
	t.Cleanup(capStore.Stop)
	pool := NewSessionConnectionPool(time.Hour)
	t.Cleanup(pool.Stop)
	tracker := newSSOTracker()
	tracker.MarkSSOFailedWithReason("alice", "svc", "old configuration")
	tracker.MarkSSOFailedWithReason("alice", "other", "unrelated")

	f := &resetFixture{clients: map[string]*poolTestClient{}}
	ctx := context.Background()
	for _, pair := range [][2]string{{"session-a", "svc"}, {"session-b", "svc"}, {"session-a", "other"}} {
		sid, server := pair[0], pair[1]
		require.NoError(t, authStore.MarkAuthenticated(ctx, sid, server))
		require.NoError(t, capStore.Set(ctx, sid, server, &oauthstore.Capabilities{}))
		c := &poolTestClient{}
		pool.Put(sid, server, c)
		f.clients[sid+"/"+server] = c
	}

	f.agg = &AggregatorServer{
		registry:        reg,
		authStore:       authStore,
		capabilityStore: capStore,
		connPool:        pool,
		ssoTracker:      tracker,
	}
	f.am = &AggregatorManager{aggregatorServer: f.agg}
	return f
}

// state reports the session's authenticated mark, cached capabilities and open
// pooled client for the server.
func (f *resetFixture) state(t *testing.T, sessionID, server string) (authenticated, capabilities, pooled bool) {
	t.Helper()
	ctx := context.Background()
	authenticated, err := f.agg.authStore.IsAuthenticated(ctx, sessionID, server)
	require.NoError(t, err)
	capabilities, err = f.agg.capabilityStore.Exists(ctx, sessionID, server)
	require.NoError(t, err)
	_, pooled = f.agg.connPool.Get(sessionID, server)
	closed := f.clients[sessionID+"/"+server].closeCount.Load() > 0
	require.Equal(t, pooled, !closed, "a client leaves the pool by being closed")
	return authenticated, capabilities, pooled
}

func (f *resetFixture) reregister(t *testing.T, auth *api.MCPServerAuth) {
	t.Helper()
	require.NoError(t, f.am.RegisterServerPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "svc", ToolPrefix: "svc"},
		URL:                "https://svc.example.com/mcp",
		AuthConfig:         auth,
	}))
}

// The incident shape (#1276): sessions connected to a forwardToken server
// while its MCPServer is switched to a pinned authorization server. Every
// session is back to auth_required for the server -- mark revoked, pooled
// client closed, SSO failure record gone -- its cached tools stay resolvable,
// and nothing else is touched.
func TestRegisterServerPendingAuth_AuthConfigChangeResetsLiveSessions(t *testing.T) {
	f := newResetFixture(t, &api.MCPServerAuth{Type: "oauth", ForwardToken: true})

	f.reregister(t, &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
		Issuer: "https://github.example.com/login/oauth", GrantScope: api.GrantScopeSubject,
	}})

	for _, sid := range []string{"session-a", "session-b"} {
		authenticated, capabilities, pooled := f.state(t, sid, "svc")
		assert.False(t, authenticated, "%s: the mark made under forwardToken is revoked", sid)
		assert.True(t, capabilities, "%s: the cached tools stay resolvable so the next call answers with the sign-in", sid)
		assert.False(t, pooled, "%s: the forwardToken connection is closed", sid)
	}
	assert.False(t, f.agg.ssoTracker.HasSSOFailed("alice", "svc"), "the SSO failure under the old configuration is forgotten")

	authenticated, capabilities, pooled := f.state(t, "session-a", "other")
	assert.True(t, authenticated && capabilities && pooled, "the other server is untouched")
	assert.True(t, f.agg.ssoTracker.HasSSOFailed("alice", "other"))

	info, ok := f.agg.registry.GetServerInfo("svc")
	require.True(t, ok)
	require.NotNil(t, info.AuthConfig.AuthorizationServer)
	assert.Equal(t, api.GrantScopeSubject, info.AuthConfig.AuthorizationServer.GrantScope, "the registry carries the new configuration")
}

// A re-registration on an unchanged configuration -- a restart of the
// service, a retry of its probe -- is not a change: the sessions keep their
// connections.
func TestRegisterServerPendingAuth_UnchangedAuthConfigKeepsSessions(t *testing.T) {
	f := newResetFixture(t, &api.MCPServerAuth{Type: "oauth", ForwardToken: true})

	f.reregister(t, &api.MCPServerAuth{Type: "oauth", ForwardToken: true})

	for _, sid := range []string{"session-a", "session-b"} {
		authenticated, capabilities, pooled := f.state(t, sid, "svc")
		assert.True(t, authenticated && capabilities && pooled, "%s keeps its connection", sid)
	}
	assert.True(t, f.agg.ssoTracker.HasSSOFailed("alice", "svc"))
}

func TestSSOTracker_ClearServer(t *testing.T) {
	tracker := newSSOTracker()
	tracker.MarkSSOFailed("alice", "svc")
	tracker.MarkSSOFailed("bob", "svc")
	tracker.MarkSSOFailed("alice", "other")
	require.True(t, tracker.MarkSSOPendingIfNotPending("carol", "svc"))

	tracker.ClearServer("svc")

	assert.False(t, tracker.HasSSOFailed("alice", "svc"))
	assert.False(t, tracker.HasSSOFailed("bob", "svc"))
	assert.True(t, tracker.HasSSOFailed("alice", "other"))
	assert.True(t, tracker.MarkSSOPendingIfNotPending("carol", "svc"), "the pending slot is free again")
}
