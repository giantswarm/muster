package aggregator

import (
	"context"
	"testing"

	"muster/internal/api"
	"muster/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestGetIDTokenForForwarding(t *testing.T) {
	// Valid JWT-like token with future expiry (not a real JWT, just the format for parsing).
	// The exp claim is set to 9999999999 (year 2286) to ensure it never expires during tests.
	validToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZXhwIjo5OTk5OTk5OTk5fQ.signature"

	t.Run("returns token from context when available", func(t *testing.T) {
		ctx := context.Background()
		ctx = server.ContextWithAccessToken(ctx, validToken)

		token := getIDTokenForForwarding(ctx, "test-session", "https://accounts.google.com")

		assert.Equal(t, validToken, token)
	})

	t.Run("returns empty when no token in context and no OAuth handler", func(t *testing.T) {
		ctx := context.Background()

		token := getIDTokenForForwarding(ctx, "test-session", "https://accounts.google.com")

		assert.Empty(t, token)
	})

	t.Run("context token takes priority over empty string", func(t *testing.T) {
		ctx := context.Background()
		ctx = server.ContextWithAccessToken(ctx, validToken)

		// Even with an issuer, context token should be returned
		token := getIDTokenForForwarding(ctx, "test-session", "")

		assert.Equal(t, validToken, token)
	})

	t.Run("returns empty for empty context token", func(t *testing.T) {
		ctx := context.Background()
		ctx = server.ContextWithAccessToken(ctx, "")

		token := getIDTokenForForwarding(ctx, "test-session", "https://accounts.google.com")

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

func TestIsIDTokenExpired(t *testing.T) {
	t.Run("empty token is expired", func(t *testing.T) {
		assert.True(t, isIDTokenExpired(""))
	})

	t.Run("invalid JWT format is expired", func(t *testing.T) {
		assert.True(t, isIDTokenExpired("not-a-jwt"))
	})

	t.Run("valid future exp is not expired", func(t *testing.T) {
		// Token with exp = 9999999999 (year 2286)
		token := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjk5OTk5OTk5OTl9.sig"
		assert.False(t, isIDTokenExpired(token))
	})

	t.Run("past exp is expired", func(t *testing.T) {
		// Token with exp = 0 (1970)
		token := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjB9.sig"
		assert.True(t, isIDTokenExpired(token))
	})

	t.Run("missing exp claim is expired", func(t *testing.T) {
		// Token with no exp claim
		token := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.sig"
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
		token := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIiwiZXhwIjo5OTk5OTk5OTk5fQ.sig"
		assert.Equal(t, "user123", extractUserIDFromToken(token))
	})

	t.Run("returns empty when sub claim is missing", func(t *testing.T) {
		// Token with only exp claim
		token := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjk5OTk5OTk5OTl9.sig"
		assert.Equal(t, "", extractUserIDFromToken(token))
	})
}
