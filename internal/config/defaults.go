package config

const (
	// DefaultOAuthCallbackPath is the default path for OAuth callbacks
	DefaultOAuthCallbackPath = "/oauth/callback"

	// DefaultOAuthClientID is the default Client ID Metadata Document URL
	DefaultOAuthClientID = "https://giantswarm.github.io/muster/oauth-client.json"
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
				ClientID:     DefaultOAuthClientID,
				Enabled:      false, // Disabled by default, requires explicit enablement
			},
		},
	}
}
