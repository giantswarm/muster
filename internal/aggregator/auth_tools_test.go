package aggregator

import (
	"context"
	"net/http"
	"testing"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
)

const fallbackIssuer = "https://fallback-issuer.example.com"

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

func (m *issuerMockOAuthHandler) StoreToken(_, _, _ string, _ *api.OAuthToken) {
}

func (m *issuerMockOAuthHandler) ClearTokenByIssuer(_, _ string) {
}

func (m *issuerMockOAuthHandler) DeleteTokensByUser(_ string) {
}

func (m *issuerMockOAuthHandler) DeleteTokensBySession(_ string) {
}

func (m *issuerMockOAuthHandler) CreateAuthChallenge(_ context.Context, _, _, _, _, _ string) (*api.AuthChallenge, error) {
	return nil, nil
}

func (m *issuerMockOAuthHandler) GetHTTPHandler() http.Handler {
	return nil
}

func (m *issuerMockOAuthHandler) GetCallbackPath() string {
	return "/oauth/proxy/callback" //nolint:goconst
}

func (m *issuerMockOAuthHandler) GetCIMDPath() string {
	return "/.well-known/oauth-client.json" //nolint:goconst
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
	return "", nil
}

func (m *issuerMockOAuthHandler) ExchangeTokenForRemoteClusterWithClient(ctx context.Context, localToken, userID string, config *api.TokenExchangeConfig, httpClient *http.Client) (string, error) {
	return "", nil
}

func TestGetMusterIssuer_WithOAuthServerConfig(t *testing.T) {
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
	aggregator.SetTokenBroker(&mockTokenBroker{enabled: true})

	provider := NewAuthToolProvider(aggregator)
	issuer := provider.getMusterIssuer(t.Context(), "test-user-sub")

	if issuer != "https://muster.example.com" {
		t.Errorf("expected issuer 'https://muster.example.com', got '%s'", issuer)
	}
}

func TestGetMusterIssuer_WithEmptyBaseURL(t *testing.T) {
	aggregator := &AggregatorServer{
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config: config.OAuthServerConfig{
					BaseURL: "",
				},
			},
		},
	}
	aggregator.SetTokenBroker(&mockTokenBroker{
		enabled: true,
		sessionIssuerFn: func(_ context.Context, _ string) (string, error) {
			return fallbackIssuer, nil
		},
	})

	provider := NewAuthToolProvider(aggregator)
	issuer := provider.getMusterIssuer(t.Context(), "test-user-sub")

	if issuer != fallbackIssuer {
		t.Errorf("expected issuer %q, got %q", fallbackIssuer, issuer)
	}
}

func TestGetMusterIssuer_BrokerNotEnabled(t *testing.T) {
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
	aggregator.SetTokenBroker(&mockTokenBroker{enabled: false})

	provider := NewAuthToolProvider(aggregator)
	issuer := provider.getMusterIssuer(t.Context(), "test-user-sub")

	if issuer != "" {
		t.Errorf("expected empty issuer when broker disabled, got %q", issuer)
	}
}

func TestGetMusterIssuer_NoBroker(t *testing.T) {
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
	issuer := provider.getMusterIssuer(t.Context(), "test-user-sub")

	if issuer != "" {
		t.Errorf("expected empty issuer when no broker wired, got %q", issuer)
	}
}

func TestGetMusterIssuer_ConfigNotOAuthServerConfig(t *testing.T) {
	aggregator := &AggregatorServer{
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config:  "invalid-type",
			},
		},
	}
	aggregator.SetTokenBroker(&mockTokenBroker{
		enabled: true,
		sessionIssuerFn: func(_ context.Context, _ string) (string, error) {
			return fallbackIssuer, nil
		},
	})

	provider := NewAuthToolProvider(aggregator)
	issuer := provider.getMusterIssuer(t.Context(), "test-user-sub")

	if issuer != fallbackIssuer {
		t.Errorf("expected issuer %q, got %q", fallbackIssuer, issuer)
	}
}

func TestGetMusterIssuer_NoFallbackToken(t *testing.T) {
	aggregator := &AggregatorServer{
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: false,
			},
		},
	}
	aggregator.SetTokenBroker(&mockTokenBroker{enabled: true})

	provider := NewAuthToolProvider(aggregator)
	issuer := provider.getMusterIssuer(t.Context(), "test-user-sub")

	if issuer != "" {
		t.Errorf("expected empty issuer, got '%s'", issuer)
	}
}
