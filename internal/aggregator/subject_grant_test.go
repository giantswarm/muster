package aggregator

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

const (
	subjectGrantIssuerURL = "https://github.example/login/oauth"
	subjectGrantToken     = "ghu_alice"
)

// subjectGrantMockHandler is an OAuth handler whose store files grants under
// the person as well as the session, as the real one does for a
// subject-scoped issuer (api.SubjectGrantHandler).
type subjectGrantMockHandler struct {
	*mockOAuthHandler
	mu      sync.Mutex
	grants  map[string]*api.OAuthToken // userID+"|"+issuer
	lookups int
	cleared []string
}

var _ api.SubjectGrantHandler = (*subjectGrantMockHandler)(nil)

func newSubjectGrantMockHandler() *subjectGrantMockHandler {
	return &subjectGrantMockHandler{mockOAuthHandler: newMockOAuthHandler(true), grants: map[string]*api.OAuthToken{}}
}

func (m *subjectGrantMockHandler) grant(userID, issuer, accessToken string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.grants[userID+"|"+issuer] = &api.OAuthToken{AccessToken: accessToken, Issuer: issuer}
}

func (m *subjectGrantMockHandler) GetFullTokenByIssuerForUser(sessionID, userID, issuer string) *api.OAuthToken {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookups++
	if token := m.GetFullTokenByIssuer(sessionID, issuer); token != nil {
		return token
	}
	return m.grants[userID+"|"+issuer]
}

func (m *subjectGrantMockHandler) ClearTokenByIssuerForUser(_, userID, issuer string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.grants, userID+"|"+issuer)
	m.cleared = append(m.cleared, userID+"|"+issuer)
}

// IssuerSubjectScoped reports the pin the real handler would hold for the
// GitHub-style issuer of these tests.
func (m *subjectGrantMockHandler) IssuerSubjectScoped(issuer string) bool {
	return strings.TrimSuffix(issuer, "/") == subjectGrantIssuerURL
}

func (m *subjectGrantMockHandler) lookupCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lookups
}

func (m *subjectGrantMockHandler) clearedGrants() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.cleared...)
}

// newGrantBackend is an MCP backend standing in for the hosted GitHub MCP
// server: it admits only the given bearer and counts initialize requests, so
// a test can tell how many connections were opened.
func newGrantBackend(t *testing.T, accept string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	mcpSrv := mcpgoserver.NewMCPServer("github", "1.0.0", mcpgoserver.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.NewTool("get_me", mcp.WithDescription("The authenticated user")),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(`{"login":"alice"}`), nil
		})
	streamable := mcpgoserver.NewStreamableHTTPServer(mcpSrv)

	var initializes atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+accept {
			w.Header().Set("WWW-Authenticate", `Bearer realm="github"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"initialize"`)) {
			initializes.Add(1)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		streamable.ServeHTTP(w, r)
	}))
	// A pooled client keeps a streaming GET open; Close alone would wait for
	// it forever.
	t.Cleanup(func() {
		backend.CloseClientConnections()
		backend.Close()
	})
	return backend, &initializes
}

// registerSubjectGrantServer registers the backend as an auth-required
// MCPServer whose authorization server is pinned with the given grant scope.
func registerSubjectGrantServer(t *testing.T, a *AggregatorServer, url, grantScope string) *ServerInfo {
	t.Helper()
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "github", ToolPrefix: "github"},
		URL:                url,
		AuthInfo:           &AuthInfo{Issuer: subjectGrantIssuerURL, Scope: "repo"},
		AuthConfig: &api.MCPServerAuth{
			Type: "oauth",
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
				Issuer:     subjectGrantIssuerURL,
				GrantScope: grantScope,
			},
		},
	}))
	info, ok := a.registry.GetServerInfo("github")
	require.True(t, ok)
	return info
}

func sessionContext(sessionID, sub string) context.Context {
	return api.WithSessionID(api.WithSubject(context.Background(), sub), sessionID)
}

func TestHoldsSubjectGrants(t *testing.T) {
	subjectAS := &api.MCPServerAuthAuthorizationServer{Issuer: subjectGrantIssuerURL, GrantScope: api.GrantScopeSubject}
	cases := map[string]struct {
		info *ServerInfo
		want bool
	}{
		"nil":                      {nil, false},
		"no auth config":           {&ServerInfo{Name: "plain"}, false},
		"session-scoped grants":    {&ServerInfo{AuthConfig: &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: subjectGrantIssuerURL}}}, false},
		"no authorization server":  {&ServerInfo{AuthConfig: &api.MCPServerAuth{Type: "oauth"}}, false},
		"token forwarding (SSO)":   {&ServerInfo{AuthConfig: &api.MCPServerAuth{ForwardToken: true, AuthorizationServer: subjectAS}}, false},
		"token exchange (SSO)":     {&ServerInfo{AuthConfig: &api.MCPServerAuth{TokenExchange: &api.TokenExchangeConfig{Enabled: true, DexTokenEndpoint: "https://dex/token", ConnectorID: "x"}, AuthorizationServer: subjectAS}}, false},
		"subject-scoped connector": {&ServerInfo{AuthConfig: &api.MCPServerAuth{Type: "oauth", AuthorizationServer: subjectAS}}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, holdsSubjectGrants(tc.info))
		})
	}
}

func TestSessionOAuthIssuer(t *testing.T) {
	as := &api.MCPServerAuthAuthorizationServer{Issuer: subjectGrantIssuerURL + "/", Scopes: "repo read:org", GrantScope: api.GrantScopeSubject}

	issuer, scope := sessionOAuthIssuer(&ServerInfo{AuthConfig: &api.MCPServerAuth{AuthorizationServer: as}})
	assert.Equal(t, subjectGrantIssuerURL, issuer, "the pinned issuer is used, without its trailing slash")
	assert.Equal(t, "repo read:org", scope)

	issuer, scope = sessionOAuthIssuer(&ServerInfo{
		AuthInfo:   &AuthInfo{Issuer: "https://discovered.example", Scope: "mcp:read"},
		AuthConfig: &api.MCPServerAuth{AuthorizationServer: as},
	})
	assert.Equal(t, "https://discovered.example", issuer, "what the 401 discovered wins")
	assert.Equal(t, "mcp:read", scope)

	issuer, _ = sessionOAuthIssuer(&ServerInfo{AuthConfig: &api.MCPServerAuth{Type: "oauth"}})
	assert.Empty(t, issuer, "nothing discovered and nothing pinned")
}

// After a restart the registry entry of a server whose authorization server
// publishes no discovery document carries no discovered issuer, only the
// operator's pin. A session already authenticated to it (mark and token
// persisted) must still get a connection from the pin instead of "unable to
// determine auth method".
func TestGetOrCreateClientForToolCall_AuthenticatedSessionUsesThePinnedIssuer(t *testing.T) {
	backend, initializes := newGrantBackend(t, subjectGrantToken)
	a := newTestAggregatorWithPool(t)
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "github", ToolPrefix: "github"},
		URL:                backend.URL,
		AuthInfo:           &AuthInfo{}, // the 401 discovered nothing
		AuthConfig: &api.MCPServerAuth{
			Type: "oauth",
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
				Issuer: subjectGrantIssuerURL + "/",
				Scopes: "repo",
			},
		},
	}))

	handler := newSubjectGrantMockHandler()
	handler.StoreToken("sess-1", "alice", subjectGrantIssuerURL, &api.OAuthToken{AccessToken: subjectGrantToken, Issuer: subjectGrantIssuerURL})
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := sessionContext("sess-1", "alice")
	require.NoError(t, a.authStore.MarkAuthenticated(ctx, "sess-1", "github"))

	client, cleanup, err := a.getOrCreateClientForToolCall(ctx, "github", "sess-1", "alice")
	require.NoError(t, err, "the pinned issuer stands in for the undiscovered one")
	defer cleanup()
	result, err := client.CallTool(ctx, "get_me", nil)
	require.NoError(t, err)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "alice")
	assert.Equal(t, int32(1), initializes.Load())
	assert.Equal(t, 1, a.connPool.Len())
}

// A session that never called core_auth_login for the server is connected
// with the grant another session of the same person completed, on the tool
// call itself.
func TestGetOrCreateClientForToolCall_AdoptsThePersonsSubjectGrant(t *testing.T) {
	backend, initializes := newGrantBackend(t, subjectGrantToken)
	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, api.GrantScopeSubject)

	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := sessionContext("sess-2", "alice")
	client, cleanup, err := a.getOrCreateClientForToolCall(ctx, "github", "sess-2", "alice")
	require.NoError(t, err, "the person's grant must serve the new session")
	defer cleanup()
	require.NotNil(t, client)

	authenticated, _ := a.authStore.IsAuthenticated(ctx, "sess-2", "github")
	assert.True(t, authenticated, "the session is marked authenticated to the server")
	caps, _ := a.capabilityStore.Get(ctx, "sess-2", "github")
	require.NotNil(t, caps, "the server's capabilities are cached for the session")
	require.Len(t, caps.Tools, 1)
	assert.Equal(t, "get_me", caps.Tools[0].Name)
	assert.Equal(t, 1, a.connPool.Len(), "the connection is pooled for the session")
	assert.Equal(t, int32(1), initializes.Load())

	result, err := a.callToolWithTokenExchangeRetry(ctx, "github", "get_me", nil, "sess-2", "alice")
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "alice")
	assert.Equal(t, int32(1), initializes.Load(), "the pooled connection is reused")

	assert.Empty(t, a.ListServersRequiringAuth(ctx), "the server is no longer reported as requiring authentication")
}

// Without a grant for the person the call fails as before, and nothing is
// connected or marked.
func TestGetOrCreateClientForToolCall_NoSubjectGrantKeepsTheFailure(t *testing.T) {
	backend, initializes := newGrantBackend(t, subjectGrantToken)
	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, api.GrantScopeSubject)

	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := sessionContext("sess-bob", "bob")
	_, _, err := a.getOrCreateClientForToolCall(ctx, "github", "sess-bob", "bob")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user not authenticated to server github")

	authenticated, _ := a.authStore.IsAuthenticated(ctx, "sess-bob", "github")
	assert.False(t, authenticated)
	assert.Equal(t, 0, a.connPool.Len())
	assert.Equal(t, int32(0), initializes.Load())
	assert.Equal(t, 1, handler.lookupCount(), "the grant was looked up once")
}

// A server whose grants are session-scoped (the default) never looks past the
// session, even when the handler would answer.
func TestGetOrCreateClientForToolCall_SessionScopedGrantsAreNotAdopted(t *testing.T) {
	backend, initializes := newGrantBackend(t, subjectGrantToken)
	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, "")

	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	_, _, err := a.getOrCreateClientForToolCall(sessionContext("sess-2", "alice"), "github", "sess-2", "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user not authenticated to server github")
	assert.Equal(t, 0, handler.lookupCount(), "no lookup for a session-scoped issuer")
	assert.Equal(t, int32(0), initializes.Load())
}

// A grant the backend rejects is cleared for the person, as core_auth_login
// does, so the next login issues a fresh sign-in link; the call fails as not
// authenticated.
func TestGetOrCreateClientForToolCall_RejectedSubjectGrantIsCleared(t *testing.T) {
	backend, _ := newGrantBackend(t, "a-token-github-still-accepts")
	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, api.GrantScopeSubject)

	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, "ghu_revoked")
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := sessionContext("sess-2", "alice")
	_, _, err := a.getOrCreateClientForToolCall(ctx, "github", "sess-2", "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user not authenticated to server github")

	assert.Equal(t, []string{"alice|" + subjectGrantIssuerURL}, handler.clearedGrants(), "the dead grant is removed for the person")
	authenticated, _ := a.authStore.IsAuthenticated(ctx, "sess-2", "github")
	assert.False(t, authenticated)
	assert.Equal(t, 0, a.connPool.Len())
}

// Concurrent first calls from one session share a single connect.
func TestAdoptSubjectGrant_ConcurrentCallsShareOneConnect(t *testing.T) {
	backend, initializes := newGrantBackend(t, subjectGrantToken)
	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, api.GrantScopeSubject)

	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := sessionContext("sess-2", "alice")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, cleanup, err := a.getOrCreateClientForToolCall(ctx, "github", "sess-2", "alice")
			if err == nil {
				cleanup()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), initializes.Load(), "one connect serves every concurrent caller")
	assert.Equal(t, 1, a.connPool.Len())
}

// A tool the registry does not know yet (no session of this process has
// connected the server) is resolved through the connection the person's
// grant makes on the call.
func TestCallToolInternal_UnknownToolIsResolvedThroughTheSubjectGrant(t *testing.T) {
	backend, initializes := newGrantBackend(t, subjectGrantToken)
	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, api.GrantScopeSubject)

	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	// Spelled out rather than built with ExposedToolName, which records the
	// mapping the precondition below asserts is absent.
	const exposed = "x_github_get_me"
	_, _, err := a.registry.ResolveToolName(exposed)
	require.Error(t, err, "precondition: the registry does not know the tool yet")

	result, err := a.CallToolInternal(sessionContext("sess-2", "alice"), exposed, map[string]any{})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "alice")
	assert.Equal(t, int32(1), initializes.Load())

	// Another person without a grant keeps the failure, and the lookup for
	// an unknown tool is not repeated for a core tool name.
	lookupsBefore := handler.lookupCount()
	_, err = a.CallToolInternal(sessionContext("sess-bob", "bob"), exposed, map[string]any{})
	require.Error(t, err)
	assert.Equal(t, lookupsBefore+1, handler.lookupCount())
	assert.Equal(t, 0, a.adoptSubjectGrants(sessionContext("sess-bob", "bob"), "sess-bob", "bob"))
}

// list_tools from a session that never connected the server shows its tools
// when the person holds a grant, and not otherwise.
func TestListToolsForContext_ShowsToolsOfTheSubjectGrant(t *testing.T) {
	backend, _ := newGrantBackend(t, subjectGrantToken)
	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, api.GrantScopeSubject)

	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	exposed := a.registry.ExposedToolName("github", "get_me")
	names := func(tools []mcp.Tool) string {
		var b strings.Builder
		for _, tool := range tools {
			b.WriteString(tool.Name)
			b.WriteString(" ")
		}
		return b.String()
	}

	aliceCtx := sessionContext("sess-3", "alice")
	assert.Contains(t, names(a.ListToolsForContext(aliceCtx)), exposed)
	assert.Empty(t, a.ListServersRequiringAuth(aliceCtx))

	bobCtx := sessionContext("sess-bob", "bob")
	assert.NotContains(t, names(a.ListToolsForContext(bobCtx)), exposed)
	require.Len(t, a.ListServersRequiringAuth(bobCtx), 1)
	assert.Equal(t, "github", a.ListServersRequiringAuth(bobCtx)[0].Name)
}
