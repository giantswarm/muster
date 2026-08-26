package aggregator

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
)

// issuerMockOAuthHandler implements api.OAuthHandler for testing getMusterIssuer
type issuerMockOAuthHandler struct {
	enabled          bool
	findTokenResult  *api.OAuthToken
	getFullTokenFunc func(sessionID, issuer string) *api.OAuthToken
	exchangeFunc     func(ctx context.Context, localToken, userID string, config *api.TokenExchangeConfig) (string, error)
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

func (m *issuerMockOAuthHandler) CreateAuthChallenge(_ context.Context, _ api.AuthChallengeParams) (*api.AuthChallenge, error) {
	return nil, nil
}

func (m *issuerMockOAuthHandler) GetHTTPHandler() http.Handler {
	return nil
}

func (m *issuerMockOAuthHandler) GetCallbackPath() string {
	return "/oauth/proxy/callback"
}

func (m *issuerMockOAuthHandler) GetStartPath() string {
	return "/oauth/proxy/start"
}

func (m *issuerMockOAuthHandler) GetStartHandler() http.HandlerFunc {
	return nil
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

func TestAuthChallengeResult_StructuredAuthURL(t *testing.T) {
	result := authChallengeResult("github", &api.AuthChallenge{
		AuthURL: "https://example.com/oauth/start?state=abc",
		Message: "authentication required",
	})

	if result.IsError {
		t.Fatal("expected non-error result")
	}

	sc, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected structured content map, got %T", result.StructuredContent)
	}
	if sc["authUrl"] != "https://example.com/oauth/start?state=abc" {
		t.Errorf("expected authUrl in structured content, got %v", sc["authUrl"])
	}

	// Prose must keep carrying the URL for text-only consumers.
	text, ok := result.Content[0].(string)
	if !ok || !strings.Contains(text, "https://example.com/oauth/start?state=abc") {
		t.Errorf("expected sign-in URL in prose content, got %v", result.Content[0])
	}
}

func TestResourceIndicator(t *testing.T) {
	tests := []struct {
		name             string
		declaredResource string
		serverURL        string
		want             string
		wantErr          bool
	}{
		{
			name:             "prefers the declared resource",
			declaredResource: "https://backend.example.com/mcp",
			serverURL:        "https://backend.example.com/mcp/",
			want:             "https://backend.example.com/mcp",
		},
		{
			name:             "sends a declared resource that is not canonical unchanged",
			declaredResource: "https://backend.example.com:443/mcp/",
			serverURL:        "https://backend.example.com/mcp",
			want:             "https://backend.example.com:443/mcp/",
		},
		{
			name:             "falls back when the declared resource carries a fragment",
			declaredResource: "https://backend.example.com/mcp#a",
			serverURL:        "https://backend.example.com/mcp",
			want:             "https://backend.example.com/mcp",
		},
		{
			name:      "derives the same value mcp-go derives on refresh",
			serverURL: "https://backend.example.com:443/mcp/?tenant=a",
			want:      "https://backend.example.com:443/mcp/",
		},
		{
			name:             "falls back when the declared resource is unusable",
			declaredResource: "not a uri",
			serverURL:        "https://backend.example.com/mcp",
			want:             "https://backend.example.com/mcp",
		},
		{
			name: "returns empty when the backend has no URL",
			want: "",
		},
		{
			name:      "fails on a URL no indicator can be derived from",
			serverURL: "stdio",
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resourceIndicator(tc.declaredResource, tc.serverURL)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got %q", tc.serverURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestNeedsResourceMetadata(t *testing.T) {
	tests := []struct {
		name      string
		authInfo  AuthInfo
		serverURL string
		want      bool
	}{
		{
			name:      "resource missing although the 401 supplied issuer and scope",
			authInfo:  AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			serverURL: "https://backend.example.com/mcp",
			want:      true,
		},
		{
			name:      "nothing left to discover",
			authInfo:  AuthInfo{Issuer: "https://dex.example.com", Scope: "openid", Resource: "https://backend.example.com/mcp"},
			serverURL: "https://backend.example.com/mcp",
			want:      false,
		},
		{
			name:      "issuer missing",
			authInfo:  AuthInfo{Scope: "openid", Resource: "https://backend.example.com/mcp"},
			serverURL: "https://backend.example.com/mcp",
			want:      true,
		},
		{
			name:      "scope missing",
			authInfo:  AuthInfo{Issuer: "https://dex.example.com", Resource: "https://backend.example.com/mcp"},
			serverURL: "https://backend.example.com/mcp",
			want:      true,
		},
		{
			name:     "no URL to probe",
			authInfo: AuthInfo{},
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsResourceMetadata(&tc.authInfo, tc.serverURL); got != tc.want {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}
