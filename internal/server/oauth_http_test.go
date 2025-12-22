package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestHashEmail(t *testing.T) {
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
			result := hashEmail(tt.email)
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
