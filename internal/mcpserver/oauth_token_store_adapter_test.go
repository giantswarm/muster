package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubOAuthHandler is a minimal implementation of api.OAuthHandler for testing
// the token store adapter. Only the methods used by MCPGoTokenStore are implemented
// with real logic; others are stubs.
type stubOAuthHandler struct {
	enabled          bool
	accessToken      string
	fullToken        *api.OAuthToken
	refreshCallCount int
	storedTokens     map[string]*api.OAuthToken // key: sessionID+issuer
}

func (m *stubOAuthHandler) IsEnabled() bool { return m.enabled }
func (m *stubOAuthHandler) RefreshTokenIfNeeded(_ context.Context, _, _ string) string {
	m.refreshCallCount++
	return m.accessToken
}
func (m *stubOAuthHandler) GetFullTokenByIssuer(_, _ string) *api.OAuthToken {
	return m.fullToken
}

func (m *stubOAuthHandler) GetToken(_, _ string) *api.OAuthToken          { return nil }
func (m *stubOAuthHandler) GetTokenByIssuer(_, _ string) *api.OAuthToken  { return nil }
func (m *stubOAuthHandler) FindTokenWithIDToken(_ string) *api.OAuthToken { return nil }
func (m *stubOAuthHandler) StoreToken(sessionID, issuer string, token *api.OAuthToken) {
	if m.storedTokens == nil {
		m.storedTokens = make(map[string]*api.OAuthToken)
	}
	m.storedTokens[sessionID+"|"+issuer] = token
}
func (m *stubOAuthHandler) ClearTokenByIssuer(_, _ string)                         {}
func (m *stubOAuthHandler) RegisterServer(_, _, _ string)                          {}
func (m *stubOAuthHandler) SetAuthCompletionCallback(_ api.AuthCompletionCallback) {}
func (m *stubOAuthHandler) GetHTTPHandler() http.Handler                           { return nil }
func (m *stubOAuthHandler) GetCallbackPath() string                                { return "" }
func (m *stubOAuthHandler) GetCIMDPath() string                                    { return "" }
func (m *stubOAuthHandler) ShouldServeCIMD() bool                                  { return false }
func (m *stubOAuthHandler) GetCIMDHandler() http.HandlerFunc                       { return nil }
func (m *stubOAuthHandler) Stop()                                                  {}
func (m *stubOAuthHandler) CreateAuthChallenge(_ context.Context, _, _, _, _ string) (*api.AuthChallenge, error) {
	return nil, nil
}
func (m *stubOAuthHandler) ExchangeTokenForRemoteCluster(_ context.Context, _, _ string, _ *api.TokenExchangeConfig) (string, error) {
	return "", nil
}
func (m *stubOAuthHandler) ExchangeTokenForRemoteClusterWithClient(_ context.Context, _, _ string, _ *api.TokenExchangeConfig, _ *http.Client) (string, error) {
	return "", nil
}

var _ api.OAuthHandler = (*stubOAuthHandler)(nil)

func TestMCPGoTokenStore_GetToken_ReturnsAccessToken(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled:     true,
		accessToken: "test-access-token",
	}
	store := NewMCPGoTokenStore("session-1", "https://issuer.example.com", handler)

	token, err := store.GetToken(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "test-access-token", token.AccessToken)
	assert.Equal(t, "Bearer", token.TokenType)
	assert.Equal(t, 1, handler.refreshCallCount)
}

func TestMCPGoTokenStore_GetToken_ReturnsErrNoToken_WhenEmpty(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled:     true,
		accessToken: "",
	}
	store := NewMCPGoTokenStore("session-1", "https://issuer.example.com", handler)

	token, err := store.GetToken(t.Context())

	assert.Nil(t, token)
	assert.True(t, errors.Is(err, transport.ErrNoToken))
}

func TestMCPGoTokenStore_GetToken_ReturnsErrNoToken_WhenDisabled(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled: false,
	}
	store := NewMCPGoTokenStore("session-1", "https://issuer.example.com", handler)

	token, err := store.GetToken(t.Context())

	assert.Nil(t, token)
	assert.True(t, errors.Is(err, transport.ErrNoToken))
}

func TestMCPGoTokenStore_GetToken_ReturnsErrNoToken_WhenNilHandler(t *testing.T) {
	store := NewMCPGoTokenStore("session-1", "https://issuer.example.com", nil)

	token, err := store.GetToken(t.Context())

	assert.Nil(t, token)
	assert.True(t, errors.Is(err, transport.ErrNoToken))
}

func TestMCPGoTokenStore_GetToken_CachesIDToken(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled:     true,
		accessToken: "access-token",
		fullToken: &api.OAuthToken{
			IDToken: "my-id-token",
		},
	}
	store := NewMCPGoTokenStore("session-1", "https://issuer.example.com", handler)

	_, err := store.GetToken(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "my-id-token", store.GetIDToken())
}

func TestMCPGoTokenStore_GetToken_IDTokenEmpty_WhenNoFullToken(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled:     true,
		accessToken: "access-token",
		fullToken:   nil,
	}
	store := NewMCPGoTokenStore("session-1", "https://issuer.example.com", handler)

	_, err := store.GetToken(t.Context())
	require.NoError(t, err)

	assert.Empty(t, store.GetIDToken())
}

func TestMCPGoTokenStore_GetToken_RespectsContextCancellation(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled:     true,
		accessToken: "token",
	}
	store := NewMCPGoTokenStore("session-1", "https://issuer.example.com", handler)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	token, err := store.GetToken(ctx)

	assert.Nil(t, token)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestMCPGoTokenStore_SaveToken_IsNoOp(t *testing.T) {
	store := NewMCPGoTokenStore("session-1", "https://issuer.example.com", nil)

	err := store.SaveToken(t.Context(), &transport.Token{AccessToken: "ignored"})

	assert.NoError(t, err)
}

func TestMCPGoTokenStore_SaveToken_IgnoresToken(t *testing.T) {
	store := NewMCPGoTokenStore("session-1", "https://issuer.example.com", nil)

	err := store.SaveToken(t.Context(), &transport.Token{
		AccessToken:  "new-token",
		RefreshToken: "new-refresh",
	})

	assert.NoError(t, err)
}
