package oauth

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"muster/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTokenExchanger(t *testing.T) {
	t.Run("creates exchanger with default options", func(t *testing.T) {
		exchanger := NewTokenExchanger()
		require.NotNil(t, exchanger)
		assert.NotNil(t, exchanger.client)
		assert.NotNil(t, exchanger.cache)
		assert.False(t, exchanger.allowPrivateIP)
	})

	t.Run("creates exchanger with custom options", func(t *testing.T) {
		exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{
			AllowPrivateIP:  true,
			CacheMaxEntries: 1000,
		})
		require.NotNil(t, exchanger)
		assert.True(t, exchanger.allowPrivateIP)
	})
}

func TestTokenExchanger_Exchange_Validation(t *testing.T) {
	exchanger := NewTokenExchanger()

	t.Run("returns error for nil request", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exchange request is nil")
	})

	t.Run("returns error for nil config", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token exchange config is nil")
	})

	t.Run("returns error when not enabled", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &api.TokenExchangeConfig{
				Enabled: false,
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token exchange is not enabled")
	})

	t.Run("returns error for missing subject token", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.example.com/token",
				ConnectorID:      "local-dex",
			},
			SubjectToken: "",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "subject token is required")
	})

	t.Run("returns error for missing dex token endpoint", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "",
				ConnectorID:      "local-dex",
			},
			SubjectToken: "test-token",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dex token endpoint is required")
	})

	t.Run("returns error for missing connector ID", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.example.com/token",
				ConnectorID:      "",
			},
			SubjectToken: "test-token",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connector ID is required")
	})

	t.Run("returns error for missing user ID", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.example.com/token",
				ConnectorID:      "local-dex",
			},
			SubjectToken: "test-token",
			UserID:       "",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "user ID is required")
	})

	t.Run("returns error for non-HTTPS endpoint", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "http://dex.example.com/token",
				ConnectorID:      "local-dex",
			},
			SubjectToken: "test-token",
			UserID:       "user123",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dex token endpoint must use HTTPS")
	})

	t.Run("rejects http://localhost - HTTPS required even for local", func(t *testing.T) {
		// HTTPS is enforced for all endpoints for security
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "http://localhost:5556/token",
				ConnectorID:      "local-dex",
			},
			SubjectToken: "test-token",
			UserID:       "user123",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must use HTTPS")
	})

	t.Run("accepts HTTPS endpoint", func(t *testing.T) {
		// Valid HTTPS endpoint should pass validation (but may fail on network)
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.example.com/token",
				ConnectorID:      "local-dex",
			},
			SubjectToken: "test-token",
			UserID:       "user123",
		})
		// Should not fail on HTTPS validation (may fail on network/exchange)
		require.Error(t, err) // Will fail on network, but not HTTPS validation
		assert.NotContains(t, err.Error(), "must use HTTPS")
	})

	t.Run("returns error for non-HTTPS expectedIssuer", func(t *testing.T) {
		// Defense-in-depth: validate expectedIssuer uses HTTPS in code,
		// even though CRD schema validation also enforces this
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.example.com/token",
				ExpectedIssuer:   "http://dex.example.com", // Non-HTTPS issuer
				ConnectorID:      "local-dex",
			},
			SubjectToken: "test-token",
			UserID:       "user123",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected issuer must use HTTPS")
	})

	t.Run("accepts HTTPS expectedIssuer", func(t *testing.T) {
		// Valid HTTPS expectedIssuer should pass validation
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex-proxy.example.com/token",
				ExpectedIssuer:   "https://dex.example.com",
				ConnectorID:      "local-dex",
			},
			SubjectToken: "test-token",
			UserID:       "user123",
		})
		// Should not fail on HTTPS validation (may fail on network/exchange)
		require.Error(t, err) // Will fail on network, but not HTTPS validation
		assert.NotContains(t, err.Error(), "must use HTTPS")
	})

	t.Run("allows empty expectedIssuer", func(t *testing.T) {
		// Empty expectedIssuer is allowed (falls back to deriving from endpoint)
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.example.com/token",
				ExpectedIssuer:   "", // Empty is allowed
				ConnectorID:      "local-dex",
			},
			SubjectToken: "test-token",
			UserID:       "user123",
		})
		// Should not fail on expectedIssuer validation
		require.Error(t, err) // Will fail on network, but not validation
		assert.NotContains(t, err.Error(), "expected issuer")
	})
}

func TestTokenExchanger_Cache(t *testing.T) {
	exchanger := NewTokenExchanger()

	t.Run("cache operations work correctly", func(t *testing.T) {
		// Cache stats should start empty
		stats := exchanger.GetCacheStats()
		assert.Equal(t, 0, stats.CurrentEntries)

		// Clear all should not panic on empty cache
		exchanger.ClearAllCache()
		assert.Equal(t, 0, exchanger.cache.Size())

		// Clear specific key should not panic
		exchanger.ClearCache("https://dex.example.com/token", "connector", "user123")
	})

	t.Run("cleanup removes nothing when cache is empty", func(t *testing.T) {
		removed := exchanger.Cleanup()
		assert.Equal(t, 0, removed)
	})
}

func TestTokenExchangeConfig(t *testing.T) {
	t.Run("config struct holds all fields", func(t *testing.T) {
		config := api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex.remote.example.com/token",
			ExpectedIssuer:   "https://dex.original.example.com",
			ConnectorID:      "cluster-a-dex",
			Scopes:           "openid profile email groups",
		}

		assert.True(t, config.Enabled)
		assert.Equal(t, "https://dex.remote.example.com/token", config.DexTokenEndpoint)
		assert.Equal(t, "https://dex.original.example.com", config.ExpectedIssuer)
		assert.Equal(t, "cluster-a-dex", config.ConnectorID)
		assert.Equal(t, "openid profile email groups", config.Scopes)
	})

	t.Run("supports separate access URL and issuer URL", func(t *testing.T) {
		// This is the key scenario from issue #303:
		// - DexTokenEndpoint is the proxy URL used to access Dex
		// - ExpectedIssuer is the actual Dex issuer URL
		config := api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex-cluster.proxy.example.com/token", // Via proxy
			ExpectedIssuer:   "https://dex.cluster.example.com",             // Original issuer
			ConnectorID:      "upstream-cluster",
		}

		assert.Equal(t, "https://dex-cluster.proxy.example.com/token", config.DexTokenEndpoint)
		assert.Equal(t, "https://dex.cluster.example.com", config.ExpectedIssuer)
		assert.NotEqual(t, config.DexTokenEndpoint, config.ExpectedIssuer)
	})
}

func TestGetExpectedIssuer(t *testing.T) {
	t.Run("returns ExpectedIssuer when explicitly set", func(t *testing.T) {
		config := &api.TokenExchangeConfig{
			DexTokenEndpoint: "https://dex-proxy.example.com/token",
			ExpectedIssuer:   "https://dex.original.example.com",
		}
		assert.Equal(t, "https://dex.original.example.com", GetExpectedIssuer(config))
	})

	t.Run("derives issuer from DexTokenEndpoint when ExpectedIssuer not set", func(t *testing.T) {
		config := &api.TokenExchangeConfig{
			DexTokenEndpoint: "https://dex.example.com/token",
		}
		assert.Equal(t, "https://dex.example.com", GetExpectedIssuer(config))
	})

	t.Run("handles /dex/token path correctly", func(t *testing.T) {
		config := &api.TokenExchangeConfig{
			DexTokenEndpoint: "https://dex.example.com/dex/token",
		}
		assert.Equal(t, "https://dex.example.com/dex", GetExpectedIssuer(config))
	})

	t.Run("returns empty for nil config", func(t *testing.T) {
		assert.Equal(t, "", GetExpectedIssuer(nil))
	})

	t.Run("returns empty for empty config", func(t *testing.T) {
		config := &api.TokenExchangeConfig{}
		assert.Equal(t, "", GetExpectedIssuer(config))
	})
}

func TestDeriveIssuerFromTokenEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		expected string
	}{
		{
			name:     "standard /token suffix",
			endpoint: "https://dex.example.com/token",
			expected: "https://dex.example.com",
		},
		{
			name:     "with port number",
			endpoint: "https://dex.example.com:5556/token",
			expected: "https://dex.example.com:5556",
		},
		{
			name:     "with path prefix",
			endpoint: "https://dex.example.com/dex/token",
			expected: "https://dex.example.com/dex",
		},
		{
			name:     "no /token suffix",
			endpoint: "https://dex.example.com/auth",
			expected: "https://dex.example.com/auth",
		},
		{
			name:     "trailing slash before token",
			endpoint: "https://dex.example.com//token",
			expected: "https://dex.example.com", // Both /token and trailing / are removed
		},
		{
			name:     "empty string",
			endpoint: "",
			expected: "",
		},
		{
			name:     "just domain",
			endpoint: "https://dex.example.com",
			expected: "https://dex.example.com",
		},
		{
			name:     "with query params removes /token from path",
			endpoint: "https://dex.example.com/token?foo=bar",
			expected: "https://dex.example.com?foo=bar", // /token is removed, query params preserved
		},
		{
			name:     "deep path",
			endpoint: "https://dex.example.com/api/v1/oauth/token",
			expected: "https://dex.example.com/api/v1/oauth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deriveIssuerFromTokenEndpoint(tt.endpoint)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateTokenIssuer(t *testing.T) {
	// Create a valid JWT with a specific issuer
	createTestToken := func(issuer string) string {
		// JWT payload with the specified issuer
		payload := fmt.Sprintf(`{"iss":"%s","sub":"user123","exp":9999999999}`, issuer)
		// Encode as base64 (header.payload.signature format)
		encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
		return "eyJhbGciOiJSUzI1NiJ9." + encodedPayload + ".signature"
	}

	t.Run("validation passes when issuer matches", func(t *testing.T) {
		token := createTestToken("https://dex.example.com")
		err := validateTokenIssuer(token, "https://dex.example.com")
		assert.NoError(t, err)
	})

	t.Run("validation passes with trailing slash normalization", func(t *testing.T) {
		token := createTestToken("https://dex.example.com/")
		err := validateTokenIssuer(token, "https://dex.example.com")
		assert.NoError(t, err)
	})

	t.Run("validation fails when issuer mismatches", func(t *testing.T) {
		token := createTestToken("https://evil.example.com")
		err := validateTokenIssuer(token, "https://dex.example.com")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "issuer mismatch")
		assert.Contains(t, err.Error(), "evil.example.com")
		assert.Contains(t, err.Error(), "dex.example.com")
	})

	t.Run("error message does not leak token content", func(t *testing.T) {
		// Security: Verify that issuer mismatch errors only contain issuer URLs,
		// not the full token which could be sensitive
		sensitiveToken := createTestToken("https://attacker.example.com")
		err := validateTokenIssuer(sensitiveToken, "https://dex.example.com")
		require.Error(t, err)

		errMsg := err.Error()
		// Error should contain issuer URLs for debugging
		assert.Contains(t, errMsg, "attacker.example.com")
		assert.Contains(t, errMsg, "dex.example.com")

		// Error should NOT contain the full token or its parts
		// The token has format: header.payload.signature
		assert.NotContains(t, errMsg, sensitiveToken, "error message should not contain full token")
		assert.NotContains(t, errMsg, "eyJhbGciOiJSUzI1NiJ9", "error message should not contain JWT header")
		assert.NotContains(t, errMsg, ".signature", "error message should not contain signature placeholder")
	})

	t.Run("validation skipped when expected issuer is empty", func(t *testing.T) {
		token := createTestToken("https://any.issuer.com")
		err := validateTokenIssuer(token, "")
		assert.NoError(t, err) // No validation when expected is empty
	})

	t.Run("validation fails for empty token", func(t *testing.T) {
		err := validateTokenIssuer("", "https://dex.example.com")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token is empty")
	})

	t.Run("validation skipped for non-JWT opaque token", func(t *testing.T) {
		// Opaque tokens (not JWTs) should skip validation
		err := validateTokenIssuer("opaque-access-token-abc123", "https://dex.example.com")
		assert.NoError(t, err) // Skips validation for non-JWT tokens
	})

	t.Run("validation skipped for token with wrong number of parts", func(t *testing.T) {
		// Token with only 2 parts is not a valid JWT
		err := validateTokenIssuer("header.payload", "https://dex.example.com")
		assert.NoError(t, err) // Skips validation for non-JWT tokens
	})
}

func TestIsJWTToken(t *testing.T) {
	t.Run("returns true for valid JWT format", func(t *testing.T) {
		assert.True(t, isJWTToken("header.payload.signature"))
	})

	t.Run("returns false for opaque token", func(t *testing.T) {
		assert.False(t, isJWTToken("opaque-token-abc123"))
	})

	t.Run("returns false for token with 2 parts", func(t *testing.T) {
		assert.False(t, isJWTToken("header.payload"))
	})

	t.Run("returns false for token with 4 parts", func(t *testing.T) {
		assert.False(t, isJWTToken("a.b.c.d"))
	})

	t.Run("returns false for empty token", func(t *testing.T) {
		assert.False(t, isJWTToken(""))
	})
}

func TestExtractIssuerFromToken(t *testing.T) {
	createTestToken := func(payload string) string {
		encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
		return "eyJhbGciOiJSUzI1NiJ9." + encodedPayload + ".signature"
	}

	t.Run("extracts issuer from valid token", func(t *testing.T) {
		token := createTestToken(`{"iss":"https://dex.example.com","sub":"user123"}`)
		issuer, err := extractIssuerFromToken(token)
		require.NoError(t, err)
		assert.Equal(t, "https://dex.example.com", issuer)
	})

	t.Run("returns error for token without iss claim", func(t *testing.T) {
		token := createTestToken(`{"sub":"user123"}`)
		_, err := extractIssuerFromToken(token)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "iss claim not found")
	})

	t.Run("returns error for invalid JWT format", func(t *testing.T) {
		_, err := extractIssuerFromToken("invalid")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid JWT format")
	})

	t.Run("returns error for empty token", func(t *testing.T) {
		_, err := extractIssuerFromToken("")
		require.Error(t, err)
	})
}
