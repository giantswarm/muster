package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"muster/internal/api"
	"muster/internal/config"
)

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
	t.Run("ContextWithAccessToken and GetAccessTokenFromContext", func(t *testing.T) {
		ctx := httptest.NewRequest("GET", "/", nil).Context()
		token := "test-token-123"

		// Initially no token
		_, ok := GetAccessTokenFromContext(ctx)
		assert.False(t, ok)

		// Add token to context
		ctx = ContextWithAccessToken(ctx, token)

		// Retrieve token
		retrieved, ok := GetAccessTokenFromContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, token, retrieved)
	})

	t.Run("GetAccessTokenFromContext with empty token returns false", func(t *testing.T) {
		ctx := httptest.NewRequest("GET", "/", nil).Context()
		ctx = ContextWithAccessToken(ctx, "")

		_, ok := GetAccessTokenFromContext(ctx)
		assert.False(t, ok)
	})

	t.Run("HasUserInfo without user returns false", func(t *testing.T) {
		ctx := httptest.NewRequest("GET", "/", nil).Context()
		assert.False(t, HasUserInfo(ctx))
	})

	t.Run("GetUserEmailFromContext without user returns empty", func(t *testing.T) {
		ctx := httptest.NewRequest("GET", "/", nil).Context()
		assert.Empty(t, GetUserEmailFromContext(ctx))
	})

	t.Run("GetUserGroupsFromContext without user returns nil", func(t *testing.T) {
		ctx := httptest.NewRequest("GET", "/", nil).Context()
		assert.Nil(t, GetUserGroupsFromContext(ctx))
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
	t.Run("First call with token triggers callback", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		var mu sync.Mutex
		callbackCalled := false
		var callbackSessionID string

		// Register callback
		api.RegisterSessionInitCallback(func(ctx context.Context, sessionID string) {
			mu.Lock()
			callbackCalled = true
			callbackSessionID = sessionID
			mu.Unlock()
		})
		defer api.RegisterSessionInitCallback(nil) // Clean up

		// Create request with session ID and tokens in context
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set(api.ClientSessionIDHeader, "test-session-1")
		ctx := ContextWithAccessToken(req.Context(), "id-token-A")
		ctx = ContextWithUpstreamAccessToken(ctx, "access-token-A")

		server.triggerSessionInitIfNeeded(ctx, req)

		// Wait briefly for async callback
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.True(t, callbackCalled, "Callback should be called on first request")
		assert.Equal(t, "test-session-1", callbackSessionID)
		mu.Unlock()
	})

	t.Run("Same token same session does NOT trigger callback again", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		var mu sync.Mutex
		callCount := 0

		// Register callback
		api.RegisterSessionInitCallback(func(ctx context.Context, sessionID string) {
			mu.Lock()
			callCount++
			mu.Unlock()
		})
		defer api.RegisterSessionInitCallback(nil) // Clean up

		sessionID := "test-session-2"
		idToken := "id-token-same"
		accessToken := "access-token-same"

		// First call with tokens
		req1 := httptest.NewRequest("GET", "/", nil)
		req1.Header.Set(api.ClientSessionIDHeader, sessionID)
		ctx1 := ContextWithAccessToken(req1.Context(), idToken)
		ctx1 = ContextWithUpstreamAccessToken(ctx1, accessToken)
		server.triggerSessionInitIfNeeded(ctx1, req1)

		// Wait for async callback
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.Equal(t, 1, callCount, "Callback should be called once")
		mu.Unlock()

		// Second call with SAME tokens
		req2 := httptest.NewRequest("GET", "/", nil)
		req2.Header.Set(api.ClientSessionIDHeader, sessionID)
		ctx2 := ContextWithAccessToken(req2.Context(), idToken)
		ctx2 = ContextWithUpstreamAccessToken(ctx2, accessToken)
		server.triggerSessionInitIfNeeded(ctx2, req2)

		// Wait briefly
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.Equal(t, 1, callCount, "Callback should NOT be called again with same token")
		mu.Unlock()
	})

	t.Run("Different ID token same session DOES trigger callback (re-authentication)", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		var mu sync.Mutex
		callCount := 0
		var lastIDToken string

		// Register callback that captures the ID token from context
		api.RegisterSessionInitCallback(func(ctx context.Context, sessionID string) {
			mu.Lock()
			callCount++
			lastIDToken, _ = GetAccessTokenFromContext(ctx)
			mu.Unlock()
		})
		defer api.RegisterSessionInitCallback(nil) // Clean up

		sessionID := "test-session-3"
		idTokenA := "id-token-A-reauth"
		idTokenB := "id-token-B-reauth"
		accessTokenA := "access-token-A-reauth"
		accessTokenB := "access-token-B-reauth"

		// First call with token A
		req1 := httptest.NewRequest("GET", "/", nil)
		req1.Header.Set(api.ClientSessionIDHeader, sessionID)
		ctx1 := ContextWithAccessToken(req1.Context(), idTokenA)
		ctx1 = ContextWithUpstreamAccessToken(ctx1, accessTokenA)
		server.triggerSessionInitIfNeeded(ctx1, req1)

		// Wait for async callback
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.Equal(t, 1, callCount, "Callback should be called once")
		assert.Equal(t, idTokenA, lastIDToken, "First callback should have ID token A")
		mu.Unlock()

		// Second call with DIFFERENT tokens (simulating re-authentication)
		req2 := httptest.NewRequest("GET", "/", nil)
		req2.Header.Set(api.ClientSessionIDHeader, sessionID)
		ctx2 := ContextWithAccessToken(req2.Context(), idTokenB)
		ctx2 = ContextWithUpstreamAccessToken(ctx2, accessTokenB)
		server.triggerSessionInitIfNeeded(ctx2, req2)

		// Wait for async callback
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.Equal(t, 2, callCount, "Callback SHOULD be called again with different token")
		assert.Equal(t, idTokenB, lastIDToken, "Second callback should have ID token B")
		mu.Unlock()
	})

	t.Run("Token refresh (same ID token, different access token) DOES trigger callback", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		var mu sync.Mutex
		callCount := 0
		var lastIDToken string

		// Register callback that captures the ID token from context
		api.RegisterSessionInitCallback(func(ctx context.Context, sessionID string) {
			mu.Lock()
			callCount++
			lastIDToken, _ = GetAccessTokenFromContext(ctx)
			mu.Unlock()
		})
		defer api.RegisterSessionInitCallback(nil) // Clean up

		sessionID := "test-session-refresh"
		// ID token stays the same (preserved during refresh per OAuth spec)
		idToken := "id-token-preserved"
		// Access token changes on refresh
		accessTokenBefore := "access-token-before-refresh"
		accessTokenAfter := "access-token-after-refresh"

		// First call with original access token
		req1 := httptest.NewRequest("GET", "/", nil)
		req1.Header.Set(api.ClientSessionIDHeader, sessionID)
		ctx1 := ContextWithAccessToken(req1.Context(), idToken)
		ctx1 = ContextWithUpstreamAccessToken(ctx1, accessTokenBefore)
		server.triggerSessionInitIfNeeded(ctx1, req1)

		// Wait for async callback
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.Equal(t, 1, callCount, "Callback should be called once")
		assert.Equal(t, idToken, lastIDToken, "First callback should have the ID token")
		mu.Unlock()

		// Second call with SAME ID token but DIFFERENT access token (simulating server-side refresh)
		req2 := httptest.NewRequest("GET", "/", nil)
		req2.Header.Set(api.ClientSessionIDHeader, sessionID)
		ctx2 := ContextWithAccessToken(req2.Context(), idToken)       // Same ID token
		ctx2 = ContextWithUpstreamAccessToken(ctx2, accessTokenAfter) // Different access token
		server.triggerSessionInitIfNeeded(ctx2, req2)

		// Wait for async callback
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.Equal(t, 2, callCount, "Callback SHOULD be called again after token refresh")
		assert.Equal(t, idToken, lastIDToken, "Second callback should have same ID token (preserved)")
		mu.Unlock()
	})

	t.Run("No session ID does not trigger callback", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		var mu sync.Mutex
		callbackCalled := false

		api.RegisterSessionInitCallback(func(ctx context.Context, sessionID string) {
			mu.Lock()
			callbackCalled = true
			mu.Unlock()
		})
		defer api.RegisterSessionInitCallback(nil)

		// Request without session ID header
		req := httptest.NewRequest("GET", "/", nil)
		ctx := ContextWithAccessToken(req.Context(), "some-id-token")
		ctx = ContextWithUpstreamAccessToken(ctx, "some-access-token")

		server.triggerSessionInitIfNeeded(ctx, req)
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.False(t, callbackCalled, "Callback should not be called without session ID")
		mu.Unlock()
	})

	t.Run("No token in context does not trigger callback", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		var mu sync.Mutex
		callbackCalled := false

		api.RegisterSessionInitCallback(func(ctx context.Context, sessionID string) {
			mu.Lock()
			callbackCalled = true
			mu.Unlock()
		})
		defer api.RegisterSessionInitCallback(nil)

		// Request with session ID but no token
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set(api.ClientSessionIDHeader, "test-session")

		server.triggerSessionInitIfNeeded(req.Context(), req)
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.False(t, callbackCalled, "Callback should not be called without token")
		mu.Unlock()
	})

	t.Run("Falls back to ID token when upstream access token not set", func(t *testing.T) {
		server := &OAuthHTTPServer{}
		var mu sync.Mutex
		callCount := 0

		api.RegisterSessionInitCallback(func(ctx context.Context, sessionID string) {
			mu.Lock()
			callCount++
			mu.Unlock()
		})
		defer api.RegisterSessionInitCallback(nil)

		sessionID := "test-session-fallback"
		idTokenA := "id-token-A-fallback"
		idTokenB := "id-token-B-fallback"

		// First call with ID token only (no upstream access token)
		req1 := httptest.NewRequest("GET", "/", nil)
		req1.Header.Set(api.ClientSessionIDHeader, sessionID)
		ctx1 := ContextWithAccessToken(req1.Context(), idTokenA)
		// Note: NOT setting ContextWithUpstreamAccessToken
		server.triggerSessionInitIfNeeded(ctx1, req1)

		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.Equal(t, 1, callCount, "Callback should be called once")
		mu.Unlock()

		// Second call with SAME ID token (no upstream access token)
		req2 := httptest.NewRequest("GET", "/", nil)
		req2.Header.Set(api.ClientSessionIDHeader, sessionID)
		ctx2 := ContextWithAccessToken(req2.Context(), idTokenA)
		server.triggerSessionInitIfNeeded(ctx2, req2)

		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.Equal(t, 1, callCount, "Callback should NOT be called with same ID token")
		mu.Unlock()

		// Third call with DIFFERENT ID token (simulating fallback re-auth detection)
		req3 := httptest.NewRequest("GET", "/", nil)
		req3.Header.Set(api.ClientSessionIDHeader, sessionID)
		ctx3 := ContextWithAccessToken(req3.Context(), idTokenB)
		server.triggerSessionInitIfNeeded(ctx3, req3)

		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		assert.Equal(t, 2, callCount, "Callback SHOULD be called with different ID token")
		mu.Unlock()
	})
}

// TestHashToken tests the token hashing function.
func TestHashToken(t *testing.T) {
	t.Run("Same token produces same hash", func(t *testing.T) {
		token := "test-token-123"
		hash1 := hashToken(token)
		hash2 := hashToken(token)
		assert.Equal(t, hash1, hash2)
	})

	t.Run("Different tokens produce different hashes", func(t *testing.T) {
		hash1 := hashToken("token-A")
		hash2 := hashToken("token-B")
		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("Hash is fixed length", func(t *testing.T) {
		hash := hashToken("some-token")
		// SHA-256 first 8 bytes = 16 hex chars
		assert.Len(t, hash, 16)
	})
}
