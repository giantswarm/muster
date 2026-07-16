package aggregator

import (
	"context"
	"net/http"
	"testing"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/stretchr/testify/require"
)

// issuerMockOAuthHandler implements api.OAuthHandler for testing getMusterIssuer
type issuerMockOAuthHandler struct {
	enabled             bool
	findTokenResult     *api.OAuthToken
	getFullTokenFunc    func(sessionID, issuer string) *api.OAuthToken
	exchangeFunc        func(ctx context.Context, localToken, userID string, config *api.TokenExchangeConfig) (string, error)
	createChallengeFunc func(sessionID, userID, serverName, issuer, scope string) (*api.AuthChallenge, error)
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

func (m *issuerMockOAuthHandler) StoreToken(_, _, _ string, _ *api.OAuthToken) {
}

func (m *issuerMockOAuthHandler) ClearTokenByIssuer(_, _ string) {
}

func (m *issuerMockOAuthHandler) DeleteTokensByUser(_ string) {
}

func (m *issuerMockOAuthHandler) DeleteTokensBySession(_ string) {
}

func (m *issuerMockOAuthHandler) CreateAuthChallenge(_ context.Context, sessionID, userID, serverName, issuer, scope string) (*api.AuthChallenge, error) {
	if m.createChallengeFunc != nil {
		return m.createChallengeFunc(sessionID, userID, serverName, issuer, scope)
	}
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

func (m *issuerMockOAuthHandler) Stop() {
}

func (m *issuerMockOAuthHandler) ExchangeTokenForRemoteCluster(ctx context.Context, localToken, userID string, config *api.TokenExchangeConfig) (string, error) {
	if m.exchangeFunc != nil {
		return m.exchangeFunc(ctx, localToken, userID, config)
	}
	return "", nil
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
	issuer := provider.getMusterIssuer("test-user-sub")

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
	issuer := provider.getMusterIssuer("test-user-sub")

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
	issuer := provider.getMusterIssuer("test-user-sub")

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
	issuer := provider.getMusterIssuer("test-user-sub")

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
	issuer := provider.getMusterIssuer("test-user-sub")

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
	issuer := provider.getMusterIssuer("test-user-sub")

	if issuer != "" {
		t.Errorf("expected empty issuer, got '%s'", issuer)
	}
}

func TestHandleAuthLogin_ChallengeCarriesStructuredContent(t *testing.T) {
	mockHandler := &issuerMockOAuthHandler{
		enabled: true,
		createChallengeFunc: func(_, _, serverName, _, _ string) (*api.AuthChallenge, error) {
			return &api.AuthChallenge{
				Status:     "auth_required",
				AuthURL:    "https://idp.example.com/authorize?state=abc",
				ServerName: serverName,
				Message:    "Authentication required for " + serverName + ". Please visit the link below to authenticate.",
			}, nil
		},
	}
	api.RegisterOAuthHandler(mockHandler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	registry := NewServerRegistry("x")
	require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "protected-server"},
		URL:                "https://protected.example.com/mcp",
		AuthInfo:           &AuthInfo{Issuer: "https://idp.example.com", Scope: "openid"},
	}))

	provider := NewAuthToolProvider(&AggregatorServer{registry: registry})

	ctx := api.WithSessionID(api.WithSubject(t.Context(), "user-sub"), "session-1")
	result, err := provider.handleAuthLogin(ctx, map[string]any{"server": "protected-server"})
	require.NoError(t, err)
	require.False(t, result.IsError)

	// Text content keeps the sign-in URL on its own line for text-only consumers.
	require.Len(t, result.Content, 1)
	text, ok := result.Content[0].(string)
	require.True(t, ok)
	require.Contains(t, text, "Authentication Required")
	require.Contains(t, text, "\nhttps://idp.example.com/authorize?state=abc\n")

	challenge, ok := result.StructuredContent.(*api.AuthChallenge)
	require.True(t, ok, "StructuredContent must be an *api.AuthChallenge, got %T", result.StructuredContent)
	require.Equal(t, "auth_required", challenge.Status)
	require.Equal(t, "https://idp.example.com/authorize?state=abc", challenge.AuthURL)
	require.Equal(t, "protected-server", challenge.ServerName)
	require.NotContains(t, challenge.Message, "below", "structured message must not assume the text layout")
	require.Contains(t, challenge.Message, "call this tool again")
}
