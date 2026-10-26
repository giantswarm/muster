package aggregator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	configPkg "github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/server"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

const (
	githubAppIssuer = "https://github.com/apps/giantswarm-workspaces"
	gitlabAppIssuer = "https://gitlab.example.com"
	appSubject      = "alice"
	appSession      = "session-a"
)

// appGrantHandler is an OAuth handler holding people's grants by issuer: it
// records pins, sign-in challenges and the grants it was asked to clear.
type appGrantHandler struct {
	*mockOAuthHandler
	pins       map[string]api.IssuerPin
	grants     map[string]*api.OAuthToken // user|issuer
	challenges []api.AuthChallengeParams
	cleared    []string // issuer
}

func newAppGrantHandler() *appGrantHandler {
	return &appGrantHandler{
		mockOAuthHandler: newMockOAuthHandler(true),
		pins:             map[string]api.IssuerPin{},
		grants:           map[string]*api.OAuthToken{},
	}
}

func (h *appGrantHandler) PinIssuer(issuer string, pin api.IssuerPin) { h.pins[issuer] = pin }
func (h *appGrantHandler) UnpinIssuer(issuer string)                  { delete(h.pins, issuer) }

func (h *appGrantHandler) GetFullTokenByIssuerForUser(_, userID, issuer string) *api.OAuthToken {
	return h.grants[userID+"|"+issuer]
}

func (h *appGrantHandler) ClearTokenByIssuerForUser(_, userID, issuer string) {
	h.cleared = append(h.cleared, issuer)
	delete(h.grants, userID+"|"+issuer)
}

func (h *appGrantHandler) IssuerSubjectScoped(issuer string) bool {
	return h.pins[issuer].SubjectScoped
}

func (h *appGrantHandler) CreateAuthChallenge(_ context.Context, params api.AuthChallengeParams) (*api.AuthChallenge, error) {
	h.challenges = append(h.challenges, params)
	return &api.AuthChallenge{Status: "auth_required", AuthURL: "https://muster.example.com/oauth/proxy/start?for=" + params.ServerName}, nil
}

func (h *appGrantHandler) grant(issuer, access string) {
	h.grants[appSubject+"|"+issuer] = &api.OAuthToken{AccessToken: access, Issuer: issuer}
}

var (
	_ api.IssuerPinner        = (*appGrantHandler)(nil)
	_ api.SubjectGrantHandler = (*appGrantHandler)(nil)
)

func testProviderApps() []configPkg.ProviderAppConfig {
	return []configPkg.ProviderAppConfig{
		{
			Name:                       "github",
			Issuer:                     githubAppIssuer + "/",
			AuthorizationEndpoint:      "https://github.com/login/oauth/authorize",
			TokenEndpoint:              "https://github.com/login/oauth/access_token",
			ExpectedIssuer:             "https://github.com/login/oauth",
			ClientCredentialsSecretRef: &configPkg.BrokerSecretRefConfig{Name: "github-app"},
		},
		{
			Name:                       "gitlab",
			Issuer:                     gitlabAppIssuer,
			RevocationEndpoint:         gitlabAppIssuer + "/oauth/revoke",
			ClientCredentialsSecretRef: &configPkg.BrokerSecretRefConfig{Name: "gitlab-app"},
			Scopes:                     "api read_user",
			GitUser:                    "oauth2",
		},
	}
}

// appFixture is an aggregator with the GitHub and GitLab Apps and the
// workspace-manager registered once, receiving both Apps' grants.
type appFixture struct {
	agg     *AggregatorServer
	handler *appGrantHandler
	secrets *mockSecretCredentialsHandler
	wm      *ServerInfo
}

func newAppFixture(t *testing.T) *appFixture {
	t.Helper()
	handler := newAppGrantHandler()
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
	secrets := &mockSecretCredentialsHandler{credentials: &api.ClientCredentials{ClientID: "app-client", ClientSecret: "app-secret"}}
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(nil) })

	set, err := newProviderAppSet(testProviderApps(), "muster")
	require.NoError(t, err)
	setAppOwnedIssuers(set)
	t.Cleanup(func() { setAppOwnedIssuers(nil) })

	agg := &AggregatorServer{registry: NewServerRegistry("x"), providerApps: set}
	wm := &ServerInfo{
		Name: "workspace-manager",
		URL:  "http://workspace-manager.agent-platform.svc/mcp",
		AuthConfig: &api.MCPServerAuth{
			ForwardToken: true,
			ProviderApps: []string{"github", "gitlab"},
		},
	}
	agg.registry.mu.Lock()
	agg.registry.servers[wm.Name] = wm
	agg.registry.mu.Unlock()
	set.pinAll(context.Background())
	return &appFixture{agg: agg, handler: handler, secrets: secrets, wm: wm}
}

func TestProviderApps_PinnedSubjectScopedWithoutAnyMCPServer(t *testing.T) {
	f := newAppFixture(t)

	gh, ok := f.handler.pins[githubAppIssuer]
	require.True(t, ok, "the GitHub App is pinned under its trimmed issuer: %v", f.handler.pins)
	assert.True(t, gh.SubjectScoped)
	assert.Equal(t, "https://github.com/login/oauth/authorize", gh.AuthorizationEndpoint)
	assert.Equal(t, "https://github.com/login/oauth", gh.ExpectedIssuer)
	assert.Equal(t, "app-client", gh.ClientID)

	gl, ok := f.handler.pins[gitlabAppIssuer]
	require.True(t, ok)
	assert.True(t, gl.SubjectScoped)
	assert.Empty(t, gl.AuthorizationEndpoint, "GitLab is discovered")
	assert.Equal(t, gitlabAppIssuer+"/oauth/revoke", gl.RevocationEndpoint)
	assert.Equal(t, "muster", f.secrets.lastDefaultNS, "a Secret reference defaults to muster's namespace")
	assert.Nil(t, f.secrets.lastOwner, "an App's Secret is configuration, read without an MCPServer binding")
	assert.Positive(t, f.secrets.configCalls)
}

func TestProviderApps_PinWithoutSecretIsRetried(t *testing.T) {
	f := newAppFixture(t)
	f.handler.pins = map[string]api.IssuerPin{}
	f.secrets.err = errors.New("secret not found")
	f.agg.providerApps.pinned = map[string]bool{}

	f.agg.providerApps.pinAll(context.Background())
	pin, ok := f.handler.pins[githubAppIssuer]
	require.True(t, ok, "pinned without its client, so the grants are still released")
	assert.True(t, pin.SubjectScoped)
	assert.Empty(t, pin.ClientID)

	f.secrets.err = nil
	f.agg.providerApps.pinAll(context.Background())
	assert.Equal(t, "app-client", f.handler.pins[githubAppIssuer].ClientID, "the next pass pins the client")
}

func TestProviderApps_MCPServerPinLeavesTheAppPin(t *testing.T) {
	f := newAppFixture(t)
	legacy := githubStyleServer(nil, api.GrantScopeSubject)
	legacy.Namespace = ""
	legacy.AuthConfig.AuthorizationServer.Issuer = githubAppIssuer
	legacy.AuthConfig.AuthorizationServer.AuthorizationEndpoint = "https://other.example.com/authorize"

	require.NoError(t, bareAggregator().pinAuthorizationServer(context.Background(), legacy))
	assert.Equal(t, "https://github.com/login/oauth/authorize", f.handler.pins[githubAppIssuer].AuthorizationEndpoint)

	f.agg.releaseAuthorizationServerPin(context.Background(), legacy.Name, legacy.AuthConfig)
	_, ok := f.handler.pins[githubAppIssuer]
	assert.True(t, ok, "an MCPServer that goes never drops an App's pin")
}

func TestGrantHeaderFunc_AttachesEveryGrantedApp(t *testing.T) {
	f := newAppFixture(t)
	headers := f.agg.grantHeaderFunc(appSession, appSubject, f.wm)
	require.NotNil(t, headers)

	assert.Nil(t, headers(context.Background()), "no grant, no header")

	f.handler.grant(githubAppIssuer, "ghu_alice")
	assert.Equal(t, map[string]string{"X-Muster-Grant-github": "ghu_alice"}, headers(context.Background()))

	f.handler.grant(gitlabAppIssuer, "glpat_alice")
	assert.Equal(t, map[string]string{
		"X-Muster-Grant-github": "ghu_alice",
		"X-Muster-Grant-gitlab": "glpat_alice",
	}, headers(context.Background()), "both tokens on one call")
}

func TestGrantHeaderFunc_OnlyForServersNamingApps(t *testing.T) {
	f := newAppFixture(t)
	f.handler.grant(githubAppIssuer, "ghu_alice")

	assert.Nil(t, f.agg.grantHeaderFunc(appSession, appSubject, &ServerInfo{Name: "plain", AuthConfig: &api.MCPServerAuth{ForwardToken: true}}))
	assert.Nil(t, f.agg.grantHeaderFunc(appSession, appSubject, &ServerInfo{Name: "plain", AuthConfig: &api.MCPServerAuth{ProviderApps: []string{"github"}}}),
		"providerApps needs a connection made as the person")
	assert.Nil(t, f.agg.grantHeaderFunc(appSession, appSubject, &ServerInfo{Name: "org-a/wm", AuthConfig: &api.MCPServerAuth{ForwardToken: true, ProviderApps: []string{"github"}}}),
		"a server of another namespace never receives the person's App tokens")

	only := &ServerInfo{Name: "only-gitlab", AuthConfig: &api.MCPServerAuth{ForwardToken: true, ProviderApps: []string{"gitlab", "unknown"}}}
	assert.Nil(t, f.agg.grantHeaderFunc(appSession, appSubject, only)(context.Background()), "an App the server does not name is not sent")
}

func TestMergeHeaderFuncs(t *testing.T) {
	assert.Nil(t, mergeHeaderFuncs(nil, nil))
	a := func(context.Context) map[string]string {
		return map[string]string{"Authorization": "Bearer id", "X": "a"}
	}
	b := func(context.Context) map[string]string { return map[string]string{"X": "b", "Y": "b"} }
	empty := func(context.Context) map[string]string { return nil }
	got := mergeHeaderFuncs(a, nil, empty, b)(context.Background())
	assert.Equal(t, map[string]string{"Authorization": "Bearer id", "X": "b", "Y": "b"}, got)
}

func TestGrantProviderApp(t *testing.T) {
	f := newAppFixture(t)
	p := NewAuthToolProvider(f.agg)
	ctx := api.WithSubject(api.WithSessionID(context.Background(), appSession), appSubject)

	result, err := p.handleAuthLogin(ctx, map[string]any{"app": "gitlab"})
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	assert.Equal(t, "https://muster.example.com/oauth/proxy/start?for=app:gitlab", challengeURL(result))
	require.Len(t, f.handler.challenges, 1)
	assert.Equal(t, gitlabAppIssuer, f.handler.challenges[0].Issuer)
	assert.Equal(t, "app:gitlab", f.handler.challenges[0].ServerName)
	assert.Equal(t, "api read_user", f.handler.challenges[0].Scope)

	f.handler.grant(gitlabAppIssuer, "glpat_alice")
	result, err = p.handleAuthLogin(ctx, map[string]any{"app": "gitlab"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.True(t, loginConnected(result), "a held grant needs no sign-in: %v", result.Content)
	assert.Len(t, f.handler.challenges, 1)

	result, err = p.handleAuthLogin(ctx, map[string]any{"app": "bitbucket"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, contentText(result), "configured: github, gitlab")

	result, err = p.handleAuthLogin(ctx, map[string]any{"app": "gitlab", "server": "workspace-manager"})
	require.NoError(t, err)
	assert.True(t, result.IsError, "server and app together are refused")
}

func TestDisconnectProviderApp_DropsOnlyThatGrant(t *testing.T) {
	f := newAppFixture(t)
	p := NewAuthToolProvider(f.agg)
	ctx := api.WithSubject(api.WithSessionID(context.Background(), appSession), appSubject)
	f.handler.grant(githubAppIssuer, "ghu_alice")
	f.handler.grant(gitlabAppIssuer, "glpat_alice")

	result, err := p.handleAuthLogout(ctx, map[string]any{"app": "gitlab"})
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	assert.Contains(t, contentText(result), "revoked")
	assert.Equal(t, []string{gitlabAppIssuer}, f.handler.cleared)
	assert.NotNil(t, f.handler.grants[appSubject+"|"+githubAppIssuer], "the GitHub grant stays")
	assert.Nil(t, f.handler.grants[appSubject+"|"+gitlabAppIssuer])
}

func grantRequiredResult(apps ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent("connect_required: gitlab")},
		IsError:           true,
		StructuredContent: map[string]any{"status": pkgoauth.StatusGrantRequired, "apps": apps},
	}
}

func TestAnswerGrantRequired(t *testing.T) {
	ctx := api.WithSubject(api.WithSessionID(context.Background(), appSession), appSubject)

	t.Run("names the missing App's sign-in link", func(t *testing.T) {
		f := newAppFixture(t)
		f.handler.grant(githubAppIssuer, "ghu_alice")

		got := f.agg.answerGrantRequired(ctx, f.wm.Name, appSession, appSubject, grantRequiredResult("gitlab"))
		require.True(t, got.IsError)
		structured, ok := got.StructuredContent.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "auth_required", structured["status"])
		assert.Equal(t, "https://muster.example.com/oauth/proxy/start?for=app:gitlab", structured["authUrl"])
		apps, ok := structured["apps"].([]map[string]any)
		require.True(t, ok)
		require.Len(t, apps, 1)
		assert.Equal(t, "gitlab", apps[0]["app"])
		text := got.Content[0].(mcp.TextContent).Text
		assert.True(t, strings.HasPrefix(text, "auth_required:"), text)
		assert.Contains(t, text, "gitlab: https://")
	})

	t.Run("a call that needs no missing grant is returned unchanged", func(t *testing.T) {
		f := newAppFixture(t)
		ok := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("done")}}
		assert.Same(t, ok, f.agg.answerGrantRequired(ctx, f.wm.Name, appSession, appSubject, ok))

		f.handler.grant(gitlabAppIssuer, "glpat_alice")
		raced := grantRequiredResult("gitlab")
		assert.Same(t, raced, f.agg.answerGrantRequired(ctx, f.wm.Name, appSession, appSubject, raced), "granted meanwhile")
	})

	t.Run("only Apps the server names, only for servers naming Apps", func(t *testing.T) {
		f := newAppFixture(t)
		unknown := grantRequiredResult("bitbucket")
		assert.Same(t, unknown, f.agg.answerGrantRequired(ctx, f.wm.Name, appSession, appSubject, unknown))
		plain := grantRequiredResult("gitlab")
		assert.Same(t, plain, f.agg.answerGrantRequired(ctx, "other", appSession, appSubject, plain))
		assert.Empty(t, f.handler.challenges)
	})
}

func TestProviderAppStatuses(t *testing.T) {
	f := newAppFixture(t)
	f.handler.grant(githubAppIssuer, "ghu_alice")
	p := NewAuthToolProvider(f.agg)

	got := p.providerAppStatuses(context.Background(), appSession, appSubject, false)
	assert.Equal(t, []pkgoauth.ProviderAppAuthStatus{
		{Name: "github", Status: pkgoauth.SessionServerStatusConnected},
		{Name: "gitlab", Status: pkgoauth.SessionServerStatusAuthRequired, AuthTool: "core_auth_login"},
	}, got)
	assert.Empty(t, f.handler.challenges)

	got = p.providerAppStatuses(context.Background(), appSession, appSubject, true)
	assert.Equal(t, "https://muster.example.com/oauth/proxy/start?for=app:gitlab", got[1].AuthURL)
}

func TestReleasableWithProviderApps(t *testing.T) {
	apps := testProviderApps()

	releasable, gitUsers, err := releasableWithProviderApps(map[string]string{"legacy": "https://legacy.example.com"}, nil, apps)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"legacy": "https://legacy.example.com",
		"github": githubAppIssuer,
		"gitlab": gitlabAppIssuer,
	}, releasable)
	assert.Equal(t, map[string]string{"gitlab": "oauth2"}, gitUsers)

	_, _, err = releasableWithProviderApps(map[string]string{"github": githubAppIssuer + "/"}, map[string]string{"gitlab": "oauth2"}, apps)
	require.NoError(t, err, "an agreeing entry is kept")

	_, _, err = releasableWithProviderApps(map[string]string{"github": "https://github.com/apps/other"}, nil, apps)
	require.Error(t, err)
	_, _, err = releasableWithProviderApps(nil, map[string]string{"gitlab": "git"}, apps)
	require.Error(t, err)

	same, _, err := releasableWithProviderApps(map[string]string{"x": "y"}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"x": "y"}, same)
}

func TestGrantRequiredApps(t *testing.T) {
	assert.Nil(t, grantRequiredApps(nil))
	assert.Nil(t, grantRequiredApps(&mcp.CallToolResult{StructuredContent: map[string]any{"status": "ok"}}))
	assert.Equal(t, []string{"gitlab", "github"}, grantRequiredApps(grantRequiredResult("gitlab", "github", "gitlab", 3, "")))
}

// TestProviderApps_BothTokensOnOneCall drives a forwardToken session client
// to a recording backend: the forwarded ID token in Authorization and each
// granted App's token in its own header, on the handshake and on the call.
func TestProviderApps_BothTokensOnOneCall(t *testing.T) {
	f := newAppFixture(t)
	f.handler.grant(githubAppIssuer, "ghu_alice")
	f.handler.grant(gitlabAppIssuer, "glpat_alice")
	backend := newPostRecorder(t)
	f.wm.URL = backend.URL

	ctx := server.ContextWithBearerToken(context.Background(), identityTokenBefore)
	client, _, err := f.agg.newTokenForwardingClient(ctx, appSession, appSubject, identityTestIssuer, f.wm, func() {})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Initialize(ctx))
	_, err = client.CallTool(ctx, "noop", nil)
	require.NoError(t, err)

	posts := backend.take()
	require.NotEmpty(t, posts)
	for _, h := range posts {
		assert.Equal(t, "Bearer "+identityTokenBefore, h.Get("Authorization"))
		assert.Equal(t, "ghu_alice", h.Get("X-Muster-Grant-github"))
		assert.Equal(t, "glpat_alice", h.Get("X-Muster-Grant-gitlab"))
	}

	// The person disconnects GitLab: the next call carries GitHub's only.
	delete(f.handler.grants, appSubject+"|"+gitlabAppIssuer)
	_, err = client.CallTool(ctx, "noop", nil)
	require.NoError(t, err)
	for _, h := range backend.take() {
		assert.Equal(t, "ghu_alice", h.Get("X-Muster-Grant-github"))
		assert.Empty(t, h.Get("X-Muster-Grant-gitlab"))
	}
}
