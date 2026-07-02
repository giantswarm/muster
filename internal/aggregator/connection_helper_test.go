package aggregator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/giantswarm/muster/internal/api"
	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
	"github.com/giantswarm/muster/internal/server"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func (m *mockOAuthHandler) StoreToken(sessionID, _, issuer string, token *api.OAuthToken) {
	m.tokens[sessionID+"|"+issuer] = token
}

func (m *mockOAuthHandler) GetFullTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	return m.tokens[sessionID+"|"+issuer]
}

func TestGetIDTokenForForwarding(t *testing.T) {
	// Valid JWT-like token with future expiry (not a real JWT, just the format for parsing).
	// The exp claim is set to 9999999999 (year 2286) to ensure it never expires during tests.
	validToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZXhwIjo5OTk5OTk5OTk5fQ.signature"

	t.Run("returns token from context when available", func(t *testing.T) {
		ctx := context.Background()
		ctx = server.ContextWithIDToken(ctx, validToken)

		token := getIDTokenForForwarding(ctx, "test-user", "https://accounts.google.com", nil)

		assert.Equal(t, validToken, token)
	})

	t.Run("returns empty when no token in context and no OAuth handler", func(t *testing.T) {
		ctx := context.Background()

		token := getIDTokenForForwarding(ctx, "test-user", "https://accounts.google.com", nil)

		assert.Empty(t, token)
	})

	t.Run("context token takes priority over empty string", func(t *testing.T) {
		ctx := context.Background()
		ctx = server.ContextWithIDToken(ctx, validToken)

		token := getIDTokenForForwarding(ctx, "test-user", "", nil)

		assert.Equal(t, validToken, token)
	})

	t.Run("returns empty for empty context token", func(t *testing.T) {
		ctx := context.Background()
		ctx = server.ContextWithIDToken(ctx, "")

		token := getIDTokenForForwarding(ctx, "test-user", "https://accounts.google.com", nil)

		assert.Empty(t, token)
	})

	t.Run("returns token from OAuth handler when context has none", func(t *testing.T) {
		mock := newMockOAuthHandler(true)
		mock.StoreToken("session-abc", "user1", "https://accounts.google.com", &api.OAuthToken{IDToken: validToken})
		api.RegisterOAuthHandler(mock)
		defer api.RegisterOAuthHandler(nil)

		ctx := context.Background()
		token := getIDTokenForForwarding(ctx, "session-abc", "https://accounts.google.com", nil)

		assert.Equal(t, validToken, token)
	})

	t.Run("context token takes priority over OAuth handler token", func(t *testing.T) {
		storedToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJjYWNoZWQiLCJleHAiOjk5OTk5OTk5OTl9.sig"
		mock := newMockOAuthHandler(true)
		mock.StoreToken("session-abc", "user1", "https://accounts.google.com", &api.OAuthToken{IDToken: storedToken})
		api.RegisterOAuthHandler(mock)
		defer api.RegisterOAuthHandler(nil)

		ctx := context.Background()
		ctx = server.ContextWithIDToken(ctx, validToken)

		token := getIDTokenForForwarding(ctx, "session-abc", "https://accounts.google.com", nil)
		assert.Equal(t, validToken, token)
	})

	t.Run("returns empty when OAuth handler has no entry for session", func(t *testing.T) {
		mock := newMockOAuthHandler(true)
		api.RegisterOAuthHandler(mock)
		defer api.RegisterOAuthHandler(nil)

		ctx := context.Background()
		token := getIDTokenForForwarding(ctx, "unknown-session", "https://accounts.google.com", nil)

		assert.Empty(t, token)
	})

	t.Run("returns empty when OAuth handler returns nil token", func(t *testing.T) {
		mock := newMockOAuthHandler(true)
		api.RegisterOAuthHandler(mock)
		defer api.RegisterOAuthHandler(nil)

		ctx := context.Background()
		token := getIDTokenForForwarding(ctx, "session-abc", "https://accounts.google.com", nil)

		assert.Empty(t, token)
	})

	t.Run("calls refresher and re-reads store when no valid token found", func(t *testing.T) {
		mock := newMockOAuthHandler(true)
		api.RegisterOAuthHandler(mock)
		defer api.RegisterOAuthHandler(nil)

		sessionID := "session-refresh-test"
		issuer := "https://dex.example.com"

		refreshCalled := false
		refresher := func(_ context.Context, familyID string) error {
			refreshCalled = true
			// Simulate TokenRefreshHandler firing: populate the proxy store.
			mock.StoreToken(familyID, "user1", issuer, &api.OAuthToken{IDToken: validToken})
			return nil
		}

		ctx := context.Background()
		token := getIDTokenForForwarding(ctx, sessionID, issuer, refresher)

		require.True(t, refreshCalled, "refresher must be called when no valid token exists")
		assert.Equal(t, validToken, token)
	})

	t.Run("returns empty when refresher is called but store still empty", func(t *testing.T) {
		mock := newMockOAuthHandler(true)
		api.RegisterOAuthHandler(mock)
		defer api.RegisterOAuthHandler(nil)

		refreshCalled := false
		refresher := func(_ context.Context, _ string) error {
			refreshCalled = true
			return nil // refresh succeeded but nothing stored (e.g. no id_token in response)
		}

		ctx := context.Background()
		token := getIDTokenForForwarding(ctx, "session-no-id-token", "https://dex.example.com", refresher)

		require.True(t, refreshCalled)
		assert.Empty(t, token)
	})

	t.Run("returns empty when refresher errors", func(t *testing.T) {
		mock := newMockOAuthHandler(true)
		api.RegisterOAuthHandler(mock)
		defer api.RegisterOAuthHandler(nil)

		refresher := func(_ context.Context, _ string) error {
			return fmt.Errorf("upstream refresh failed")
		}

		ctx := context.Background()
		token := getIDTokenForForwarding(ctx, "session-refresh-err", "https://dex.example.com", refresher)

		assert.Empty(t, token)
	})

	t.Run("skips refresher when context already has a token", func(t *testing.T) {
		ctx := context.Background()
		ctx = server.ContextWithIDToken(ctx, validToken)

		refreshCalled := false
		refresher := func(_ context.Context, _ string) error {
			refreshCalled = true
			return nil
		}

		token := getIDTokenForForwarding(ctx, "any-session", "https://dex.example.com", refresher)

		assert.False(t, refreshCalled, "refresher must not be called when context token is present")
		assert.Equal(t, validToken, token)
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
				TokenExchange: &api.TokenExchangeConfig{
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
				TokenExchange: &api.TokenExchangeConfig{
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
				TokenExchange: &api.TokenExchangeConfig{
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

func (m *mockSecretCredentialsHandler) LoadSecretKey(_ context.Context, _ *api.ClientCredentialsSecretRef, _ string, _ string) ([]byte, error) {
	return nil, fmt.Errorf("LoadSecretKey not implemented in mockSecretCredentialsHandler")
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
				TokenExchange: &api.TokenExchangeConfig{
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
				TokenExchange: &api.TokenExchangeConfig{
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
				TokenExchange: &api.TokenExchangeConfig{
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
				TokenExchange: &api.TokenExchangeConfig{
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
				TokenExchange: &api.TokenExchangeConfig{
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
				TokenExchange: &api.TokenExchangeConfig{
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
	sub := "test-user"
	musterIssuer := "https://dex.example.com"
	serverName := "test-server"
	fallbackToken := "original-token"

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
	validToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZXhwIjo5OTk5OTk5OTk5fQ.signature"
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
	validToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZXhwIjo5OTk5OTk5OTk5fQ.signature"
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

// TestHeaderFunc_ForwardsRequestBearer pins per-request forwarding: a request
// context carrying a validated bearer wins over the OAuth-store ID token and
// is forwarded byte-identical.
func TestHeaderFunc_ForwardsRequestBearer(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	sessionID := "test-session-bearer"
	musterIssuer := "https://muster.example.com"
	storeToken := unsignedJWT(t, map[string]any{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix()})
	mock := newMockOAuthHandler(true)
	mock.StoreToken(sessionID, "", musterIssuer, &api.OAuthToken{IDToken: storeToken})
	api.RegisterOAuthHandler(mock)
	defer api.RegisterOAuthHandler(nil)

	oboToken := unsignedJWT(t, map[string]any{
		"sub": "alice",
		"act": map[string]any{"sub": "system:serviceaccount:kagent:sre-agent"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	headerFunc := makeTokenForwardingHeaderFunc(sessionID, "alice", musterIssuer, "srv", "fallback", nil)

	ctx := server.ContextWithBearerToken(context.Background(), oboToken)
	headers := headerFunc(ctx)
	require.Equal(t, "Bearer "+oboToken, headers["Authorization"],
		"request bearer must be forwarded byte-identical, not the store token")

	// Without a request bearer (listen stream) the store token applies.
	headers = headerFunc(context.Background())
	require.Equal(t, "Bearer "+storeToken, headers["Authorization"])
}

// TestHeaderFunc_OpaqueBearerIgnored pins that a non-JWT bearer is never
// forwarded: opaque muster tokens cannot be validated by a backend, so the
// resolution falls through to the stored ID token.
func TestHeaderFunc_OpaqueBearerIgnored(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	sessionID := "test-session-opaque"
	musterIssuer := "https://muster.example.com"
	storeToken := unsignedJWT(t, map[string]any{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix()})
	mock := newMockOAuthHandler(true)
	mock.StoreToken(sessionID, "", musterIssuer, &api.OAuthToken{IDToken: storeToken})
	api.RegisterOAuthHandler(mock)
	defer api.RegisterOAuthHandler(nil)

	headerFunc := makeTokenForwardingHeaderFunc(sessionID, "alice", musterIssuer, "srv", "fallback", nil)

	ctx := server.ContextWithBearerToken(context.Background(), "opaque-access-token")
	headers := headerFunc(ctx)
	require.Equal(t, "Bearer "+storeToken, headers["Authorization"])
}

// TestHeaderFunc_ValidFallbackIsNotAFailure pins the failure accounting for
// sessions with no OAuth-store entry (agent OBO callers): as long as the
// connection token is an unexpired JWT, resolution succeeds without warnings
// or eviction.
func TestHeaderFunc_ValidFallbackIsNotAFailure(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	api.RegisterOAuthHandler(nil)
	defer api.RegisterOAuthHandler(nil)

	var evictCount atomic.Int32
	onStaleToken := func() { evictCount.Add(1) }

	fallback := unsignedJWT(t, map[string]any{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix()})
	headerFunc := makeTokenForwardingHeaderFunc("s", "alice", "https://muster.example.com", "srv", fallback, onStaleToken)

	logBuf.Reset()
	for i := 0; i < maxConsecutiveTokenFailures+2; i++ {
		headers := headerFunc(context.Background())
		require.Equal(t, "Bearer "+fallback, headers["Authorization"])
	}
	require.Equal(t, int32(0), evictCount.Load(),
		"an unexpired connection token must not count toward stale eviction")
	require.NotContains(t, logBuf.String(), "WARN")
}

// TestHeaderFunc_ExpiredFallbackEvicts pins that the stale-token state (no
// store token, connection token past expiry) still counts failures and evicts.
func TestHeaderFunc_ExpiredFallbackEvicts(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

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

	expired := unsignedJWT(t, map[string]any{"sub": "alice", "exp": time.Now().Add(-time.Hour).Unix()})
	headerFunc := makeTokenForwardingHeaderFunc("s", "alice", "https://muster.example.com", "srv", expired, onStaleToken)

	for i := 0; i < maxConsecutiveTokenFailures; i++ {
		headerFunc(context.Background())
	}
	firstEvict.Wait()
	require.Equal(t, int32(1), evictCount.Load())
}

func TestForwardableBearer(t *testing.T) {
	jwtToken := unsignedJWT(t, map[string]any{"sub": "alice"})
	tests := []struct {
		name   string
		bearer string
		want   string
	}{
		{"no bearer", "", ""},
		{"opaque bearer", "opaque-token", ""},
		{"jwt bearer", jwtToken, jwtToken},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.bearer != "" {
				ctx = server.ContextWithBearerToken(ctx, tc.bearer)
			}
			require.Equal(t, tc.want, forwardableBearer(ctx))
		})
	}
}

// unsignedJWT builds a header.payload.signature string whose payload is the
// given claims. EmailVerified parses it unverified, so no real signature is needed.
func unsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]any{"alg": "none", "typ": "JWT"}) + "." + enc(claims) + ".sig"
}

// mcp-go invokes a connection's headerFunc concurrently from the listener
// goroutine and from tool-call goroutines. This test hammers the closure from
// many goroutines so the race detector (make test / go test -race) catches any
// unsynchronised access to the failure counters, and asserts onStaleToken fires
// at most once despite the concurrency. Removing the mutex fails it.

func TestMakeTokenForwardingHeaderFunc_ConcurrentCalls(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	api.RegisterOAuthHandler(nil)
	defer api.RegisterOAuthHandler(nil)

	var evictCount atomic.Int32
	onStaleToken := func() { evictCount.Add(1) }

	// No OAuth handler → getIDTokenForForwarding returns "" → the fallback path
	// drives consecutiveFailures/staleEvicted/hadToken/lastWarnTime on every call.
	headerFunc := makeTokenForwardingHeaderFunc("session", "user", "https://dex.example.com", "server", "fallback-token", onStaleToken)

	const goroutines = 16
	const perGoroutine = 50
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				headers := headerFunc(context.Background())
				assert.Equal(t, "Bearer fallback-token", headers["Authorization"])
			}
		})
	}
	wg.Wait()

	// staleEvicted latches under the mutex, so the eviction goroutine is launched
	// at most once regardless of how many callers cross the threshold.
	assert.LessOrEqual(t, evictCount.Load(), int32(1), "onStaleToken must fire at most once")
}

func TestMakeTokenExchangeHeaderFunc_NoRefreshBeforeMargin(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	var calls atomic.Int32
	reexchange := func() (string, time.Time, error) {
		calls.Add(1)
		return "new-token", time.Now().Add(time.Hour), nil
	}

	// Token expires well beyond the refresh margin — no re-exchange expected.
	headerFunc := makeTokenExchangeHeaderFunc("srv", "initial-token", time.Now().Add(time.Hour), reexchange, nil)

	for i := 0; i < 3; i++ {
		headers := headerFunc(context.Background())
		assert.Equal(t, "Bearer initial-token", headers["Authorization"])
	}
	assert.Equal(t, int32(0), calls.Load(), "must not re-exchange before the refresh margin")
}

func TestMakeTokenExchangeHeaderFunc_RefreshesWithinMargin(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	var calls atomic.Int32
	reexchange := func() (string, time.Time, error) {
		calls.Add(1)
		return "refreshed-token", time.Now().Add(time.Hour), nil
	}

	// Token is already inside the refresh margin: first call re-exchanges, and the
	// new far-future expiry stops further re-exchanges.
	headerFunc := makeTokenExchangeHeaderFunc("srv", "initial-token", time.Now(), reexchange, nil)

	headers := headerFunc(context.Background())
	assert.Equal(t, "Bearer refreshed-token", headers["Authorization"], "should present the re-exchanged token")

	headers = headerFunc(context.Background())
	assert.Equal(t, "Bearer refreshed-token", headers["Authorization"])
	assert.Equal(t, int32(1), calls.Load(), "should re-exchange exactly once, then reuse the fresh token")
}

func TestMakeTokenExchangeHeaderFunc_EvictsAfterConsecutiveFailures(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	reexchange := func() (string, time.Time, error) {
		return "", time.Time{}, fmt.Errorf("subject ID token expired")
	}

	var evictCount atomic.Int32
	var firstEvict sync.WaitGroup
	firstEvict.Add(1)
	onStaleToken := func() {
		if evictCount.Add(1) == 1 {
			firstEvict.Done()
		}
	}

	headerFunc := makeTokenExchangeHeaderFunc("srv", "stale-token", time.Now(), reexchange, onStaleToken)

	for i := 0; i < maxConsecutiveTokenFailures-1; i++ {
		headers := headerFunc(context.Background())
		assert.Equal(t, "Bearer stale-token", headers["Authorization"], "must fall back to the current token while re-exchange fails")
	}
	assert.Equal(t, int32(0), evictCount.Load(), "must not evict before reaching the failure threshold")

	headers := headerFunc(context.Background())
	assert.Equal(t, "Bearer stale-token", headers["Authorization"])
	firstEvict.Wait()
	assert.Equal(t, int32(1), evictCount.Load(), "onStaleToken should fire exactly once at the threshold")

	// Further failures do not re-fire the callback within the same streak.
	headerFunc(context.Background())
	assert.Equal(t, int32(1), evictCount.Load())
}

func TestMakeTokenExchangeHeaderFunc_ResetsFailureCountOnRecovery(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	fail := true
	reexchange := func() (string, time.Time, error) {
		if fail {
			return "", time.Time{}, fmt.Errorf("transient failure")
		}
		return "recovered-token", time.Now().Add(time.Hour), nil
	}

	var evictCount atomic.Int32
	onStaleToken := func() { evictCount.Add(1) }

	headerFunc := makeTokenExchangeHeaderFunc("srv", "stale-token", time.Now(), reexchange, onStaleToken)

	for i := 0; i < maxConsecutiveTokenFailures-1; i++ {
		headerFunc(context.Background())
	}
	assert.Equal(t, int32(0), evictCount.Load())

	fail = false
	headers := headerFunc(context.Background())
	assert.Equal(t, "Bearer recovered-token", headers["Authorization"], "should use the recovered token")

	// After recovery the token expiry is far in the future, so no further
	// re-exchange (and no eviction) occurs.
	headers = headerFunc(context.Background())
	assert.Equal(t, "Bearer recovered-token", headers["Authorization"])
	assert.Equal(t, int32(0), evictCount.Load(), "failure counter must reset on recovery")
}

func TestMakeTokenExchangeHeaderFunc_NilCallbackNoPanic(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	reexchange := func() (string, time.Time, error) {
		return "", time.Time{}, fmt.Errorf("always fails")
	}
	headerFunc := makeTokenExchangeHeaderFunc("srv", "stale-token", time.Now(), reexchange, nil)

	for i := 0; i < maxConsecutiveTokenFailures+2; i++ {
		headers := headerFunc(context.Background())
		assert.Equal(t, "Bearer stale-token", headers["Authorization"])
	}
}

func TestMakeTokenExchangeHeaderFunc_ZeroExpiryRefreshesOnFallbackInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		reexchange := func() (string, time.Time, error) {
			calls.Add(1)
			// Fresh token also carries no parseable exp, so refresh must keep
			// firing on the fallback interval rather than latching off.
			return "refreshed-token", time.Time{}, nil
		}
		headerFunc := makeTokenExchangeHeaderFunc("srv", "initial-token", time.Time{}, reexchange, nil)

		// The initial token is fresh: a zero expiry is normalised to the fallback
		// window, not treated as already-expired, so no immediate re-exchange.
		headers := headerFunc(context.Background())
		assert.Equal(t, "Bearer initial-token", headers["Authorization"])
		assert.Equal(t, int32(0), calls.Load(), "must not re-exchange while inside the fallback window")

		// Cross the fallback refresh point (fallback window minus the margin).
		time.Sleep(tokenExchangeFallbackRefreshInterval + time.Second)
		headers = headerFunc(context.Background())
		assert.Equal(t, "Bearer refreshed-token", headers["Authorization"], "unparseable expiry must not disable refresh")
		assert.Equal(t, int32(1), calls.Load())

		// And it keeps refreshing on the interval rather than stopping after one.
		time.Sleep(tokenExchangeFallbackRefreshInterval + time.Second)
		headerFunc(context.Background())
		assert.Equal(t, int32(2), calls.Load(), "refresh must recur on the fallback interval")
	})
}

// makeTokenExchangeRefreshClosures backs the self-refreshing header func used by
// both the initial token-exchange connection and the tool-call / background-
// refresh path. The tool-call path previously built a static header, so a
// backend touched once then left idle re-stranded in Auth Required on token
// expiry; these tests pin the refresh (reexchange) and recovery (onStaleToken)
// behaviour the shared closures give both paths.

func TestMakeTokenExchangeRefreshClosures_ReexchangeMintsFromFreshSubject(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	subjectToken := unsignedJWT(t, map[string]any{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix()})
	var gotSubject, gotUserID string
	var gotConfig *api.TokenExchangeConfig
	mock := &issuerMockOAuthHandler{
		enabled: true,
		getFullTokenFunc: func(string, string) *api.OAuthToken {
			return &api.OAuthToken{IDToken: subjectToken}
		},
		exchangeFunc: func(_ context.Context, localToken, userID string, config *api.TokenExchangeConfig) (string, error) {
			gotSubject, gotUserID, gotConfig = localToken, userID, config
			return "fresh-backend-token", nil
		},
	}
	api.RegisterOAuthHandler(mock)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	a := &AggregatorServer{}
	config := &api.TokenExchangeConfig{ConnectorID: "dex-connector"}
	reexchange, _ := a.makeTokenExchangeRefreshClosures("srv", "sess-1", "fallback-user", "https://muster.example.com", mock, config)

	token, _, err := reexchange()
	require.NoError(t, err)
	require.Equal(t, "fresh-backend-token", token)
	require.Equal(t, subjectToken, gotSubject, "must mint from the freshly resolved subject ID token")
	require.Equal(t, "alice", gotUserID, "user ID comes from the fresh subject token's sub, not the fallback")
	require.Same(t, config, gotConfig, "must pass the resolved exchange config (creds + audience scopes) through")
}

func TestMakeTokenExchangeRefreshClosures_ReexchangeFailsWhenNoSubject(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	// enabled handler, but no stored token: the subject can no longer be resolved.
	mock := &issuerMockOAuthHandler{enabled: true}
	api.RegisterOAuthHandler(mock)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	a := &AggregatorServer{}
	reexchange, _ := a.makeTokenExchangeRefreshClosures("srv", "sess-1", "u", "https://muster.example.com", mock, &api.TokenExchangeConfig{})

	_, _, err := reexchange()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no subject ID token")
}

func TestMakeTokenExchangeRefreshClosures_OnStaleTokenEvictsAndRevokes(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	pool := NewSessionConnectionPool(time.Hour)
	t.Cleanup(pool.Stop)
	authStore := oauthstore.NewInMemorySessionAuthStore(time.Hour)
	a := &AggregatorServer{connPool: pool, authStore: authStore}

	ctx := t.Context()
	require.NoError(t, authStore.MarkAuthenticated(ctx, "sess-1", "srv"))
	pool.Put("sess-1", "srv", &poolTestClient{})

	_, onStaleToken := a.makeTokenExchangeRefreshClosures("srv", "sess-1", "u", "iss", &issuerMockOAuthHandler{}, &api.TokenExchangeConfig{})
	onStaleToken()

	_, ok := pool.Get("sess-1", "srv")
	require.False(t, ok, "stale connection must be evicted so the looping listener stops")

	authed, err := authStore.IsAuthenticated(ctx, "sess-1", "srv")
	require.NoError(t, err)
	require.False(t, authed, "stored auth must be revoked so the state settles to Auth Required")
}

// TestBuildConnectionTokenExchangeConfig_DoesNotMutateShared is a regression test
// for the MCPServer restart-churn bug (giantswarm/giantswarm#37060): the token
// exchange path must not mutate the shared registry definition's Auth pointer,
// because MCPServerReconciler.ConfigurationChanged compares it against the CR.
// Covers the requiredAudiences case, which the credential-only cases missed.
func TestBuildConnectionTokenExchangeConfig_DoesNotMutateShared(t *testing.T) {
	base := &api.TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: "https://dex.example/token",
		ConnectorID:      "giantswarm-simple-oidc",
		Scopes:           "openid profile email groups",
	}

	cfg, err := buildConnectionTokenExchangeConfig(
		base,
		[]string{"dex-k8s-authenticator"},
		"resolved-client-id",
		"resolved-client-secret",
	)
	require.NoError(t, err)

	// The returned copy carries the per-connection mutations...
	assert.Equal(t, "resolved-client-id", cfg.ClientID)
	assert.Equal(t, "resolved-client-secret", cfg.ClientSecret)
	assert.Contains(t, cfg.Scopes, "openid profile email groups")
	assert.Contains(t, cfg.Scopes, "dex-k8s-authenticator",
		"required audiences should be appended as cross-client scopes on the copy")

	// ...while the shared base is left completely untouched.
	assert.Empty(t, base.ClientID, "shared definition ClientID must not be mutated")
	assert.Empty(t, base.ClientSecret, "shared definition ClientSecret must not be mutated")
	assert.Equal(t, "openid profile email groups", base.Scopes,
		"shared definition Scopes must not gain audience scopes (would cause reconcile churn)")
}

// TestBuildConnectionTokenExchangeConfig_NoAudiences covers the plain
// tokenExchange case (no requiredAudiences): scopes are carried through unchanged
// and the shared base is not mutated.
func TestBuildConnectionTokenExchangeConfig_NoAudiences(t *testing.T) {
	base := &api.TokenExchangeConfig{
		Enabled: true,
		Scopes:  "openid profile",
	}

	cfg, err := buildConnectionTokenExchangeConfig(base, nil, "cid", "secret")
	require.NoError(t, err)

	assert.Equal(t, "openid profile", cfg.Scopes)
	assert.Equal(t, "cid", cfg.ClientID)
	assert.Empty(t, base.ClientID, "shared definition must not be mutated")
	assert.Equal(t, "openid profile", base.Scopes)
}
