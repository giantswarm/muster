package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
)

// mockMCPServerManager implements api.MCPServerManagerHandler for testing.
type mockMCPServerManager struct {
	listMCPServersFn func() []api.MCPServerInfo
	getMCPServerFn   func(name string) (*api.MCPServerInfo, error)
	getToolsFn       func() []api.ToolMetadata
	executeToolFn    func(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error)
}

func (m *mockMCPServerManager) ListMCPServers() []api.MCPServerInfo {
	if m.listMCPServersFn != nil {
		return m.listMCPServersFn()
	}
	return nil
}

func (m *mockMCPServerManager) GetMCPServer(name string) (*api.MCPServerInfo, error) {
	if m.getMCPServerFn != nil {
		return m.getMCPServerFn(name)
	}
	return nil, nil
}

func (m *mockMCPServerManager) GetTools() []api.ToolMetadata {
	if m.getToolsFn != nil {
		return m.getToolsFn()
	}
	return nil
}

func (m *mockMCPServerManager) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	if m.executeToolFn != nil {
		return m.executeToolFn(ctx, toolName, args)
	}
	return nil, nil
}

func TestBuildDexScopes(t *testing.T) {
	tests := []struct {
		name              string
		requiredAudiences []string
		expectedScopes    []string
	}{
		{
			name:              "no required audiences returns base scopes only",
			requiredAudiences: nil,
			expectedScopes:    []string{"openid", "profile", "email", "groups", "offline_access"},
		},
		{
			name:              "empty required audiences returns base scopes only",
			requiredAudiences: []string{},
			expectedScopes:    []string{"openid", "profile", "email", "groups", "offline_access"},
		},
		{
			name:              "single required audience adds cross-client scope",
			requiredAudiences: []string{"dex-k8s-authenticator"},
			expectedScopes: []string{
				"openid", "profile", "email", "groups", "offline_access",
				"audience:server:client_id:dex-k8s-authenticator",
			},
		},
		{
			name:              "multiple required audiences adds multiple cross-client scopes",
			requiredAudiences: []string{"audience-a", "audience-b"},
			expectedScopes: []string{
				"openid", "profile", "email", "groups", "offline_access",
				"audience:server:client_id:audience-a",
				"audience:server:client_id:audience-b",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildDexScopes(tt.requiredAudiences)
			assert.Equal(t, tt.expectedScopes, result)
		})
	}
}

func TestBuildDexScopesDoesNotModifyBaseScopes(t *testing.T) {
	// Verify that calling buildDexScopes doesn't modify the global dexOAuthScopes
	originalLen := len(dexOAuthScopes)

	// Call with audiences
	_ = buildDexScopes([]string{"test-audience"})

	// Verify base scopes unchanged
	assert.Equal(t, originalLen, len(dexOAuthScopes), "dexOAuthScopes should not be modified")
	assert.Equal(t, []string{"openid", "profile", "email", "groups", "offline_access"}, dexOAuthScopes)
}

// TestCollectRequiredAudiencesIntegrationWithBuildDexScopes tests the full integration
// between MCPServer requiredAudiences configuration and OAuth scope building.
func TestCollectRequiredAudiencesIntegrationWithBuildDexScopes(t *testing.T) {
	// This test verifies that when MCPServers with requiredAudiences are registered,
	// the CollectRequiredAudiences function returns them, and buildDexScopes
	// correctly formats them as cross-client scopes for Dex.

	t.Run("MCPServers with requiredAudiences are included in Dex scopes", func(t *testing.T) {
		// Register a mock MCPServer manager with servers that have requiredAudiences
		api.RegisterMCPServerManager(&mockMCPServerManager{
			listMCPServersFn: func() []api.MCPServerInfo {
				return []api.MCPServerInfo{
					{
						Name: "kubernetes-mcp",
						Auth: &api.MCPServerAuth{
							ForwardToken:      true,
							RequiredAudiences: []string{"dex-k8s-authenticator"},
						},
					},
					{
						Name: "another-mcp",
						Auth: &api.MCPServerAuth{
							ForwardToken:      true,
							RequiredAudiences: []string{"another-client"},
						},
					},
				}
			},
		})
		defer api.RegisterMCPServerManager(nil)

		// Collect audiences (simulates what createOAuthServer does)
		audiences := api.CollectRequiredAudiences()

		// Build Dex scopes (simulates what createOAuthServer does)
		scopes := buildDexScopes(audiences)

		// Verify the cross-client scopes are present
		assert.Contains(t, scopes, "audience:server:client_id:another-client")
		assert.Contains(t, scopes, "audience:server:client_id:dex-k8s-authenticator")

		// Verify base scopes are still present
		assert.Contains(t, scopes, "openid")
		assert.Contains(t, scopes, "profile")
		assert.Contains(t, scopes, "email")
		assert.Contains(t, scopes, "groups")
		assert.Contains(t, scopes, "offline_access")
	})

	t.Run("MCPServers without forwardToken do not add scopes", func(t *testing.T) {
		api.RegisterMCPServerManager(&mockMCPServerManager{
			listMCPServersFn: func() []api.MCPServerInfo {
				return []api.MCPServerInfo{
					{
						Name: "regular-mcp",
						Auth: &api.MCPServerAuth{
							ForwardToken:      false, // Not forwarding tokens
							RequiredAudiences: []string{"should-be-ignored"},
						},
					},
				}
			},
		})
		defer api.RegisterMCPServerManager(nil)

		audiences := api.CollectRequiredAudiences()
		scopes := buildDexScopes(audiences)

		// Should only have base scopes, no cross-client scopes
		assert.Equal(t, []string{"openid", "profile", "email", "groups", "offline_access"}, scopes)
	})

	t.Run("Invalid audiences are filtered by CollectRequiredAudiences before reaching buildDexScopes", func(t *testing.T) {
		// Invalid audiences (containing whitespace) are filtered at the API layer
		// by CollectRequiredAudiences's isValidAudience check. This provides
		// defense-in-depth: API validation + mcp-oauth library validation.
		api.RegisterMCPServerManager(&mockMCPServerManager{
			listMCPServersFn: func() []api.MCPServerInfo {
				return []api.MCPServerInfo{
					{
						Name: "mcp-with-invalid-audiences",
						Auth: &api.MCPServerAuth{
							ForwardToken: true,
							RequiredAudiences: []string{
								"valid-audience",
								"invalid audience", // space makes it invalid - filtered by API layer
								"",                 // empty string - filtered by API layer
								"another-valid",
							},
						},
					},
				}
			},
		})
		defer api.RegisterMCPServerManager(nil)

		audiences := api.CollectRequiredAudiences()
		scopes := buildDexScopes(audiences)

		// Only valid audiences should be included (invalid ones filtered by API layer)
		assert.Contains(t, scopes, "audience:server:client_id:another-valid")
		assert.Contains(t, scopes, "audience:server:client_id:valid-audience")

		// Invalid audiences should NOT be included
		assert.NotContains(t, scopes, "audience:server:client_id:invalid audience")
		assert.NotContains(t, scopes, "audience:server:client_id:")

		// Base scopes still present
		assert.Contains(t, scopes, "openid")
	})
}

func TestNewOAuthHTTPServer_DisabledReturnsError(t *testing.T) {
	cfg := config.OAuthServerConfig{
		Enabled: false,
	}

	_, err := NewOAuthHTTPServer(cfg, http.DefaultServeMux, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not enabled")
}

func TestNewOAuthHTTPServer_InvalidProviderReturnsError(t *testing.T) {
	cfg := config.OAuthServerConfig{
		Enabled:  true,
		BaseURL:  "http://localhost:8080",
		Provider: "unsupported",
	}

	_, err := NewOAuthHTTPServer(cfg, http.DefaultServeMux, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported OAuth provider")
}

func TestNewOAuthHTTPServer_MissingDexIssuerReturnsError(t *testing.T) {
	cfg := config.OAuthServerConfig{
		Enabled:  true,
		BaseURL:  "http://localhost:8080",
		Provider: OAuthProviderDex,
		Dex: config.DexConfig{
			// IssuerURL is required but empty
			ClientID:     "test-client",
			ClientSecret: "test-secret",
		},
	}

	_, err := NewOAuthHTTPServer(cfg, http.DefaultServeMux, false)
	assert.Error(t, err)
	// The Dex provider will fail to initialize without issuer URL
}

func TestValidateHTTPSRequirement(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		wantErr bool
	}{
		{
			name:    "HTTPS URL is valid",
			baseURL: "https://muster.example.com",
			wantErr: false,
		},
		{
			name:    "HTTP localhost is valid",
			baseURL: "http://localhost:8080",
			wantErr: false,
		},
		{
			name:    "HTTP 127.0.0.1 is valid",
			baseURL: "http://127.0.0.1:8080",
			wantErr: false,
		},
		{
			name:    "HTTP ::1 is valid",
			baseURL: "http://[::1]:8080",
			wantErr: false,
		},
		{
			name:    "HTTP on non-loopback is invalid",
			baseURL: "http://muster.example.com",
			wantErr: true,
		},
		{
			name:    "Empty URL is invalid",
			baseURL: "",
			wantErr: true,
		},
		{
			name:    "Invalid scheme is invalid",
			baseURL: "ftp://example.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHTTPSRequirement(tt.baseURL)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTruncateEmail(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		expected string
	}{
		{
			name:     "Long email returns first 8 chars",
			email:    "test@example.com",
			expected: "test@exa",
		},
		{
			name:     "Short email returns as-is",
			email:    "short",
			expected: "short",
		},
		{
			name:     "Exactly 8 chars returns as-is",
			email:    "12345678",
			expected: "12345678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateEmail(tt.email)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTokenProviderContextFunctions(t *testing.T) {
	t.Run("ContextWithIDToken and GetIDTokenFromContext", func(t *testing.T) {
		ctx := httptest.NewRequest("GET", "/", nil).Context()
		token := "test-token-123"

		// Initially no token
		_, ok := GetIDTokenFromContext(ctx)
		assert.False(t, ok)

		// Add token to context
		ctx = ContextWithIDToken(ctx, token)

		// Retrieve token
		retrieved, ok := GetIDTokenFromContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, token, retrieved)
	})

	t.Run("GetIDTokenFromContext with empty token returns false", func(t *testing.T) {
		ctx := httptest.NewRequest("GET", "/", nil).Context()
		ctx = ContextWithIDToken(ctx, "")

		_, ok := GetIDTokenFromContext(ctx)
		assert.False(t, ok)
	})

}

func TestGetIDToken(t *testing.T) {
	t.Run("Nil token returns empty", func(t *testing.T) {
		result := GetIDToken(nil)
		assert.Empty(t, result)
	})

	// Note: Testing with actual oauth2.Token requires mocking Extra() which
	// is not straightforward since it reads from internal fields
}

func TestOAuthHTTPServerCreateMux(t *testing.T) {
	// Skip this test if we can't create a valid OAuth server
	// This test verifies that CreateMux returns a valid handler
	t.Skip("Requires valid Dex/Google provider configuration to test")
}

// TestOAuthHTTPServerEndpoints verifies that OAuth endpoints are registered correctly.
// This is an integration test that requires a valid OAuth server configuration.
func TestOAuthHTTPServerEndpoints(t *testing.T) {
	t.Skip("Requires valid Dex/Google provider configuration to test")
}

// TestMCPEndpointProtection verifies that MCP endpoints require authentication.
func TestMCPEndpointProtection(t *testing.T) {
	// This test would require mocking the mcp-oauth library which is complex
	// The actual protection is tested via scenario tests
	t.Skip("MCP endpoint protection is tested via scenario tests")
}

// TestCreateAccessTokenInjectorMiddleware tests the middleware that injects tokens
// into the request context for downstream authentication.
func TestCreateAccessTokenInjectorMiddleware(t *testing.T) {
	t.Run("Middleware passes request without user info", func(t *testing.T) {
		// Create a simple handler that just returns 200
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})

		// We can't easily test the middleware without a valid OAuth server
		// but we can verify the pattern works
		require.NotNil(t, next)
		assert.False(t, called) // Not called yet
	})
}

// TestTriggerSessionInitIfNeeded tests the proactive SSO triggering logic.
// This tests proactive SSO triggering for:
// - First authentication (new session)
// - Re-authentication (logout + login with new ID token)
// - Token refresh (new access token, preserved ID token)
func TestTriggerSessionInitIfNeeded(t *testing.T) {
	// testTimeout is the maximum time to wait for async callbacks.
	// This is used in select statements to fail fast if callbacks don't arrive.
	const testTimeout = 5 * time.Second

	t.Run("First call with token triggers callback", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		callbackDone := make(chan string, 1)

		// Register callback that signals completion via channel
		api.RegisterSessionInitCallback(func(ctx context.Context, sub string) {
			callbackDone <- sub
		})
		defer api.RegisterSessionInitCallback(nil)

		// Create request with user subject and tokens in context
		req := httptest.NewRequest("GET", "/", nil)
		ctx := ContextWithIDToken(req.Context(), "id-token-A")
		ctx = ContextWithUpstreamAccessToken(ctx, "access-token-A")
		ctx = api.WithSubject(ctx, "test-user-1")
		ctx = api.WithSessionID(ctx, "session-first")

		server.triggerSessionInitIfNeeded(ctx, req)

		// Wait for callback with timeout
		select {
		case sub := <-callbackDone:
			assert.Equal(t, "test-user-1", sub)
		case <-time.After(testTimeout):
			t.Fatal("Callback was not called within timeout")
		}
	})

	t.Run("Same token same user does NOT trigger callback again", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		callbackDone := make(chan struct{}, 2)

		// Register callback that signals each invocation
		api.RegisterSessionInitCallback(func(ctx context.Context, sub string) {
			callbackDone <- struct{}{}
		})
		defer api.RegisterSessionInitCallback(nil)

		sub := "test-user-2"
		idToken := "id-token-same"
		accessToken := "access-token-same"
		sessionID := "session-same-token"

		// First call with tokens - should trigger callback
		req1 := httptest.NewRequest("GET", "/", nil)
		ctx1 := ContextWithIDToken(req1.Context(), idToken)
		ctx1 = ContextWithUpstreamAccessToken(ctx1, accessToken)
		ctx1 = api.WithSubject(ctx1, sub)
		ctx1 = api.WithSessionID(ctx1, sessionID)
		server.triggerSessionInitIfNeeded(ctx1, req1)

		// Wait for first callback
		select {
		case <-callbackDone:
			// Expected
		case <-time.After(testTimeout):
			t.Fatal("First callback was not called within timeout")
		}

		// Second call with SAME tokens and SAME session ID - should NOT trigger callback
		req2 := httptest.NewRequest("GET", "/", nil)
		ctx2 := ContextWithIDToken(req2.Context(), idToken)
		ctx2 = ContextWithUpstreamAccessToken(ctx2, accessToken)
		ctx2 = api.WithSubject(ctx2, sub)
		ctx2 = api.WithSessionID(ctx2, sessionID)
		server.triggerSessionInitIfNeeded(ctx2, req2)

		// Verify no second callback arrives (short timeout since we're proving a negative)
		select {
		case <-callbackDone:
			t.Fatal("Callback should NOT be called again with same token")
		case <-time.After(100 * time.Millisecond):
			// Expected - no callback for same token
		}
	})

	t.Run("Different ID token same user DOES trigger callback (re-authentication)", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		type callbackResult struct {
			idToken string
		}
		callbackDone := make(chan callbackResult, 2)

		// Register callback that captures the ID token from context
		api.RegisterSessionInitCallback(func(ctx context.Context, sub string) {
			idToken, _ := GetIDTokenFromContext(ctx)
			callbackDone <- callbackResult{idToken: idToken}
		})
		defer api.RegisterSessionInitCallback(nil)

		sub := "test-user-3"
		idTokenA := "id-token-A-reauth"
		idTokenB := "id-token-B-reauth"
		accessTokenA := "access-token-A-reauth"
		accessTokenB := "access-token-B-reauth"

		// First call with token A
		req1 := httptest.NewRequest("GET", "/", nil)
		ctx1 := ContextWithIDToken(req1.Context(), idTokenA)
		ctx1 = ContextWithUpstreamAccessToken(ctx1, accessTokenA)
		ctx1 = api.WithSubject(ctx1, sub)
		ctx1 = api.WithSessionID(ctx1, "session-reauth-1")
		server.triggerSessionInitIfNeeded(ctx1, req1)

		// Wait for first callback
		select {
		case result := <-callbackDone:
			assert.Equal(t, idTokenA, result.idToken, "First callback should have ID token A")
		case <-time.After(testTimeout):
			t.Fatal("First callback was not called within timeout")
		}

		// Second call with DIFFERENT tokens and DIFFERENT session ID (simulating re-authentication)
		req2 := httptest.NewRequest("GET", "/", nil)
		ctx2 := ContextWithIDToken(req2.Context(), idTokenB)
		ctx2 = ContextWithUpstreamAccessToken(ctx2, accessTokenB)
		ctx2 = api.WithSubject(ctx2, sub)
		ctx2 = api.WithSessionID(ctx2, "session-reauth-2")
		server.triggerSessionInitIfNeeded(ctx2, req2)

		// Wait for second callback
		select {
		case result := <-callbackDone:
			assert.Equal(t, idTokenB, result.idToken, "Second callback should have ID token B")
		case <-time.After(testTimeout):
			t.Fatal("Second callback was not called within timeout (re-auth should trigger)")
		}
	})

	t.Run("Token refresh (same ID token, different access token) DOES trigger callback", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		type callbackResult struct {
			idToken string
		}
		callbackDone := make(chan callbackResult, 2)

		// Register callback that captures the ID token from context
		api.RegisterSessionInitCallback(func(ctx context.Context, sub string) {
			idToken, _ := GetIDTokenFromContext(ctx)
			callbackDone <- callbackResult{idToken: idToken}
		})
		defer api.RegisterSessionInitCallback(nil)

		sub := "test-user-refresh"
		// ID token stays the same (preserved during refresh per OAuth spec)
		idToken := "id-token-preserved"
		// Access token changes on refresh
		accessTokenBefore := "access-token-before-refresh"
		accessTokenAfter := "access-token-after-refresh"

		// First call with original access token
		req1 := httptest.NewRequest("GET", "/", nil)
		ctx1 := ContextWithIDToken(req1.Context(), idToken)
		ctx1 = ContextWithUpstreamAccessToken(ctx1, accessTokenBefore)
		ctx1 = api.WithSubject(ctx1, sub)
		ctx1 = api.WithSessionID(ctx1, "session-refresh-1")
		server.triggerSessionInitIfNeeded(ctx1, req1)

		// Wait for first callback
		select {
		case result := <-callbackDone:
			assert.Equal(t, idToken, result.idToken, "First callback should have the ID token")
		case <-time.After(testTimeout):
			t.Fatal("First callback was not called within timeout")
		}

		// Second call with SAME ID token but DIFFERENT access token and DIFFERENT session ID (simulating server-side refresh)
		req2 := httptest.NewRequest("GET", "/", nil)
		ctx2 := ContextWithIDToken(req2.Context(), idToken)           // Same ID token
		ctx2 = ContextWithUpstreamAccessToken(ctx2, accessTokenAfter) // Different access token
		ctx2 = api.WithSubject(ctx2, sub)
		ctx2 = api.WithSessionID(ctx2, "session-refresh-2")
		server.triggerSessionInitIfNeeded(ctx2, req2)

		// Wait for second callback
		select {
		case result := <-callbackDone:
			assert.Equal(t, idToken, result.idToken, "Second callback should have same ID token (preserved)")
		case <-time.After(testTimeout):
			t.Fatal("Second callback was not called within timeout (refresh should trigger)")
		}
	})

	t.Run("No user subject does not trigger callback", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		callbackCalled := make(chan struct{}, 1)

		api.RegisterSessionInitCallback(func(ctx context.Context, sub string) {
			callbackCalled <- struct{}{}
		})
		defer api.RegisterSessionInitCallback(nil)

		// Request without user subject in context
		req := httptest.NewRequest("GET", "/", nil)
		ctx := ContextWithIDToken(req.Context(), "some-id-token")
		ctx = ContextWithUpstreamAccessToken(ctx, "some-access-token")

		// This returns early (no goroutine launched) when subject is missing
		server.triggerSessionInitIfNeeded(ctx, req)

		// Verify callback is not called (short timeout since code returns synchronously)
		select {
		case <-callbackCalled:
			t.Fatal("Callback should not be called without user subject")
		case <-time.After(100 * time.Millisecond):
			// Expected - no callback
		}
	})

	t.Run("No token in context does not trigger callback", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		callbackCalled := make(chan struct{}, 1)

		api.RegisterSessionInitCallback(func(ctx context.Context, sub string) {
			callbackCalled <- struct{}{}
		})
		defer api.RegisterSessionInitCallback(nil)

		// Request with user subject but no token
		req := httptest.NewRequest("GET", "/", nil)
		ctx := api.WithSubject(req.Context(), "test-user")

		// This returns early (no goroutine launched) when token is missing
		server.triggerSessionInitIfNeeded(ctx, req)

		// Verify callback is not called (short timeout since code returns synchronously)
		select {
		case <-callbackCalled:
			t.Fatal("Callback should not be called without token")
		case <-time.After(100 * time.Millisecond):
			// Expected - no callback
		}
	})

	t.Run("Falls back to ID token when upstream access token not set", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		callbackDone := make(chan struct{}, 3)

		api.RegisterSessionInitCallback(func(ctx context.Context, sub string) {
			callbackDone <- struct{}{}
		})
		defer api.RegisterSessionInitCallback(nil)

		sub := "test-user-fallback"
		idTokenA := "id-token-A-fallback"
		idTokenB := "id-token-B-fallback"
		sessionIDSame := "session-fallback-same"
		sessionIDDifferent := "session-fallback-different"

		// First call with ID token only (no upstream access token) - should trigger
		req1 := httptest.NewRequest("GET", "/", nil)
		ctx1 := ContextWithIDToken(req1.Context(), idTokenA)
		ctx1 = api.WithSubject(ctx1, sub)
		ctx1 = api.WithSessionID(ctx1, sessionIDSame)
		// Note: NOT setting ContextWithUpstreamAccessToken
		server.triggerSessionInitIfNeeded(ctx1, req1)

		select {
		case <-callbackDone:
			// Expected
		case <-time.After(testTimeout):
			t.Fatal("First callback was not called within timeout")
		}

		// Second call with SAME ID token and SAME session ID (no upstream access token) - should NOT trigger
		req2 := httptest.NewRequest("GET", "/", nil)
		ctx2 := ContextWithIDToken(req2.Context(), idTokenA)
		ctx2 = api.WithSubject(ctx2, sub)
		ctx2 = api.WithSessionID(ctx2, sessionIDSame)
		server.triggerSessionInitIfNeeded(ctx2, req2)

		select {
		case <-callbackDone:
			t.Fatal("Callback should NOT be called with same ID token")
		case <-time.After(100 * time.Millisecond):
			// Expected - no callback for same token
		}

		// Third call with DIFFERENT ID token and DIFFERENT session ID - should trigger
		req3 := httptest.NewRequest("GET", "/", nil)
		ctx3 := ContextWithIDToken(req3.Context(), idTokenB)
		ctx3 = api.WithSubject(ctx3, sub)
		ctx3 = api.WithSessionID(ctx3, sessionIDDifferent)
		server.triggerSessionInitIfNeeded(ctx3, req3)

		select {
		case <-callbackDone:
			// Expected
		case <-time.After(testTimeout):
			t.Fatal("Third callback was not called within timeout (different ID token should trigger)")
		}
	})
}

// TestCleanupExpiredSessions tests the session tracker cleanup logic.
func TestCleanupExpiredSessions(t *testing.T) {
	t.Run("Removes expired sessions", func(t *testing.T) {
		server := &OAuthHTTPServer{}

		// Add an expired session (last accessed more than TTL ago)
		expiredEntry := sessionTrackerEntry{
			lastAccess: time.Now().Add(-DefaultSessionTrackerTTL - time.Hour),
		}
		server.sessionInitTracker.Store("expired-session", expiredEntry)

		// Add a fresh session (just accessed)
		freshEntry := sessionTrackerEntry{
			lastAccess: time.Now(),
		}
		server.sessionInitTracker.Store("fresh-session", freshEntry)

		// Run cleanup
		server.cleanupExpiredSessions()

		// Verify expired session was removed
		_, exists := server.sessionInitTracker.Load("expired-session")
		assert.False(t, exists, "Expired session should be removed")

		// Verify fresh session still exists
		_, exists = server.sessionInitTracker.Load("fresh-session")
		assert.True(t, exists, "Fresh session should still exist")
	})

	t.Run("Does not remove sessions within TTL", func(t *testing.T) {
		server := &OAuthHTTPServer{}

		// Add a session that's just under the TTL
		almostExpiredEntry := sessionTrackerEntry{
			lastAccess: time.Now().Add(-DefaultSessionTrackerTTL + time.Minute),
		}
		server.sessionInitTracker.Store("almost-expired-session", almostExpiredEntry)

		// Run cleanup
		server.cleanupExpiredSessions()

		// Verify session still exists
		_, exists := server.sessionInitTracker.Load("almost-expired-session")
		assert.True(t, exists, "Session within TTL should not be removed")
	})

	t.Run("Handles empty tracker gracefully", func(t *testing.T) {
		server := &OAuthHTTPServer{}

		// Run cleanup on empty tracker - should not panic
		server.cleanupExpiredSessions()

		// Count entries (should be zero)
		count := 0
		server.sessionInitTracker.Range(func(_, _ interface{}) bool {
			count++
			return true
		})
		assert.Equal(t, 0, count, "Empty tracker should remain empty")
	})
}

// TestSessionTrackerCleanupGoroutine tests that the cleanup goroutine starts and stops correctly.
func TestSessionTrackerCleanupGoroutine(t *testing.T) {
	t.Run("Cleanup goroutine stops on channel close", func(t *testing.T) {
		server := &OAuthHTTPServer{
			stopCleanup: make(chan struct{}),
		}

		// Start the cleanup goroutine
		done := make(chan struct{})
		go func() {
			server.runSessionTrackerCleanup()
			close(done)
		}()

		// Stop it by closing the channel
		close(server.stopCleanup)

		// Wait for goroutine to finish
		select {
		case <-done:
			// Expected - goroutine stopped
		case <-time.After(time.Second):
			t.Fatal("Cleanup goroutine did not stop within timeout")
		}
	})
}
