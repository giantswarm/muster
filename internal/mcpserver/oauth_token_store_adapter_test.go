package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubOAuthHandler is a minimal implementation of api.OAuthHandler for testing
// the token store adapter. Only the methods used by MusterTokenStore are implemented
// with real logic; others are stubs.
type stubOAuthHandler struct {
	enabled      bool
	fullToken    *api.OAuthToken
	storedTokens map[string]*api.OAuthToken // key: sessionID+"|"+issuer
}

func (m *stubOAuthHandler) IsEnabled() bool { return m.enabled }
func (m *stubOAuthHandler) GetFullTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	if m.storedTokens != nil {
		if tok, ok := m.storedTokens[sessionID+"|"+issuer]; ok {
			return tok
		}
	}
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
func (m *stubOAuthHandler) RefreshTokenIfNeeded(_ context.Context, _, _ string) string {
	return ""
}

var _ api.OAuthHandler = (*stubOAuthHandler)(nil)

func TestMusterTokenStore_GetToken_ReturnsFullToken(t *testing.T) {
	expiry := time.Now().Add(10 * time.Minute)
	handler := &stubOAuthHandler{
		enabled: true,
		fullToken: &api.OAuthToken{
			AccessToken:  "test-access-token",
			RefreshToken: "test-refresh-token",
			ExpiresAt:    expiry,
		},
	}
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", handler)

	token, err := store.GetToken(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "test-access-token", token.AccessToken)
	assert.Equal(t, "Bearer", token.TokenType)
	assert.Equal(t, "test-refresh-token", token.RefreshToken)
	assert.Equal(t, expiry, token.ExpiresAt)
}

func TestMusterTokenStore_GetToken_ReturnsErrNoToken_WhenEmpty(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled:   true,
		fullToken: nil,
	}
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", handler)

	token, err := store.GetToken(t.Context())

	assert.Nil(t, token)
	assert.True(t, errors.Is(err, transport.ErrNoToken))
}

func TestMusterTokenStore_GetToken_ReturnsErrNoToken_WhenEmptyAccessToken(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled: true,
		fullToken: &api.OAuthToken{
			AccessToken: "",
		},
	}
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", handler)

	token, err := store.GetToken(t.Context())

	assert.Nil(t, token)
	assert.True(t, errors.Is(err, transport.ErrNoToken))
}

func TestMusterTokenStore_GetToken_ReturnsErrNoToken_WhenDisabled(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled: false,
	}
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", handler)

	token, err := store.GetToken(t.Context())

	assert.Nil(t, token)
	assert.True(t, errors.Is(err, transport.ErrNoToken))
}

func TestMusterTokenStore_GetToken_ReturnsErrNoToken_WhenNilHandler(t *testing.T) {
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", nil)

	token, err := store.GetToken(t.Context())

	assert.Nil(t, token)
	assert.True(t, errors.Is(err, transport.ErrNoToken))
}

func TestMusterTokenStore_GetToken_CachesIDToken(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled: true,
		fullToken: &api.OAuthToken{
			AccessToken: "access-token",
			IDToken:     "my-id-token",
		},
	}
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", handler)

	_, err := store.GetToken(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "my-id-token", store.GetIDToken())
}

func TestMusterTokenStore_GetToken_IDTokenEmpty_WhenNoIDToken(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled: true,
		fullToken: &api.OAuthToken{
			AccessToken: "access-token",
		},
	}
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", handler)

	_, err := store.GetToken(t.Context())
	require.NoError(t, err)

	assert.Empty(t, store.GetIDToken())
}

func TestMusterTokenStore_GetToken_RespectsContextCancellation(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled: true,
		fullToken: &api.OAuthToken{
			AccessToken: "token",
		},
	}
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", handler)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	token, err := store.GetToken(ctx)

	assert.Nil(t, token)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestMusterTokenStore_SaveToken_Persists(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled: true,
	}
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", handler)

	expiry := time.Now().Add(30 * time.Minute)
	err := store.SaveToken(t.Context(), &transport.Token{
		AccessToken:  "new-access",
		TokenType:    "Bearer",
		RefreshToken: "new-refresh",
		ExpiresAt:    expiry,
	})
	require.NoError(t, err)

	// Verify the token was stored via the handler
	stored := handler.storedTokens["session-1|https://issuer.example.com"]
	require.NotNil(t, stored)
	assert.Equal(t, "new-access", stored.AccessToken)
	assert.Equal(t, "Bearer", stored.TokenType)
	assert.Equal(t, "new-refresh", stored.RefreshToken)
	assert.Equal(t, expiry, stored.ExpiresAt)
	assert.Equal(t, "https://issuer.example.com", stored.Issuer)
}

func TestMusterTokenStore_SaveToken_PreservesIDToken(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled: true,
		fullToken: &api.OAuthToken{
			AccessToken: "original-access",
			IDToken:     "my-id-token",
		},
	}
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", handler)

	// Populate the cached IDToken by calling GetToken first
	_, err := store.GetToken(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "my-id-token", store.GetIDToken())

	// Now save a refreshed token (which won't contain an ID token)
	err = store.SaveToken(t.Context(), &transport.Token{
		AccessToken:  "refreshed-access",
		RefreshToken: "refreshed-refresh",
	})
	require.NoError(t, err)

	// Verify the stored token preserves the cached ID token
	stored := handler.storedTokens["session-1|https://issuer.example.com"]
	require.NotNil(t, stored)
	assert.Equal(t, "refreshed-access", stored.AccessToken)
	assert.Equal(t, "my-id-token", stored.IDToken)
}

func TestMusterTokenStore_SaveToken_RoundTrips(t *testing.T) {
	handler := &stubOAuthHandler{
		enabled: true,
		fullToken: &api.OAuthToken{
			AccessToken:  "original",
			RefreshToken: "original-refresh",
			IDToken:      "original-id",
		},
	}
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", handler)

	// GetToken caches IDToken
	_, err := store.GetToken(t.Context())
	require.NoError(t, err)

	// SaveToken with refreshed token
	expiry := time.Now().Add(1 * time.Hour)
	err = store.SaveToken(t.Context(), &transport.Token{
		AccessToken:  "refreshed",
		RefreshToken: "refreshed-refresh",
		ExpiresAt:    expiry,
	})
	require.NoError(t, err)

	// GetToken should now return the saved token
	token, err := store.GetToken(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "refreshed", token.AccessToken)
	assert.Equal(t, "refreshed-refresh", token.RefreshToken)
	assert.Equal(t, expiry, token.ExpiresAt)

	// IDToken should still be cached
	assert.Equal(t, "original-id", store.GetIDToken())
}

func TestMusterTokenStore_SaveToken_NilToken(t *testing.T) {
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", &stubOAuthHandler{enabled: true})

	err := store.SaveToken(t.Context(), nil)
	assert.NoError(t, err)
}

func TestMusterTokenStore_SaveToken_NilHandler(t *testing.T) {
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", nil)

	err := store.SaveToken(t.Context(), &transport.Token{AccessToken: "token"})
	assert.NoError(t, err)
}

func TestMusterTokenStore_SaveToken_RespectsContextCancellation(t *testing.T) {
	store := NewMusterTokenStore("session-1", "https://issuer.example.com", &stubOAuthHandler{enabled: true})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := store.SaveToken(ctx, &transport.Token{AccessToken: "token"})
	assert.ErrorIs(t, err, context.Canceled)
}
