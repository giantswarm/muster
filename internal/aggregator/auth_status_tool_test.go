package aggregator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// challengeMockOAuthHandler answers every challenge with a link naming the
// server, so a test can tell which server each link starts.
type challengeMockOAuthHandler struct {
	issuerMockOAuthHandler
	challenged []api.AuthChallengeParams
}

// PinIssuer, UnpinIssuer and PinnedDescriptionKey make the mock an
// api.IssuerPinner, as the real handler is, so a pinned server's login can
// record its pin.
func (m *challengeMockOAuthHandler) PinIssuer(string, api.IssuerPin)            {}
func (m *challengeMockOAuthHandler) UnpinIssuer(string)                         {}
func (m *challengeMockOAuthHandler) PinnedDescriptionKey(string) (string, bool) { return "", false }

func (m *challengeMockOAuthHandler) CreateAuthChallenge(_ context.Context, params api.AuthChallengeParams) (*api.AuthChallenge, error) {
	m.challenged = append(m.challenged, params)
	return &api.AuthChallenge{AuthURL: "https://muster.example.com/oauth/proxy/start?state=" + params.ServerName}, nil
}

// statusTestAggregator registers one server of every kind: a pinned grant
// filed under the person, an unpinned grant, a forwarded-token server and a
// pinned grant rebuilt without the 401-time auth info.
func statusTestAggregator(t *testing.T) *AggregatorServer {
	t.Helper()
	agg := &AggregatorServer{registry: NewServerRegistry("x")}
	pinned := &api.MCPServerAuthAuthorizationServer{
		Issuer:                "https://github.com/apps/example",
		AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
		TokenEndpoint:         "https://github.com/login/oauth/access_token",
		GrantScope:            api.GrantScopeSubject,
	}
	registrations := []PendingAuthRegistration{
		{
			ServerRegistration: ServerRegistration{Name: "agent-manager", ToolPrefix: "agent-manager"},
			URL:                "http://127.0.0.1:1/mcp",
			AuthInfo:           &AuthInfo{Issuer: pinned.Issuer, Scope: "repo", Resource: "http://127.0.0.1:1/mcp"},
			AuthConfig:         &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, AuthorizationServer: pinned},
		},
		{
			ServerRegistration: ServerRegistration{Name: "miro", ToolPrefix: "miro"},
			URL:                "http://127.0.0.1:1/miro",
			AuthInfo:           &AuthInfo{Issuer: "https://mcp.miro.com", Scope: "boards:read", Resource: "http://127.0.0.1:1/miro"},
			AuthConfig:         &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth},
		},
		{
			ServerRegistration: ServerRegistration{Name: "kubernetes", ToolPrefix: "kubernetes"},
			URL:                "http://127.0.0.1:1/k8s",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			AuthConfig:         &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, ForwardToken: true},
		},
	}
	for _, r := range registrations {
		require.NoError(t, agg.registry.RegisterPendingAuth(r))
	}
	// Rebuilt from the service's own data: no AuthInfo, the pin alone.
	require.NoError(t, agg.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "cluster-manager", ToolPrefix: "cluster-manager"},
		URL:                "http://127.0.0.1:1/cm",
		AuthConfig:         &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, AuthorizationServer: pinned},
	}))
	getServerInfo(t, agg.registry, "cluster-manager").SetAuthInfo(nil)
	return agg
}

func statusByName(servers []pkgoauth.ServerAuthStatus) map[string]pkgoauth.ServerAuthStatus {
	byName := make(map[string]pkgoauth.ServerAuthStatus, len(servers))
	for _, s := range servers {
		byName[s.Name] = s
	}
	return byName
}

func TestSessionAuthStatus_KindAndGrantScope(t *testing.T) {
	agg := statusTestAggregator(t)

	servers := statusByName(agg.sessionAuthStatus(testSessionCtx()).Servers)

	assert.Equal(t, pkgoauth.ServerAuthKindGrant, servers["agent-manager"].Kind)
	assert.Equal(t, "subject", servers["agent-manager"].GrantScope)
	assert.Equal(t, pkgoauth.ServerAuthKindGrant, servers["miro"].Kind)
	assert.Equal(t, "session", servers["miro"].GrantScope)
	assert.Equal(t, pkgoauth.ServerAuthKindSSO, servers["kubernetes"].Kind)
	assert.Empty(t, servers["kubernetes"].GrantScope)
	assert.Empty(t, servers["kubernetes"].AuthTool)
}

func TestSessionAuthStatus_GrantServerWithoutAuthInfoIsAuthRequired(t *testing.T) {
	agg := statusTestAggregator(t)

	cm := statusByName(agg.sessionAuthStatus(testSessionCtx()).Servers)["cluster-manager"]

	assert.Equal(t, pkgoauth.SessionServerStatusAuthRequired, cm.Status)
	assert.Equal(t, "core_auth_login", cm.AuthTool)
	assert.Equal(t, "https://github.com/apps/example", cm.Issuer)
}

func TestHandleAuthStatus_WithoutAuthURLsIssuesNoLink(t *testing.T) {
	agg := statusTestAggregator(t)
	handler := &challengeMockOAuthHandler{issuerMockOAuthHandler: issuerMockOAuthHandler{enabled: true}}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	result, err := NewAuthToolProvider(agg).ExecuteTool(testSessionCtx(), "auth_status", map[string]any{})
	require.NoError(t, err)
	require.False(t, result.IsError)

	response, ok := result.StructuredContent.(pkgoauth.AuthStatusResponse)
	require.True(t, ok)
	for _, s := range response.Servers {
		assert.Empty(t, s.AuthURL, s.Name)
	}
	assert.Empty(t, handler.challenged)
}

func TestHandleAuthStatus_AuthURLsForEveryMissingGrant(t *testing.T) {
	agg := statusTestAggregator(t)
	handler := &challengeMockOAuthHandler{issuerMockOAuthHandler: issuerMockOAuthHandler{enabled: true}}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	result, err := NewAuthToolProvider(agg).ExecuteTool(testSessionCtx(), "auth_status", map[string]any{authURLsArg: true})
	require.NoError(t, err)
	require.False(t, result.IsError)

	response, ok := result.StructuredContent.(pkgoauth.AuthStatusResponse)
	require.True(t, ok)
	servers := statusByName(response.Servers)
	for _, name := range []string{"agent-manager", "miro", "cluster-manager"} {
		assert.Empty(t, servers[name].Error, name)
		assert.Equal(t, "https://muster.example.com/oauth/proxy/start?state="+name, servers[name].AuthURL, name)
	}
	assert.Empty(t, servers["kubernetes"].AuthURL, "an SSO server never gets a link")
	for _, params := range handler.challenged {
		assert.Equal(t, "test-session", params.SessionID)
		assert.Equal(t, "test-user", params.UserID)
	}
}

func TestHandleAuthStatus_RequiresSession(t *testing.T) {
	agg := statusTestAggregator(t)

	result, err := NewAuthToolProvider(agg).ExecuteTool(context.Background(), "auth_status", map[string]any{authURLsArg: true})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestGrantable(t *testing.T) {
	base := pkgoauth.ServerAuthStatus{AuthTool: "core_auth_login", Status: pkgoauth.SessionServerStatusAuthRequired}
	assert.True(t, grantable(base))

	reauth := base
	reauth.Status = pkgoauth.SessionServerStatusReauthRequired
	assert.True(t, grantable(reauth))

	connected := base
	connected.Status = pkgoauth.SessionServerStatusConnected
	assert.False(t, grantable(connected))

	suspended := base
	suspended.Suspended = true
	assert.False(t, grantable(suspended))

	sso := base
	sso.AuthTool = ""
	assert.False(t, grantable(sso))
}
