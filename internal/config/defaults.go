package config

const (
	// DefaultOAuthCallbackPath is the default path for OAuth callbacks
	DefaultOAuthCallbackPath = "/oauth/callback"

	// DefaultOAuthCIMDPath is the default path for serving the Client ID Metadata Document (CIMD)
	DefaultOAuthCIMDPath = "/.well-known/oauth-client.json"

	// DefaultOAuthClientID is the default Client ID Metadata Document URL.
	// This is the legacy Giant Swarm hosted CIMD. When oauth.publicUrl is set,
	// muster will auto-generate a CIMD and serve it at /.well-known/oauth-client.json,
	// making this default unused.
	DefaultOAuthClientID = "https://giantswarm.github.io/muster/oauth-client.json"

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
				CallbackPath: DefaultOAuthCallbackPath,
				CIMDPath:     DefaultOAuthCIMDPath,
				ClientID:     DefaultOAuthClientID,
				Enabled:      false, // Disabled by default, requires explicit enablement
			},
			OAuthServer: OAuthServerConfig{
				Enabled:    false, // Disabled by default, requires explicit enablement
				Provider:   DefaultOAuthServerProvider,
				EnableCIMD: true, // Enable CIMD by default for MCP 2025-11-25 compliance
				Storage: OAuthStorageConfig{
					Type: DefaultOAuthStorageType,
				},
			},
		},
	}
}
