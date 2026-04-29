package aggregator

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/server"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/stretchr/testify/assert"
)

// mockOAuthHandler implements api.OAuthHandler for testing getIDTokenForForwarding.
type mockOAuthHandler struct {
	enabled bool
	tokens  map[string]*api.OAuthToken // key: sessionID+"|"+issuer
}

var _ api.OAuthHandler = (*mockOAuthHandler)(nil)

func newMockOAuthHandler(enabled bool) *mockOAuthHandler {
	return &mockOAuthHandler{
		enabled: enabled,
		tokens:  make(map[string]*api.OAuthToken),
	}
}

func (m *mockOAuthHandler) IsEnabled() bool                                        { return m.enabled }
func (m *mockOAuthHandler) GetCallbackPath() string                                { return "" }
func (m *mockOAuthHandler) GetHTTPHandler() http.Handler                           { return nil }
func (m *mockOAuthHandler) ShouldServeCIMD() bool                                  { return false }
func (m *mockOAuthHandler) GetCIMDPath() string                                    { return "" }
func (m *mockOAuthHandler) GetCIMDHandler() http.HandlerFunc                       { return nil }
func (m *mockOAuthHandler) GetToken(_, _ string) *api.OAuthToken                   { return nil }
func (m *mockOAuthHandler) GetTokenByIssuer(_, _ string) *api.OAuthToken           { return nil }
func (m *mockOAuthHandler) FindTokenWithIDToken(_ string) *api.OAuthToken          { return nil }
func (m *mockOAuthHandler) ClearTokenByIssuer(_, _ string)                         {}
func (m *mockOAuthHandler) DeleteTokensByUser(_ string)                            {}
func (m *mockOAuthHandler) DeleteTokensBySession(_ string)                         {}
func (m *mockOAuthHandler) RegisterServer(_, _, _ string)                          {}
func (m *mockOAuthHandler) SetAuthCompletionCallback(_ api.AuthCompletionCallback) {}
func (m *mockOAuthHandler) Stop()                                                  {}

func (m *mockOAuthHandler) CreateAuthChallenge(_ context.Context, _, _, _, _, _ string) (*api.AuthChallenge, error) {
	return nil, nil
}

func (m *mockOAuthHandler) ExchangeTokenForRemoteCluster(_ context.Context, _, _ string, _ *api.TokenExchangeConfig) (string, error) {
	return "", nil
}

func (m *mockOAuthHandler) ExchangeTokenForRemoteClusterWithClient(_ context.Context, _, _ string, _ *api.TokenExchangeConfig, _ *http.Client) (string, error) {
	return "", nil
}

func (m *mockOAuthHandler) StoreToken(sessionID, _, issuer string, token *api.OAuthToken) {
	m.tokens[sessionID+"|"+issuer] = token
}

func (m *mockOAuthHandler) GetFullTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	return m.tokens[sessionID+"|"+issuer]
}

func TestGetIDTokenForForwarding(t *testing.T) {
	// Valid JWT-like token with future expiry (not a real JWT, just the format for parsing).
	// The exp claim is set to 9999999999 (year 2286) to ensure it never expires during tests.
	validToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZXhwIjo5OTk5OTk5OTk5fQ.signature" //nolint:goconst,gosec

	t.Run("returns token from context when available", func(t *testing.T) {
		ctx := context.Background()
		ctx = server.ContextWithIDToken(ctx, validToken)

		token := getIDTokenForForwarding(ctx, "test-user", "https://accounts.google.com")

		assert.Equal(t, validToken, token)
	})

	t.Run("returns empty when no token in context and no OAuth handler", func(t *testing.T) {
		ctx := context.Background()

		token := getIDTokenForForwarding(ctx, "test-user", "https://accounts.google.com")

		assert.Empty(t, token)
	})

	t.Run("context token takes priority over empty string", func(t *testing.T) {
		ctx := context.Background()
		ctx = server.ContextWithIDToken(ctx, validToken)

		token := getIDTokenForForwarding(ctx, "test-user", "")

		assert.Equal(t, validToken, token)
	})

	t.Run("returns empty for empty context token", func(t *testing.T) {
		ctx := context.Background()
		ctx = server.ContextWithIDToken(ctx, "")

		token := getIDTokenForForwarding(ctx, "test-user", "https://accounts.google.com")

		assert.Empty(t, token)
	})

	t.Run("returns token from OAuth handler when context has none", func(t *testing.T) {
		mock := newMockOAuthHandler(true)
		mock.StoreToken("session-abc", "user1", "https://accounts.google.com", &api.OAuthToken{IDToken: validToken})
		api.RegisterOAuthHandler(mock)
		defer api.RegisterOAuthHandler(nil)

		ctx := context.Background()
		token := getIDTokenForForwarding(ctx, "session-abc", "https://accounts.google.com")

		assert.Equal(t, validToken, token)
	})

	t.Run("context token takes priority over OAuth handler token", func(t *testing.T) {
		storedToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJjYWNoZWQiLCJleHAiOjk5OTk5OTk5OTl9.sig" //nolint:gosec
		mock := newMockOAuthHandler(true)
		mock.StoreToken("session-abc", "user1", "https://accounts.google.com", &api.OAuthToken{IDToken: storedToken})
		api.RegisterOAuthHandler(mock)
		defer api.RegisterOAuthHandler(nil)

		ctx := context.Background()
		ctx = server.ContextWithIDToken(ctx, validToken)

		token := getIDTokenForForwarding(ctx, "session-abc", "https://accounts.google.com")
		assert.Equal(t, validToken, token)
	})

	t.Run("returns empty when OAuth handler has no entry for session", func(t *testing.T) {
		mock := newMockOAuthHandler(true)
		api.RegisterOAuthHandler(mock)
		defer api.RegisterOAuthHandler(nil)

		ctx := context.Background()
		token := getIDTokenForForwarding(ctx, "unknown-session", "https://accounts.google.com")

		assert.Empty(t, token)
	})

	t.Run("returns empty when OAuth handler returns nil token", func(t *testing.T) {
		mock := newMockOAuthHandler(true)
		api.RegisterOAuthHandler(mock)
		defer api.RegisterOAuthHandler(nil)

		ctx := context.Background()
		token := getIDTokenForForwarding(ctx, "session-abc", "https://accounts.google.com")

		assert.Empty(t, token)
	})
}

func TestShouldUseTokenForwarding(t *testing.T) {
	t.Run("returns false for nil server info", func(t *testing.T) {
		assert.False(t, ShouldUseTokenForwarding(nil))
	})

	t.Run("returns false for nil auth config", func(t *testing.T) {
		info := &ServerInfo{
			Name:       "test-server",
			AuthConfig: nil,
		}
		assert.False(t, ShouldUseTokenForwarding(info))
	})

	t.Run("returns false when forwardToken is false", func(t *testing.T) {
		info := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type:         "oauth",
				ForwardToken: false,
			},
		}
		assert.False(t, ShouldUseTokenForwarding(info))
	})

	t.Run("returns true when forwardToken is true and type is oauth", func(t *testing.T) {
		info := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type:         "oauth",
				ForwardToken: true,
			},
		}
		assert.True(t, ShouldUseTokenForwarding(info))
	})

	t.Run("returns true when forwardToken is true without type specified", func(t *testing.T) {
		// forwardToken: true implies OAuth authentication
		info := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				ForwardToken: true,
			},
		}
		assert.True(t, ShouldUseTokenForwarding(info))
	})
}

// Note: appendAudienceScopes tests have been moved to mcp-oauth library.
// The local function was replaced with dex.AppendAudienceScopes() which has
// comprehensive tests in the mcp-oauth providers/dex package.

func TestIsIDTokenExpired(t *testing.T) {
	t.Run("empty token is expired", func(t *testing.T) {
		assert.True(t, isIDTokenExpired(""))
	})

	t.Run("invalid JWT format is expired", func(t *testing.T) {
		assert.True(t, isIDTokenExpired("not-a-jwt"))
	})

	t.Run("valid future exp is not expired", func(t *testing.T) {
		// Token with exp = 9999999999 (year 2286)
		token := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjk5OTk5OTk5OTl9.sig" //nolint:goconst,gosec
		assert.False(t, isIDTokenExpired(token))
	})

	t.Run("past exp is expired", func(t *testing.T) {
		// Token with exp = 0 (1970)
		token := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjB9.sig" //nolint:gosec
		assert.True(t, isIDTokenExpired(token))
	})

	t.Run("missing exp claim is expired", func(t *testing.T) {
		// Token with no exp claim
		token := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.sig" //nolint:gosec
		assert.True(t, isIDTokenExpired(token))
	})
}

func TestShouldUseTokenExchange(t *testing.T) {
	t.Run("returns false for nil server info", func(t *testing.T) {
		assert.False(t, ShouldUseTokenExchange(nil))
	})

	t.Run("returns false for nil auth config", func(t *testing.T) {
		info := &ServerInfo{
			Name:       "test-server",
			AuthConfig: nil,
		}
		assert.False(t, ShouldUseTokenExchange(info))
	})

	t.Run("returns false when tokenExchange is nil", func(t *testing.T) {
		info := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type:          "oauth",
				TokenExchange: nil,
			},
		}
		assert.False(t, ShouldUseTokenExchange(info))
	})

	t.Run("returns false when tokenExchange.Enabled is false", func(t *testing.T) {
		info := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
				TokenExchange: &api.TokenExchangeConfig{ //nolint:gosec
					Enabled:          false,
					DexTokenEndpoint: "https://dex.example.com/token",
					ConnectorID:      "local-dex",
				},
			},
		}
		assert.False(t, ShouldUseTokenExchange(info))
	})

	t.Run("returns false when required fields are missing", func(t *testing.T) {
		info := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
				TokenExchange: &api.TokenExchangeConfig{
					Enabled: true,
					// Missing DexTokenEndpoint and ConnectorID
				},
			},
		}
		assert.False(t, ShouldUseTokenExchange(info))
	})

	t.Run("returns false when DexTokenEndpoint is missing", func(t *testing.T) {
		info := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
				TokenExchange: &api.TokenExchangeConfig{
					Enabled:     true,
					ConnectorID: "local-dex",
				},
			},
		}
		assert.False(t, ShouldUseTokenExchange(info))
	})

	t.Run("returns false when ConnectorID is missing", func(t *testing.T) {
		info := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
				TokenExchange: &api.TokenExchangeConfig{ //nolint:gosec
					Enabled:          true,
					DexTokenEndpoint: "https://dex.example.com/token",
				},
			},
		}
		assert.False(t, ShouldUseTokenExchange(info))
	})

	t.Run("returns true when fully configured", func(t *testing.T) {
		info := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
				TokenExchange: &api.TokenExchangeConfig{ //nolint:gosec
					Enabled:          true,
					DexTokenEndpoint: "https://dex.example.com/token",
					ConnectorID:      "local-dex",
					Scopes:           "openid profile email groups",
				},
			},
		}
		assert.True(t, ShouldUseTokenExchange(info))
	})
}

func TestExtractUserIDFromToken(t *testing.T) {
	t.Run("returns empty for empty token", func(t *testing.T) {
		assert.Equal(t, "", extractUserIDFromToken(""))
	})

	t.Run("returns empty for invalid JWT format", func(t *testing.T) {
		assert.Equal(t, "", extractUserIDFromToken("not-a-jwt"))
	})

	t.Run("extracts sub claim from valid JWT", func(t *testing.T) {
		// Token with sub = "user123"
		// Payload: {"sub":"user123","exp":9999999999}
		// base64url encoded: eyJzdWIiOiJ1c2VyMTIzIiwiZXhwIjo5OTk5OTk5OTk5fQ
		token := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIiwiZXhwIjo5OTk5OTk5OTk5fQ.sig" //nolint:gosec
		assert.Equal(t, "user123", extractUserIDFromToken(token))
	})

	t.Run("returns empty when sub claim is missing", func(t *testing.T) {
		// Token with only exp claim
		token := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjk5OTk5OTk5OTl9.sig" //nolint:gosec
		assert.Equal(t, "", extractUserIDFromToken(token))
	})
}

func TestDecodeJWTPayload(t *testing.T) {
	t.Run("returns error for empty token", func(t *testing.T) {
		_, err := decodeJWTPayload("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "token is empty")
	})

	t.Run("returns error for invalid JWT format", func(t *testing.T) {
		_, err := decodeJWTPayload("not-a-jwt")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid JWT format")
	})

	t.Run("decodes valid JWT payload", func(t *testing.T) {
		// Token with payload: {"sub":"user123","exp":9999999999}
		token := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIiwiZXhwIjo5OTk5OTk5OTk5fQ.sig" //nolint:gosec
		decoded, err := decodeJWTPayload(token)
		assert.NoError(t, err)
		assert.Contains(t, string(decoded), "user123")
		assert.Contains(t, string(decoded), "9999999999")
	})

	t.Run("handles token with only two parts", func(t *testing.T) {
		// Minimal JWT with just header and payload (no signature)
		token := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0" //nolint:gosec
		decoded, err := decodeJWTPayload(token)
		assert.NoError(t, err)
		assert.Contains(t, string(decoded), "test")
	})
}

// mockSecretCredentialsHandler implements api.SecretCredentialsHandler for testing.
type mockSecretCredentialsHandler struct {
	credentials *api.ClientCredentials
	err         error
	// Track calls for verification
	loadCalls     int
	lastSecretRef *api.ClientCredentialsSecretRef
	lastDefaultNS string
}

func (m *mockSecretCredentialsHandler) LoadClientCredentials(
	ctx context.Context,
	secretRef *api.ClientCredentialsSecretRef,
	defaultNamespace string,
) (*api.ClientCredentials, error) {
	m.loadCalls++
	m.lastSecretRef = secretRef
	m.lastDefaultNS = defaultNamespace
	if m.err != nil {
		return nil, m.err
	}
	return m.credentials, nil
}

func TestLoadTokenExchangeCredentials(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error when serverInfo has nil AuthConfig", func(t *testing.T) {
		serverInfo := &ServerInfo{
			Name:       "test-server",
			AuthConfig: nil,
		}
		_, err := loadTokenExchangeCredentials(ctx, serverInfo)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no client credentials secret reference configured")
	})

	t.Run("returns error when TokenExchange is nil", func(t *testing.T) {
		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type:          "oauth",
				TokenExchange: nil,
			},
		}
		_, err := loadTokenExchangeCredentials(ctx, serverInfo)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no client credentials secret reference configured")
	})

	t.Run("returns error when ClientCredentialsSecretRef is nil", func(t *testing.T) {
		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
				TokenExchange: &api.TokenExchangeConfig{ //nolint:gosec
					Enabled:                    true,
					DexTokenEndpoint:           "https://dex.example.com/token",
					ConnectorID:                "local-dex",
					ClientCredentialsSecretRef: nil,
				},
			},
		}
		_, err := loadTokenExchangeCredentials(ctx, serverInfo)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no client credentials secret reference configured")
	})

	t.Run("returns error when handler is not registered", func(t *testing.T) {
		// Ensure no handler is registered
		api.RegisterSecretCredentialsHandler(nil)

		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
				TokenExchange: &api.TokenExchangeConfig{ //nolint:gosec
					Enabled:          true,
					DexTokenEndpoint: "https://dex.example.com/token",
					ConnectorID:      "local-dex",
					ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{
						Name: "test-credentials",
					},
				},
			},
		}
		_, err := loadTokenExchangeCredentials(ctx, serverInfo)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "secret credentials handler not registered")
	})

	t.Run("returns credentials when handler succeeds", func(t *testing.T) {
		expectedCreds := &api.ClientCredentials{
			ClientID:     "my-client-id",
			ClientSecret: "my-client-secret",
		}
		mockHandler := &mockSecretCredentialsHandler{
			credentials: expectedCreds,
		}
		api.RegisterSecretCredentialsHandler(mockHandler)
		defer api.RegisterSecretCredentialsHandler(nil)

		serverInfo := &ServerInfo{
			Name:      "test-server",
			Namespace: "muster",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
				TokenExchange: &api.TokenExchangeConfig{ //nolint:gosec
					Enabled:          true,
					DexTokenEndpoint: "https://dex.example.com/token",
					ConnectorID:      "local-dex",
					ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{
						Name:      "test-credentials",
						Namespace: "secrets-ns",
					},
				},
			},
		}
		creds, err := loadTokenExchangeCredentials(ctx, serverInfo)
		assert.NoError(t, err)
		assert.Equal(t, expectedCreds.ClientID, creds.ClientID)
		assert.Equal(t, expectedCreds.ClientSecret, creds.ClientSecret)
		assert.Equal(t, 1, mockHandler.loadCalls)
		assert.Equal(t, "test-credentials", mockHandler.lastSecretRef.Name)
		assert.Equal(t, "secrets-ns", mockHandler.lastSecretRef.Namespace)
		assert.Equal(t, "muster", mockHandler.lastDefaultNS)
	})

	t.Run("uses server namespace as default when GetNamespace returns value", func(t *testing.T) {
		expectedCreds := &api.ClientCredentials{
			ClientID:     "client-id",
			ClientSecret: "client-secret",
		}
		mockHandler := &mockSecretCredentialsHandler{
			credentials: expectedCreds,
		}
		api.RegisterSecretCredentialsHandler(mockHandler)
		defer api.RegisterSecretCredentialsHandler(nil)

		serverInfo := &ServerInfo{
			Name:      "test-server",
			Namespace: "my-namespace",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
				TokenExchange: &api.TokenExchangeConfig{ //nolint:gosec
					Enabled:          true,
					DexTokenEndpoint: "https://dex.example.com/token",
					ConnectorID:      "local-dex",
					ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{
						Name: "test-credentials",
						// No namespace specified - should use server's namespace
					},
				},
			},
		}
		_, err := loadTokenExchangeCredentials(ctx, serverInfo)
		assert.NoError(t, err)
		assert.Equal(t, "my-namespace", mockHandler.lastDefaultNS)
	})

	t.Run("uses 'default' namespace when server namespace is empty", func(t *testing.T) {
		expectedCreds := &api.ClientCredentials{
			ClientID:     "client-id",
			ClientSecret: "client-secret",
		}
		mockHandler := &mockSecretCredentialsHandler{
			credentials: expectedCreds,
		}
		api.RegisterSecretCredentialsHandler(mockHandler)
		defer api.RegisterSecretCredentialsHandler(nil)

		serverInfo := &ServerInfo{
			Name:      "test-server",
			Namespace: "", // Empty namespace
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
				TokenExchange: &api.TokenExchangeConfig{ //nolint:gosec
					Enabled:          true,
					DexTokenEndpoint: "https://dex.example.com/token",
					ConnectorID:      "local-dex",
					ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{
						Name: "test-credentials",
					},
				},
			},
		}
		_, err := loadTokenExchangeCredentials(ctx, serverInfo)
		assert.NoError(t, err)
		assert.Equal(t, "default", mockHandler.lastDefaultNS)
	})

	t.Run("returns error when handler returns error", func(t *testing.T) {
		mockHandler := &mockSecretCredentialsHandler{
			err: errors.New("secret not found"),
		}
		api.RegisterSecretCredentialsHandler(mockHandler)
		defer api.RegisterSecretCredentialsHandler(nil)

		serverInfo := &ServerInfo{
			Name:      "test-server",
			Namespace: "muster",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
				TokenExchange: &api.TokenExchangeConfig{ //nolint:gosec
					Enabled:          true,
					DexTokenEndpoint: "https://dex.example.com/token",
					ConnectorID:      "local-dex",
					ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{
						Name: "nonexistent-secret",
					},
				},
			},
		}
		_, err := loadTokenExchangeCredentials(ctx, serverInfo)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "secret not found")
	})
}

func TestHeaderFunc_RateLimitsWarning(t *testing.T) {
	// Set up a logger that captures output at DEBUG level so we can see all messages.
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	sessionID := "test-session-rate-limit"
	sub := "test-user"                        //nolint:goconst
	musterIssuer := "https://dex.example.com" //nolint:goconst
	serverName := "test-server"               //nolint:goconst
	fallbackToken := "original-token"         //nolint:goconst

	// No OAuth handler registered means getIDTokenForForwarding always returns "".
	api.RegisterOAuthHandler(nil)

	headerFunc := makeTokenForwardingHeaderFunc(sessionID, sub, musterIssuer, serverName, fallbackToken, nil)

	// First call: should produce a WARN (interval has not been hit yet).
	logBuf.Reset()
	headers := headerFunc(context.Background())
	assert.Equal(t, "Bearer "+fallbackToken, headers["Authorization"])

	firstCallLogs := logBuf.String()
	assert.Contains(t, firstCallLogs, "WARN", "first call should emit a WARN log")
	assert.NotContains(t, firstCallLogs, "warning suppressed", "first call should not suppress")

	// Second call immediately after: should be suppressed to DEBUG.
	logBuf.Reset()
	headers = headerFunc(context.Background())
	assert.Equal(t, "Bearer "+fallbackToken, headers["Authorization"])

	secondCallLogs := logBuf.String()
	assert.NotContains(t, secondCallLogs, "WARN", "second call should NOT emit a WARN (rate-limited)")
	assert.Contains(t, secondCallLogs, "warning suppressed", "second call should log at DEBUG with suppression note")

	// Third call also immediately after: still suppressed.
	logBuf.Reset()
	_ = headerFunc(context.Background())
	thirdCallLogs := logBuf.String()
	assert.NotContains(t, thirdCallLogs, "WARN", "third call should NOT emit a WARN")

	// Now simulate token recovery by registering an OAuth handler with a token.
	validToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZXhwIjo5OTk5OTk5OTk5fQ.signature" //nolint:gosec
	mock := newMockOAuthHandler(true)
	mock.StoreToken(sessionID, "", musterIssuer, &api.OAuthToken{IDToken: validToken})
	api.RegisterOAuthHandler(mock)
	defer api.RegisterOAuthHandler(nil)

	logBuf.Reset()
	headers = headerFunc(context.Background())
	assert.Equal(t, "Bearer "+validToken, headers["Authorization"])

	recoveryLogs := logBuf.String()
	assert.Contains(t, recoveryLogs, "recovered", "should log token recovery at INFO")
}

func TestHeaderFunc_EvictsAfterConsecutiveFailures(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	sessionID := "test-session-evict"
	sub := "test-user"
	musterIssuer := "https://dex.example.com"
	serverName := "test-server"
	fallbackToken := "original-token"

	api.RegisterOAuthHandler(nil)
	defer api.RegisterOAuthHandler(nil)

	var evictCount atomic.Int32
	var firstEvict sync.WaitGroup
	firstEvict.Add(1)
	onStaleToken := func() {
		if evictCount.Add(1) == 1 {
			firstEvict.Done()
		}
	}

	headerFunc := makeTokenForwardingHeaderFunc(sessionID, sub, musterIssuer, serverName, fallbackToken, onStaleToken)

	// Call fewer than maxConsecutiveTokenFailures times — callback should NOT fire.
	for i := 0; i < maxConsecutiveTokenFailures-1; i++ {
		headers := headerFunc(context.Background())
		assert.Equal(t, "Bearer "+fallbackToken, headers["Authorization"])
	}
	assert.Equal(t, int32(0), evictCount.Load(),
		"onStaleToken should not fire before reaching maxConsecutiveTokenFailures")

	// One more call should trigger the eviction callback.
	logBuf.Reset()
	headers := headerFunc(context.Background())
	assert.Equal(t, "Bearer "+fallbackToken, headers["Authorization"])

	firstEvict.Wait()
	assert.Equal(t, int32(1), evictCount.Load(),
		"onStaleToken should fire exactly once after reaching threshold")

	logs := logBuf.String()
	assert.Contains(t, logs, "evicting stale connection",
		"should log eviction message at WARN level")

	// Subsequent calls should NOT fire the callback again (staleEvicted=true
	// prevents goroutine launch, so no synchronization needed).
	headers = headerFunc(context.Background())
	assert.Equal(t, "Bearer "+fallbackToken, headers["Authorization"])
	assert.Equal(t, int32(1), evictCount.Load(),
		"onStaleToken should fire at most once per failure streak")
}

func TestHeaderFunc_ResetsFailureCountOnRecovery(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	sessionID := "test-session-reset"
	sub := "test-user"
	musterIssuer := "https://dex.example.com"
	serverName := "test-server"
	fallbackToken := "original-token"

	api.RegisterOAuthHandler(nil)

	var evictCount atomic.Int32
	onStaleToken := func() {
		evictCount.Add(1)
	}

	headerFunc := makeTokenForwardingHeaderFunc(sessionID, sub, musterIssuer, serverName, fallbackToken, onStaleToken)

	// Accumulate failures just below the threshold.
	for i := 0; i < maxConsecutiveTokenFailures-1; i++ {
		headerFunc(context.Background())
	}
	assert.Equal(t, int32(0), evictCount.Load(), "should not evict before threshold")

	// Recover by providing a valid token.
	validToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZXhwIjo5OTk5OTk5OTk5fQ.signature" //nolint:gosec
	mock := newMockOAuthHandler(true)
	mock.StoreToken(sessionID, "", musterIssuer, &api.OAuthToken{IDToken: validToken})
	api.RegisterOAuthHandler(mock)
	defer api.RegisterOAuthHandler(nil)

	headers := headerFunc(context.Background())
	assert.Equal(t, "Bearer "+validToken, headers["Authorization"],
		"should use recovered token")

	// Now remove the token again and accumulate failures.
	api.RegisterOAuthHandler(nil)
	for i := 0; i < maxConsecutiveTokenFailures-1; i++ {
		headerFunc(context.Background())
	}
	assert.Equal(t, int32(0), evictCount.Load(),
		"failure counter should have reset on recovery; should not evict yet")
}

func TestHeaderFunc_NilCallback(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	api.RegisterOAuthHandler(nil)
	defer api.RegisterOAuthHandler(nil)

	headerFunc := makeTokenForwardingHeaderFunc("s", "u", "iss", "srv", "tok", nil)

	// Should not panic even after many failures with nil callback.
	for i := 0; i < maxConsecutiveTokenFailures+5; i++ {
		headers := headerFunc(context.Background())
		assert.Equal(t, "Bearer tok", headers["Authorization"])
	}
}

func TestGetTokenExpiryTime(t *testing.T) {
	t.Run("returns zero for empty token", func(t *testing.T) {
		result := getTokenExpiryTime("")
		assert.True(t, result.IsZero())
	})

	t.Run("returns zero for invalid JWT", func(t *testing.T) {
		result := getTokenExpiryTime("not-a-jwt")
		assert.True(t, result.IsZero())
	})

	t.Run("returns zero for missing exp claim", func(t *testing.T) {
		// Token with only sub claim: {"sub":"test"}
		token := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.sig" //nolint:gosec
		result := getTokenExpiryTime(token)
		assert.True(t, result.IsZero())
	})

	t.Run("returns correct time for valid exp", func(t *testing.T) {
		// Token with exp = 9999999999
		token := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjk5OTk5OTk5OTl9.sig" //nolint:gosec
		result := getTokenExpiryTime(token)
		assert.False(t, result.IsZero())
		assert.Equal(t, int64(9999999999), result.Unix())
	})
}
