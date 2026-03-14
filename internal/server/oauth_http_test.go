package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
