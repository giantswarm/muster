package aggregator

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"muster/internal/api"
	"muster/internal/server"

	"github.com/stretchr/testify/assert"
)

// mockTeleportClientHandler implements api.TeleportClientHandler for testing.
type mockTeleportClientHandler struct {
	httpClient    *http.Client
	httpTransport *http.Transport
	err           error
	// Track calls for verification
	getClientCalls    int
	getTransportCalls int
	getConfigCalls    int
	lastConfig        api.TeleportClientConfig
	lastIdentityDir   string
}

func (m *mockTeleportClientHandler) GetHTTPClientForIdentity(identityDir string) (*http.Client, error) {
	m.getClientCalls++
	m.lastIdentityDir = identityDir
	if m.err != nil {
		return nil, m.err
	}
	return m.httpClient, nil
}

func (m *mockTeleportClientHandler) GetHTTPTransportForIdentity(identityDir string) (*http.Transport, error) {
	m.getTransportCalls++
	m.lastIdentityDir = identityDir
	if m.err != nil {
		return nil, m.err
	}
	return m.httpTransport, nil
}

func (m *mockTeleportClientHandler) GetHTTPClientForConfig(ctx context.Context, config api.TeleportClientConfig) (*http.Client, error) {
	m.getConfigCalls++
	m.lastConfig = config
	if m.err != nil {
		return nil, m.err
	}
	return m.httpClient, nil
}

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
		token := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIiwiZXhwIjo5OTk5OTk5OTk5fQ.sig"
		decoded, err := decodeJWTPayload(token)
		assert.NoError(t, err)
		assert.Contains(t, string(decoded), "user123")
		assert.Contains(t, string(decoded), "9999999999")
	})

	t.Run("handles token with only two parts", func(t *testing.T) {
		// Minimal JWT with just header and payload (no signature)
		token := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0"
		decoded, err := decodeJWTPayload(token)
		assert.NoError(t, err)
		assert.Contains(t, string(decoded), "test")
	})
}

func TestGetTeleportHTTPClientIfConfigured(t *testing.T) {
	ctx := context.Background()

	t.Run("returns not configured for nil serverInfo", func(t *testing.T) {
		result := getTeleportHTTPClientIfConfigured(ctx, nil)
		assert.False(t, result.Configured)
		assert.Nil(t, result.Client)
		assert.NoError(t, result.Error)
	})

	t.Run("returns not configured for nil authConfig", func(t *testing.T) {
		serverInfo := &ServerInfo{
			Name:       "test-server",
			AuthConfig: nil,
		}
		result := getTeleportHTTPClientIfConfigured(ctx, serverInfo)
		assert.False(t, result.Configured)
		assert.Nil(t, result.Client)
		assert.NoError(t, result.Error)
	})

	t.Run("returns not configured for non-teleport auth type", func(t *testing.T) {
		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
			},
		}
		result := getTeleportHTTPClientIfConfigured(ctx, serverInfo)
		assert.False(t, result.Configured)
		assert.Nil(t, result.Client)
		assert.NoError(t, result.Error)
	})

	t.Run("returns error for teleport type without teleport settings", func(t *testing.T) {
		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type:     api.AuthTypeTeleport,
				Teleport: nil,
			},
		}
		result := getTeleportHTTPClientIfConfigured(ctx, serverInfo)
		assert.True(t, result.Configured)
		assert.Nil(t, result.Client)
		assert.Error(t, result.Error)
		assert.Contains(t, result.Error.Error(), "teleport settings missing")
	})

	t.Run("returns error when no identity source is configured", func(t *testing.T) {
		// Register a handler to pass the handler check
		mockHandler := &mockTeleportClientHandler{httpClient: &http.Client{}}
		api.RegisterTeleportClient(mockHandler)
		defer api.RegisterTeleportClient(nil)

		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: api.AuthTypeTeleport,
				Teleport: &api.TeleportAuth{
					// No IdentityDir or IdentitySecretName
					AppName: "test-app",
				},
			},
		}
		result := getTeleportHTTPClientIfConfigured(ctx, serverInfo)
		assert.True(t, result.Configured)
		assert.Nil(t, result.Client)
		assert.Error(t, result.Error)
		assert.Contains(t, result.Error.Error(), "identityDir or identitySecretName")
	})

	t.Run("returns error when both identity sources are configured", func(t *testing.T) {
		// Register a handler to pass the handler check
		mockHandler := &mockTeleportClientHandler{httpClient: &http.Client{}}
		api.RegisterTeleportClient(mockHandler)
		defer api.RegisterTeleportClient(nil)

		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: api.AuthTypeTeleport,
				Teleport: &api.TeleportAuth{
					IdentityDir:        "/var/run/tbot/identity",
					IdentitySecretName: "tbot-identity",
					AppName:            "test-app",
				},
			},
		}
		result := getTeleportHTTPClientIfConfigured(ctx, serverInfo)
		assert.True(t, result.Configured)
		assert.Nil(t, result.Client)
		assert.Error(t, result.Error)
		assert.Contains(t, result.Error.Error(), "mutually exclusive")
	})

	t.Run("returns error when teleport handler is not registered", func(t *testing.T) {
		// Ensure no handler is registered
		api.RegisterTeleportClient(nil)

		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: api.AuthTypeTeleport,
				Teleport: &api.TeleportAuth{
					IdentityDir: "/var/run/tbot/identity",
					AppName:     "test-app",
				},
			},
		}
		result := getTeleportHTTPClientIfConfigured(ctx, serverInfo)
		assert.True(t, result.Configured)
		assert.Nil(t, result.Client)
		assert.Error(t, result.Error)
		assert.Contains(t, result.Error.Error(), "handler not registered")
	})

	t.Run("returns http client when handler is registered with identityDir", func(t *testing.T) {
		expectedClient := &http.Client{}
		mockHandler := &mockTeleportClientHandler{
			httpClient: expectedClient,
		}
		api.RegisterTeleportClient(mockHandler)
		defer api.RegisterTeleportClient(nil)

		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: api.AuthTypeTeleport,
				Teleport: &api.TeleportAuth{
					IdentityDir: "/var/run/tbot/identity",
					AppName:     "mcp-kubernetes",
				},
			},
		}
		result := getTeleportHTTPClientIfConfigured(ctx, serverInfo)
		assert.True(t, result.Configured)
		assert.Equal(t, expectedClient, result.Client)
		assert.NoError(t, result.Error)
		assert.Equal(t, 1, mockHandler.getConfigCalls)
		assert.Equal(t, "/var/run/tbot/identity", mockHandler.lastConfig.IdentityDir)
		assert.Equal(t, "mcp-kubernetes", mockHandler.lastConfig.AppName)
	})

	t.Run("returns http client when handler is registered with secret", func(t *testing.T) {
		expectedClient := &http.Client{}
		mockHandler := &mockTeleportClientHandler{
			httpClient: expectedClient,
		}
		api.RegisterTeleportClient(mockHandler)
		defer api.RegisterTeleportClient(nil)

		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: api.AuthTypeTeleport,
				Teleport: &api.TeleportAuth{
					IdentitySecretName:      "tbot-identity-output",
					IdentitySecretNamespace: "teleport-system",
					AppName:                 "mcp-kubernetes",
				},
			},
		}
		result := getTeleportHTTPClientIfConfigured(ctx, serverInfo)
		assert.True(t, result.Configured)
		assert.Equal(t, expectedClient, result.Client)
		assert.NoError(t, result.Error)
		assert.Equal(t, 1, mockHandler.getConfigCalls)
		assert.Equal(t, "tbot-identity-output", mockHandler.lastConfig.IdentitySecretName)
		assert.Equal(t, "teleport-system", mockHandler.lastConfig.IdentitySecretNamespace)
		assert.Equal(t, "mcp-kubernetes", mockHandler.lastConfig.AppName)
	})

	t.Run("returns error when handler returns error", func(t *testing.T) {
		mockHandler := &mockTeleportClientHandler{
			err: errors.New("failed to load certificates"),
		}
		api.RegisterTeleportClient(mockHandler)
		defer api.RegisterTeleportClient(nil)

		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: api.AuthTypeTeleport,
				Teleport: &api.TeleportAuth{
					IdentityDir: "/var/run/tbot/identity",
					AppName:     "mcp-kubernetes",
				},
			},
		}
		result := getTeleportHTTPClientIfConfigured(ctx, serverInfo)
		assert.True(t, result.Configured)
		assert.Nil(t, result.Client)
		assert.Error(t, result.Error)
		assert.Contains(t, result.Error.Error(), "failed to load certificates")
		assert.Equal(t, 1, mockHandler.getConfigCalls)
	})

	t.Run("returns not configured for empty auth type", func(t *testing.T) {
		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: "", // Empty type, should not match teleport
				Teleport: &api.TeleportAuth{
					IdentityDir: "/var/run/tbot/identity",
				},
			},
		}
		result := getTeleportHTTPClientIfConfigured(ctx, serverInfo)
		assert.False(t, result.Configured)
		assert.Nil(t, result.Client)
		assert.NoError(t, result.Error)
	})

	t.Run("works without AppName", func(t *testing.T) {
		expectedClient := &http.Client{}
		mockHandler := &mockTeleportClientHandler{
			httpClient: expectedClient,
		}
		api.RegisterTeleportClient(mockHandler)
		defer api.RegisterTeleportClient(nil)

		serverInfo := &ServerInfo{
			Name: "test-server",
			AuthConfig: &api.MCPServerAuth{
				Type: api.AuthTypeTeleport,
				Teleport: &api.TeleportAuth{
					IdentityDir: "/var/run/tbot/identity",
					// No AppName - should still work
				},
			},
		}
		result := getTeleportHTTPClientIfConfigured(ctx, serverInfo)
		assert.True(t, result.Configured)
		assert.Equal(t, expectedClient, result.Client)
		assert.NoError(t, result.Error)
		assert.Equal(t, "", mockHandler.lastConfig.AppName)
	})

	// New test: verify that caller can distinguish between "not configured" and "error"
	t.Run("caller can distinguish not-configured from error", func(t *testing.T) {
		// Not configured case (type is oauth, not teleport)
		oauthServer := &ServerInfo{
			Name: "oauth-server",
			AuthConfig: &api.MCPServerAuth{
				Type: "oauth",
			},
		}
		notConfigured := getTeleportHTTPClientIfConfigured(ctx, oauthServer)

		// Error case (teleport configured but handler missing)
		api.RegisterTeleportClient(nil)
		teleportServer := &ServerInfo{
			Name: "teleport-server",
			AuthConfig: &api.MCPServerAuth{
				Type: api.AuthTypeTeleport,
				Teleport: &api.TeleportAuth{
					IdentityDir: "/var/run/tbot/identity",
				},
			},
		}
		errorCase := getTeleportHTTPClientIfConfigured(ctx, teleportServer)

		// Not configured: caller should use default HTTP client
		assert.False(t, notConfigured.Configured, "oauth server should not be configured for teleport")
		assert.Nil(t, notConfigured.Client)
		assert.NoError(t, notConfigured.Error)

		// Error: caller should fail with explicit error, NOT fallback
		assert.True(t, errorCase.Configured, "teleport server is configured")
		assert.Nil(t, errorCase.Client)
		assert.Error(t, errorCase.Error, "should return error when teleport configured but failed")
	})
}
