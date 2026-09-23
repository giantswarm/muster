package aggregator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/internal/server"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

const (
	identityTestIssuer  = "https://dex.example.com"
	identityTestSession = "family-1"
	// JWT-shaped ID tokens of the same person, before and after a refresh
	// (payloads {"sub":"alice","exp":9999999999} and {"sub":"alice","exp":9999999998}).
	identityTokenBefore = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSIsImV4cCI6OTk5OTk5OTk5OX0.sig"
	identityTokenAfter  = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSIsImV4cCI6OTk5OTk5OTk5OH0.sig"
	pinnedGrant         = "pinned-grant-opaque"
)

// lockedOAuthHandler guards the mock's token map: the header func reads it from
// mcp-go's goroutines while a test stores a refreshed token.
type lockedOAuthHandler struct {
	*mockOAuthHandler
	mu sync.Mutex
}

func (h *lockedOAuthHandler) StoreToken(sessionID, userID, issuer string, token *api.OAuthToken) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mockOAuthHandler.StoreToken(sessionID, userID, issuer, token)
}

func (h *lockedOAuthHandler) GetFullTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.mockOAuthHandler.GetFullTokenByIssuer(sessionID, issuer)
}

// refreshingOAuthServer is an OAuth server whose in-process provider refresh
// files a new ID token for the session, as TokenRefreshHandler does.
type refreshingOAuthServer struct {
	fakeOAuthServer
	onRefresh func(familyID string)
}

func (f *refreshingOAuthServer) RefreshSessionProvider(_ context.Context, familyID string) error {
	f.onRefresh(familyID)
	return nil
}

// newIdentityTestAggregator registers a fresh OAuth handler holding no token
// and returns an aggregator whose login issuer is identityTestIssuer.
func newIdentityTestAggregator(t *testing.T) (*AggregatorServer, *lockedOAuthHandler) {
	t.Helper()
	handler := &lockedOAuthHandler{mockOAuthHandler: newMockOAuthHandler(true)}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
	return &AggregatorServer{
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config: config.OAuthServerConfig{
					Provider: "dex",
					Dex:      config.DexConfig{IssuerURL: identityTestIssuer},
				},
			},
		},
	}, handler
}

func pinnedServerInfo(forwardIdentity bool) *ServerInfo {
	return &ServerInfo{
		Name: "two-hats",
		AuthConfig: &api.MCPServerAuth{
			Type:                api.MCPServerAuthTypeOAuth,
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth"},
			ForwardIdentity:     forwardIdentity,
		},
	}
}

func TestIdentityHeaderFunc_OnlyForAForwardIdentityServer(t *testing.T) {
	a, _ := newIdentityTestAggregator(t)

	assert.NotNil(t, a.identityHeaderFunc(identityTestSession, pinnedServerInfo(true)))

	for name, info := range map[string]*ServerInfo{
		"no registry entry":       nil,
		"no auth":                 {Name: "plain"},
		"pinned, field unset":     pinnedServerInfo(false),
		"forwardToken":            {Name: "sso", AuthConfig: &api.MCPServerAuth{ForwardToken: true}},
		"field without a pin":     {Name: "unpinned", AuthConfig: &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, ForwardIdentity: true}},
		"field without type auth": {Name: "untyped", AuthConfig: &api.MCPServerAuth{AuthorizationServer: pinnedServerInfo(true).AuthConfig.AuthorizationServer, ForwardIdentity: true}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Nil(t, a.identityHeaderFunc(identityTestSession, info), "the server never receives the header")
		})
	}
}

func TestIdentityHeaderFunc_ResolvesTheTokenOnEveryRequest(t *testing.T) {
	t.Run("the session's ID token from the store, the refreshed one after a refresh", func(t *testing.T) {
		a, handler := newIdentityTestAggregator(t)
		headerFunc := a.identityHeaderFunc(identityTestSession, pinnedServerInfo(true))

		handler.StoreToken(identityTestSession, "", identityTestIssuer, &api.OAuthToken{IDToken: identityTokenBefore})
		assert.Equal(t, map[string]string{pkgoauth.HeaderMusterIDToken: identityTokenBefore}, headerFunc(context.Background()))

		handler.StoreToken(identityTestSession, "", identityTestIssuer, &api.OAuthToken{IDToken: identityTokenAfter})
		assert.Equal(t, map[string]string{pkgoauth.HeaderMusterIDToken: identityTokenAfter}, headerFunc(context.Background()))
	})

	t.Run("an in-process refresh when the store holds no valid token", func(t *testing.T) {
		a, handler := newIdentityTestAggregator(t)
		var refreshed []string
		a.oauthHTTPServer = &refreshingOAuthServer{onRefresh: func(familyID string) {
			refreshed = append(refreshed, familyID)
			handler.StoreToken(familyID, "", identityTestIssuer, &api.OAuthToken{IDToken: identityTokenAfter})
		}}
		headerFunc := a.identityHeaderFunc(identityTestSession, pinnedServerInfo(true))

		assert.Equal(t, map[string]string{pkgoauth.HeaderMusterIDToken: identityTokenAfter}, headerFunc(context.Background()))
		assert.Equal(t, []string{identityTestSession}, refreshed)
	})

	t.Run("the inbound JWT bearer, as forwardToken sends it", func(t *testing.T) {
		a, handler := newIdentityTestAggregator(t)
		handler.StoreToken(identityTestSession, "", identityTestIssuer, &api.OAuthToken{IDToken: identityTokenBefore})
		headerFunc := a.identityHeaderFunc(identityTestSession, pinnedServerInfo(true))

		ctx := server.ContextWithBearerToken(context.Background(), identityTokenAfter)
		assert.Equal(t, map[string]string{pkgoauth.HeaderMusterIDToken: identityTokenAfter}, headerFunc(ctx))
	})

	t.Run("no header for a session without an upstream ID token", func(t *testing.T) {
		a, _ := newIdentityTestAggregator(t)
		headerFunc := a.identityHeaderFunc(identityTestSession, pinnedServerInfo(true))

		assert.Empty(t, headerFunc(context.Background()))
		opaque := server.ContextWithBearerToken(context.Background(), "opaque-muster-token")
		assert.Empty(t, headerFunc(opaque), "an opaque bearer is never forwarded")
	})
}

// postRecorder is a streamable-http MCP backend that keeps the headers of
// every POST it answers.
type postRecorder struct {
	*httptest.Server
	mu    sync.Mutex
	posts []http.Header
}

func newPostRecorder(t *testing.T) *postRecorder {
	t.Helper()
	backend := mcpgoserver.NewMCPServer("recorder", "0.0.1")
	backend.AddTool(mcp.NewTool("noop"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	streamable := mcpgoserver.NewStreamableHTTPServer(backend, mcpgoserver.WithStateful(true))
	rec := &postRecorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			rec.mu.Lock()
			rec.posts = append(rec.posts, r.Header.Clone())
			rec.mu.Unlock()
		}
		streamable.ServeHTTP(w, r)
	}))
	t.Cleanup(rec.Close)
	return rec
}

// take returns the POSTs recorded since the last call.
func (r *postRecorder) take() []http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	posts := r.posts
	r.posts = nil
	return posts
}

// pinnedGrantClient is the session client a pinned server gets: the grant in
// Authorization through the OAuth handler, the identity header func next to it.
func pinnedGrantClient(t *testing.T, ctx context.Context, url string, headerFunc transport.HTTPHeaderFunc) *internalmcp.DynamicAuthClient {
	t.Helper()
	store := transport.NewMemoryTokenStore()
	require.NoError(t, store.SaveToken(ctx, &transport.Token{
		AccessToken: pinnedGrant,
		TokenType:   pkgoauth.SchemeBearer,
		ExpiresAt:   time.Now().Add(time.Hour),
	}))
	client := internalmcp.NewDynamicAuthClient(url, store, "repo", "client", "").WithHeaderFunc(headerFunc)
	require.NoError(t, client.Initialize(ctx))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestPinnedServerCallsCarryTheGrantAndTheIDToken drives the session client of
// a forwardIdentity server against a real streamable-http backend: every
// request carries the pinned grant in Authorization and the session's ID token
// in X-Muster-Id-Token, the refreshed one after a refresh; a pinned server
// without the field receives the grant alone.
func TestPinnedServerCallsCarryTheGrantAndTheIDToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, handler := newIdentityTestAggregator(t)
	handler.StoreToken(identityTestSession, "", identityTestIssuer, &api.OAuthToken{IDToken: identityTokenBefore})

	backend := newPostRecorder(t)
	client := pinnedGrantClient(t, ctx, backend.URL+"/mcp", a.identityHeaderFunc(identityTestSession, pinnedServerInfo(true)))
	_, err := client.CallTool(ctx, "noop", nil)
	require.NoError(t, err)

	assertPosts := func(posts []http.Header, idToken string) {
		t.Helper()
		require.NotEmpty(t, posts)
		for i, h := range posts {
			assert.Equal(t, pkgoauth.SchemeBearer+" "+pinnedGrant, h.Get(pkgoauth.HeaderAuthorization), "POST %d keeps the pinned grant", i)
			assert.Equal(t, idToken, h.Get(pkgoauth.HeaderMusterIDToken), "POST %d carries the session's ID token", i)
		}
	}
	assertPosts(backend.take(), identityTokenBefore)

	// The session's ID token is refreshed; the pooled connection sends the new
	// one on its next call.
	handler.StoreToken(identityTestSession, "", identityTestIssuer, &api.OAuthToken{IDToken: identityTokenAfter})
	_, err = client.CallTool(ctx, "noop", nil)
	require.NoError(t, err)
	assertPosts(backend.take(), identityTokenAfter)

	t.Run("a pinned server without the field", func(t *testing.T) {
		other := newPostRecorder(t)
		plain := pinnedGrantClient(t, ctx, other.URL+"/mcp", a.identityHeaderFunc(identityTestSession, pinnedServerInfo(false)))
		_, err := plain.CallTool(ctx, "noop", nil)
		require.NoError(t, err)

		posts := other.take()
		require.NotEmpty(t, posts)
		for i, h := range posts {
			assert.Equal(t, pkgoauth.SchemeBearer+" "+pinnedGrant, h.Get(pkgoauth.HeaderAuthorization), "POST %d", i)
			assert.Empty(t, h.Values(pkgoauth.HeaderMusterIDToken), "POST %d never carries the header", i)
		}
	})
}
