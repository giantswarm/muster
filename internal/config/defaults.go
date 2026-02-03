package config

const (
	// DefaultOAuthCallbackPath is the default path for OAuth server callbacks (Cursor -> Muster auth)
	DefaultOAuthCallbackPath = "/oauth/callback"

	// DefaultOAuthProxyCallbackPath is the default path for OAuth proxy callbacks (Muster -> Remote server auth)
	// This MUST be different from DefaultOAuthCallbackPath to avoid route conflicts
	DefaultOAuthProxyCallbackPath = "/oauth/proxy/callback"

	// DefaultOAuthCIMDPath is the default path for serving the Client ID Metadata Document (CIMD)
	DefaultOAuthCIMDPath = "/.well-known/oauth-client.json"

	// DefaultOAuthCIMDScopes contains the default OAuth scopes for the CIMD.
	// Operators can customize this via Helm values (muster.oauth.cimdScopes) to add
	// additional scopes needed by downstream MCP servers (e.g., Google API scopes).
	// The "groups" scope is included by default to ensure group claims are available
	// in ID tokens for RBAC decisions in downstream services like mcp-kubernetes.
	DefaultOAuthCIMDScopes = "openid profile email groups offline_access"

	// DefaultOAuthServerProvider is the default OAuth provider for server protection.
	DefaultOAuthServerProvider = "dex"

	// DefaultOAuthStorageType is the default storage type for OAuth tokens.
	DefaultOAuthStorageType = "memory"
)

// GetDefaultConfigWithRoles returns default configuration
func GetDefaultConfigWithRoles() MusterConfig {
	return MusterConfig{
		Aggregator: AggregatorConfig{
			Port:      8090,
			Host:      "localhost",
			Transport: MCPTransportStreamableHTTP,
			OAuth: OAuthConfig{
				MCPClient: OAuthMCPClientConfig{
					Enabled:      false, // Disabled by default, requires explicit enablement
					CallbackPath: DefaultOAuthProxyCallbackPath,
					CIMD: OAuthCIMDConfig{
						Path: DefaultOAuthCIMDPath,
						// Scopes intentionally empty - defaults to DefaultOAuthCIMDScopes via GetCIMDScopes()
					},
					// ClientID is intentionally NOT set here - when empty, GetEffectiveClientID()
					// auto-derives from PublicURL. Setting a default would prevent self-hosted CIMD.
				},
				Server: OAuthServerConfig{
					Enabled:                    false, // Disabled by default, requires explicit enablement
					Provider:                   DefaultOAuthServerProvider,
					EnableCIMD:                 true, // Enable CIMD by default for MCP 2025-11-25 compliance
					AllowLocalhostRedirectURIs: true, // Enable localhost redirects for native apps per RFC 8252
					Storage: OAuthStorageConfig{
						Type: DefaultOAuthStorageType,
					},
				},
			},
		},
	}
}
