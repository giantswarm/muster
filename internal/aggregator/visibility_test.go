package aggregator

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/server"
)

// fixedVisibility is a visibility handler with one answer for every caller.
type fixedVisibility struct{ view api.ServerVisibility }

func (f fixedVisibility) CallerVisibility(context.Context) api.ServerVisibility { return f.view }
func (f fixedVisibility) LoginAudience() string                                 { return "" }

func useVisibility(t *testing.T, view api.ServerVisibility) {
	t.Helper()
	api.RegisterMCPServerVisibility(fixedVisibility{view: view})
	t.Cleanup(func() { api.RegisterMCPServerVisibility(nil) })
}

// visibilityTestServer registers a server called github in muster's
// namespace, in org-a and in org-b, each connected with a tool, a resource
// and a prompt, and a server of org-b awaiting the session's sign-in.
func visibilityTestServer(t *testing.T) *AggregatorServer {
	t.Helper()
	ctx := context.Background()
	a := newSuspensionTestServer(t)
	for _, name := range []string{"github", "org-a/github", "org-b/github"} {
		client := &mockMCPClient{
			tools:     []mcp.Tool{{Name: "create_issue"}},
			resources: []mcp.Resource{{URI: "repo://" + strings.ReplaceAll(name, "/", "-"), Name: "repo"}},
			prompts:   []mcp.Prompt{{Name: "triage"}},
		}
		require.NoError(t, a.registry.Register(ctx, ServerRegistration{Name: name}, client))
	}
	registerSuspendableServer(t, a.registry, "org-b/hubspot", false, api.StateAuthRequired)
	// The global catalogue (updateCapabilities) knows every exposed name, so
	// a call names a server's tool that resolves; only visibility refuses it.
	a.registry.GetAllTools()
	return a
}

func toolNames(tools []mcp.Tool) []string {
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// agent-permissions D9: a server the caller may not see is absent from every
// listing and refused on every call. Without a visibility handler (the
// setting off) every caller sees every server, as before.
func TestVisibility_ServerTheCallerMayNotSeeIsAbsent(t *testing.T) {
	ctx := testSessionCtx()
	const hiddenTool = "x_org-b_github_create_issue"

	t.Run("setting off: every server, as before", func(t *testing.T) {
		a := visibilityTestServer(t)
		assert.Equal(t, []string{"x_github_create_issue", "x_org-a_github_create_issue", hiddenTool}, toolNames(a.GetToolsForSession(ctx, "test-session")))
		_, err := a.CallToolInternal(ctx, hiddenTool, nil)
		assert.NoError(t, err)
	})

	t.Run("a member of org-a", func(t *testing.T) {
		a := visibilityTestServer(t)
		useVisibility(t, api.ServersOfNamespacesVisible("muster", []string{"muster", "org-a"}))

		assert.Equal(t, []string{"x_github_create_issue", "x_org-a_github_create_issue"}, toolNames(a.GetToolsForSession(ctx, "test-session")))
		for _, r := range a.GetResourcesForSession(ctx, "test-session") {
			assert.NotEqual(t, "org-b/github", r.Server, "resource %s", r.Resource.URI)
		}
		for _, p := range a.GetPromptsForSession(ctx, "test-session") {
			assert.NotEqual(t, "org-b/github", p.Server, "prompt %s", p.Prompt.Name)
		}
		for _, s := range a.ListServersRequiringAuth(ctx) {
			assert.NotEqual(t, "org-b/hubspot", s.Name)
		}
		for _, s := range a.sessionAuthStatus(ctx).Servers {
			assert.NotContains(t, s.Name, "org-b/")
		}

		res, err := a.CallToolInternal(ctx, hiddenTool, nil)
		assert.Nil(t, res)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tool not found")
		_, err = a.CallToolInternal(ctx, "x_org-a_github_create_issue", nil)
		assert.NoError(t, err, "a visible server's tool still runs")

		login, err := NewAuthToolProvider(a).handleAuthLogin(ctx, map[string]any{"server": "org-b/hubspot"})
		require.NoError(t, err)
		assert.True(t, login.IsError)
		assert.Contains(t, login.Content[0], "not found", "core_auth_login answers as for a missing server")
	})

	t.Run("a token the API server does not accept sees no server and is told to sign in again", func(t *testing.T) {
		a := visibilityTestServer(t)
		useVisibility(t, api.NoServerVisible("A re-login is required: sign in again."))

		assert.Empty(t, a.GetToolsForSession(ctx, "test-session"))
		assert.Empty(t, a.GetResourcesForSession(ctx, "test-session"))
		assert.Empty(t, a.ListServersRequiringAuth(ctx))
		_, err := a.CallToolInternal(ctx, "x_github_create_issue", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sign in again")
	})
}

// The SSO fan-out connects only servers the person may see, judged with the
// session's own token: a server the person may not see gets no forwarded or
// exchanged token.
func TestPlanSessionBootstrap_SkipsServersTheCallerMayNotSee(t *testing.T) {
	a := &AggregatorServer{registry: NewServerRegistry("x")}
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "kubernetes", ToolPrefix: "kubernetes"},
		URL:                "http://127.0.0.1:1/mcp",
		AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		AuthConfig:         &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, ForwardToken: true},
	}))
	sso := ssoSession{userID: "alice", sessionID: "s1", tokens: server.CallerTokens{IDToken: "id-token"}}

	assert.Len(t, a.planSessionBootstrap(sso).sso, 1, "setting off: the SSO server is connected")

	useVisibility(t, api.ServersOfNamespacesVisible("muster", nil))
	assert.True(t, a.planSessionBootstrap(sso).empty(), "a person who may not list muster's servers gets no SSO connect")
}

// A person's token never reaches an MCP server they may not see: the
// on-demand connect of a tool call and the SSO connect refuse before any
// connection exists, so no token is forwarded or exchanged.
func TestVisibility_NoConnectToAServerTheCallerMayNotSee(t *testing.T) {
	a := newSuspensionTestServer(t)
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "kubernetes", ToolPrefix: "kubernetes"},
		URL:                "http://127.0.0.1:1/mcp",
		AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		AuthConfig:         &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, ForwardToken: true},
	}))
	a.ssoTracker = newSSOTracker()
	useVisibility(t, api.ServersOfNamespacesVisible("muster", []string{"org-a"}))
	ctx := server.ContextWithIDToken(testSessionCtx(), "id-token")

	client, _, err := a.getOrCreateClientForToolCall(ctx, "kubernetes", "test-session", "test-user")
	assert.Nil(t, client)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	info := getServerInfo(t, a.registry, "kubernetes")
	assert.Equal(t, ssoConnectFailed, a.establishSSOConnection(ctx, info, "https://dex.example.com"))
	authenticated, _ := a.authStore.IsAuthenticated(ctx, "test-session", "kubernetes")
	assert.False(t, authenticated)
	_, pooled := a.connPool.Get("test-session", "kubernetes")
	assert.False(t, pooled, "no connection to the hidden server")
	assert.False(t, a.ssoTracker.HasSSOFailed("test-user", "kubernetes"), "no SSO attempt was made, so none failed")
}
