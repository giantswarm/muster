package aggregator

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"muster/internal/api"

	"github.com/stretchr/testify/assert"
)

// mockOAuthHandler implements api.OAuthHandler for testing
type mockOAuthHandler struct {
	enabled          bool
	refreshToken     string
	refreshCallCount atomic.Int32
}

func newMockOAuthHandler(enabled bool, token string) *mockOAuthHandler {
	return &mockOAuthHandler{
		enabled:      enabled,
		refreshToken: token,
	}
}

func (m *mockOAuthHandler) IsEnabled() bool { return m.enabled }
func (m *mockOAuthHandler) GetToken(sessionID, serverName string) *api.OAuthToken {
	return nil
}
func (m *mockOAuthHandler) GetTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	return nil
}
func (m *mockOAuthHandler) GetFullTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	return nil
}
func (m *mockOAuthHandler) FindTokenWithIDToken(sessionID string) *api.OAuthToken {
	return nil
}
func (m *mockOAuthHandler) ClearTokenByIssuer(sessionID, issuer string)     {}
func (m *mockOAuthHandler) GetHTTPHandler() http.Handler                    { return nil }
func (m *mockOAuthHandler) GetCallbackPath() string                         { return "" }
func (m *mockOAuthHandler) GetCIMDPath() string                             { return "" }
func (m *mockOAuthHandler) ShouldServeCIMD() bool                           { return false }
func (m *mockOAuthHandler) GetCIMDHandler() http.HandlerFunc                { return nil }
func (m *mockOAuthHandler) RegisterServer(serverName, issuer, scope string) {}
func (m *mockOAuthHandler) SetAuthCompletionCallback(callback api.AuthCompletionCallback) {
}
func (m *mockOAuthHandler) Stop() {}

func (m *mockOAuthHandler) CreateAuthChallenge(ctx context.Context, sessionID, serverName, issuer, scope string) (*api.AuthChallenge, error) {
	return nil, nil
}

func (m *mockOAuthHandler) RefreshTokenIfNeeded(ctx context.Context, sessionID, issuer string) string {
	m.refreshCallCount.Add(1)
	return m.refreshToken
}

func (m *mockOAuthHandler) ExchangeTokenForRemoteCluster(ctx context.Context, localToken, userID string, config *api.TokenExchangeConfig) (string, error) {
	return "", nil
}

func TestSessionTokenProvider(t *testing.T) {
	t.Run("returns empty when handler is nil", func(t *testing.T) {
		provider := NewSessionTokenProvider("session-1", "https://issuer.example.com", "openid", nil)

		token := provider.GetAccessToken(context.Background())
		assert.Empty(t, token)
	})

	t.Run("returns empty when handler is disabled", func(t *testing.T) {
		mockHandler := newMockOAuthHandler(false, "some-token")
		provider := NewSessionTokenProvider("session-1", "https://issuer.example.com", "openid", mockHandler)

		token := provider.GetAccessToken(context.Background())
		assert.Empty(t, token)
	})

	t.Run("returns token from handler when enabled", func(t *testing.T) {
		mockHandler := newMockOAuthHandler(true, "valid-access-token")
		provider := NewSessionTokenProvider("session-1", "https://issuer.example.com", "openid", mockHandler)

		token := provider.GetAccessToken(context.Background())
		assert.Equal(t, "valid-access-token", token)
	})

	t.Run("calls RefreshTokenIfNeeded with correct parameters", func(t *testing.T) {
		mockHandler := newMockOAuthHandler(true, "refreshed-token")
		provider := NewSessionTokenProvider("my-session", "https://my-issuer.com", "profile email", mockHandler)

		// First call
		token := provider.GetAccessToken(context.Background())
		assert.Equal(t, "refreshed-token", token)
		assert.Equal(t, int32(1), mockHandler.refreshCallCount.Load())

		// Second call should also call RefreshTokenIfNeeded
		token2 := provider.GetAccessToken(context.Background())
		assert.Equal(t, "refreshed-token", token2)
		assert.Equal(t, int32(2), mockHandler.refreshCallCount.Load())
	})

	t.Run("GetTokenKey returns correct key", func(t *testing.T) {
		provider := NewSessionTokenProvider("session-xyz", "https://auth.example.com", "read write", nil)

		key := provider.GetTokenKey()
		assert.NotNil(t, key)
		assert.Equal(t, "session-xyz", key.SessionID)
		assert.Equal(t, "https://auth.example.com", key.Issuer)
		assert.Equal(t, "read write", key.Scope)
	})

	t.Run("returns empty when handler returns empty token", func(t *testing.T) {
		mockHandler := newMockOAuthHandler(true, "")
		provider := NewSessionTokenProvider("session-1", "https://issuer.example.com", "openid", mockHandler)

		token := provider.GetAccessToken(context.Background())
		assert.Empty(t, token)
	})
}

func TestSessionTokenProviderConcurrency(t *testing.T) {
	t.Run("handles concurrent access", func(t *testing.T) {
		mockHandler := newMockOAuthHandler(true, "concurrent-token")
		provider := NewSessionTokenProvider("session-concurrent", "https://issuer.example.com", "openid", mockHandler)

		// Run multiple goroutines accessing the provider
		done := make(chan bool, 10)
		for i := 0; i < 10; i++ {
			go func() {
				token := provider.GetAccessToken(context.Background())
				assert.Equal(t, "concurrent-token", token)
				done <- true
			}()
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		// All calls should have been made
		assert.Equal(t, int32(10), mockHandler.refreshCallCount.Load())
	})
}
