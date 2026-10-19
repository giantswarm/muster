package aggregator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// tokenHoldingOAuthHandler holds one stored grant for the issuer, as the
// person's token store does after a sign-in, and drops it when cleared.
type tokenHoldingOAuthHandler struct {
	challengeCountingOAuthHandler
	mu      sync.Mutex
	token   *api.OAuthToken
	cleared int
}

func (m *tokenHoldingOAuthHandler) GetFullTokenByIssuer(_, _ string) *api.OAuthToken {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.token
}

func (m *tokenHoldingOAuthHandler) ClearTokenByIssuer(_, _ string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = nil
	m.cleared++
}

func (m *tokenHoldingOAuthHandler) stored() *api.OAuthToken {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.token
}

// The issue: the session is marked authenticated and holds a stored grant,
// but has no pooled connection, so the call opens an on-demand client -- and
// the server refuses the stored token with 401 on the handshake. The answer is
// the server's auth_required challenge with its sign-in link, not the bare
// "failed to initialize on-demand client: ... 401"; the refused grant is
// dropped and the session's mark revoked, so auth status stops showing the
// server connected and a login starts the server's sign-in again.
func TestCallTool_OnDemandConnect401AnswersAuthRequiredChallenge(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(backend.Close)

	a := newTestAggregatorWithPool(t)
	pinned := &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: answerIssuer, Scopes: "repo"}}
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: answerServer, ToolPrefix: answerServer},
		URL:                backend.URL + "/mcp",
		AuthInfo:           &AuthInfo{Issuer: answerIssuer, Scope: "repo"},
		AuthConfig:         pinned,
	}))
	handler := &tokenHoldingOAuthHandler{
		challengeCountingOAuthHandler: challengeCountingOAuthHandler{issuerMockOAuthHandler: issuerMockOAuthHandler{enabled: true}},
		token:                         &api.OAuthToken{AccessToken: "refused", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)},
	}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := api.WithSubject(api.WithSessionID(context.Background(), answerSession), answerSubject)
	require.NoError(t, a.authStore.MarkAuthenticated(ctx, answerSession, answerServer))

	result, err := a.callToolWithTokenExchangeRetry(ctx, answerServer, "op", map[string]any{}, answerSession, answerSubject)
	require.NoError(t, err, "a refused credential is an answer, not a transport error")
	require.NotNil(t, result)

	assert.True(t, result.IsError, "the tool did not run")
	text := mcpText(result)
	assert.Contains(t, text, "auth_required: server 'svc'")
	assert.Contains(t, text, "rejected this session's credentials")
	assert.Contains(t, text, "https://muster.example.com/oauth/proxy/start", "the server's sign-in link")
	assert.NotContains(t, text, "401")
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://muster.example.com/oauth/proxy/start?state=x", structured["authUrl"])
	assert.Equal(t, 1, handler.challenges)

	assert.Nil(t, handler.stored(), "the refused grant is dropped")
	authenticated, _ := a.authStore.IsAuthenticated(context.Background(), answerSession, answerServer)
	assert.False(t, authenticated, "the session is no longer authenticated to the server")
	_, pooled := a.connPool.Get(answerSession, answerServer)
	assert.False(t, pooled)
}
