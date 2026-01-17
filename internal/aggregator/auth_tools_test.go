package aggregator

import (
	"context"
	"net/http"
	"testing"

	"muster/internal/api"
	"muster/internal/config"
)

// issuerMockOAuthHandler implements api.OAuthHandler for testing getMusterIssuer
type issuerMockOAuthHandler struct {
	enabled          bool
	findTokenResult  *api.OAuthToken
	getFullTokenFunc func(sessionID, issuer string) *api.OAuthToken
}

func (m *issuerMockOAuthHandler) IsEnabled() bool {
	return m.enabled
}

func (m *issuerMockOAuthHandler) GetToken(sessionID, serverName string) *api.OAuthToken {
	return nil
}

func (m *issuerMockOAuthHandler) GetTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	return nil
}

func (m *issuerMockOAuthHandler) GetFullTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	if m.getFullTokenFunc != nil {
		return m.getFullTokenFunc(sessionID, issuer)
	}
	return nil
}

func (m *issuerMockOAuthHandler) FindTokenWithIDToken(sessionID string) *api.OAuthToken {
	return m.findTokenResult
}

func (m *issuerMockOAuthHandler) ClearTokenByIssuer(sessionID, issuer string) {
}

func (m *issuerMockOAuthHandler) CreateAuthChallenge(ctx context.Context, sessionID, serverName, issuer, scope string) (*api.AuthChallenge, error) {
	return nil, nil
}

func (m *issuerMockOAuthHandler) GetHTTPHandler() http.Handler {
	return nil
}

func (m *issuerMockOAuthHandler) GetCallbackPath() string {
	return "/oauth/proxy/callback"
}

func (m *issuerMockOAuthHandler) GetCIMDPath() string {
	return "/.well-known/oauth-client.json"
}

func (m *issuerMockOAuthHandler) ShouldServeCIMD() bool {
	return true
}

func (m *issuerMockOAuthHandler) GetCIMDHandler() http.HandlerFunc {
	return nil
}

func (m *issuerMockOAuthHandler) RegisterServer(serverName, issuer, scope string) {
}

func (m *issuerMockOAuthHandler) SetAuthCompletionCallback(callback api.AuthCompletionCallback) {
}

func (m *issuerMockOAuthHandler) RefreshTokenIfNeeded(ctx context.Context, sessionID, issuer string) string {
	return ""
}

func (m *issuerMockOAuthHandler) Stop() {
}

func TestGetMusterIssuer_WithOAuthServerConfig(t *testing.T) {
	// Register a mock OAuth handler
	mockHandler := &issuerMockOAuthHandler{
		enabled: true,
	}
	api.RegisterOAuthHandler(mockHandler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	// Create an aggregator with OAuthServer.Config properly set
	aggregator := &AggregatorServer{
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config: config.OAuthServerConfig{
					BaseURL: "https://muster.example.com",
				},
			},
		},
	}

	provider := NewAuthToolProvider(aggregator)

	// Call getMusterIssuer
	issuer := provider.getMusterIssuer("test-session-id")

	// Should return the BaseURL from the config
	if issuer != "https://muster.example.com" {
		t.Errorf("expected issuer 'https://muster.example.com', got '%s'", issuer)
	}
}

func TestGetMusterIssuer_WithEmptyBaseURL(t *testing.T) {
	// Register a mock OAuth handler
	mockHandler := &issuerMockOAuthHandler{
		enabled: true,
		findTokenResult: &api.OAuthToken{
			Issuer:  "https://fallback-issuer.example.com",
			IDToken: "test-id-token",
		},
	}
	api.RegisterOAuthHandler(mockHandler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	// Create an aggregator with OAuthServer.Config but empty BaseURL
	aggregator := &AggregatorServer{
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config: config.OAuthServerConfig{
					BaseURL: "", // Empty
				},
			},
		},
	}

	provider := NewAuthToolProvider(aggregator)

	// Call getMusterIssuer - should fall back to FindTokenWithIDToken
	issuer := provider.getMusterIssuer("test-session-id")

	// Should return the issuer from the fallback token
	if issuer != "https://fallback-issuer.example.com" {
		t.Errorf("expected issuer 'https://fallback-issuer.example.com', got '%s'", issuer)
	}
}

func TestGetMusterIssuer_OAuthNotEnabled(t *testing.T) {
	// Register a mock OAuth handler that's not enabled
	mockHandler := &issuerMockOAuthHandler{
		enabled: false,
	}
	api.RegisterOAuthHandler(mockHandler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	// Create an aggregator with OAuthServer.Config
	aggregator := &AggregatorServer{
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config: config.OAuthServerConfig{
					BaseURL: "https://muster.example.com",
				},
			},
		},
	}

	provider := NewAuthToolProvider(aggregator)

	// Call getMusterIssuer - should return empty because OAuth handler is not enabled
	issuer := provider.getMusterIssuer("test-session-id")

	if issuer != "" {
		t.Errorf("expected empty issuer when OAuth not enabled, got '%s'", issuer)
	}
}

func TestGetMusterIssuer_NoOAuthHandler(t *testing.T) {
	// Ensure no OAuth handler is registered
	api.RegisterOAuthHandler(nil)

	// Create an aggregator with OAuthServer.Config
	aggregator := &AggregatorServer{
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config: config.OAuthServerConfig{
					BaseURL: "https://muster.example.com",
				},
			},
		},
	}

	provider := NewAuthToolProvider(aggregator)

	// Call getMusterIssuer - should return empty because no OAuth handler
	issuer := provider.getMusterIssuer("test-session-id")

	if issuer != "" {
		t.Errorf("expected empty issuer when no OAuth handler, got '%s'", issuer)
	}
}

func TestGetMusterIssuer_ConfigNotOAuthServerConfig(t *testing.T) {
	// Register a mock OAuth handler
	mockHandler := &issuerMockOAuthHandler{
		enabled: true,
		findTokenResult: &api.OAuthToken{
			Issuer:  "https://fallback-issuer.example.com",
			IDToken: "test-id-token",
		},
	}
	api.RegisterOAuthHandler(mockHandler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	// Create an aggregator with OAuthServer.Config set to wrong type
	aggregator := &AggregatorServer{
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config:  "invalid-type", // Wrong type, should fall back
			},
		},
	}

	provider := NewAuthToolProvider(aggregator)

	// Call getMusterIssuer - should fall back to FindTokenWithIDToken
	issuer := provider.getMusterIssuer("test-session-id")

	// Should return the issuer from the fallback token
	if issuer != "https://fallback-issuer.example.com" {
		t.Errorf("expected issuer 'https://fallback-issuer.example.com', got '%s'", issuer)
	}
}

func TestGetMusterIssuer_NoFallbackToken(t *testing.T) {
	// Register a mock OAuth handler with no fallback token
	mockHandler := &issuerMockOAuthHandler{
		enabled:         true,
		findTokenResult: nil, // No fallback token
	}
	api.RegisterOAuthHandler(mockHandler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	// Create an aggregator with OAuthServer disabled
	aggregator := &AggregatorServer{
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: false, // Disabled
			},
		},
	}

	provider := NewAuthToolProvider(aggregator)

	// Call getMusterIssuer - should return empty
	issuer := provider.getMusterIssuer("test-session-id")

	if issuer != "" {
		t.Errorf("expected empty issuer, got '%s'", issuer)
	}
}
