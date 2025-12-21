package config

import "strings"

// MusterConfig is the top-level configuration structure for muster.
type MusterConfig struct {
	Aggregator AggregatorConfig `yaml:"aggregator"`
	Namespace  string           `yaml:"namespace,omitempty"` // Namespace for MCPServer, ServiceClass and Workflow discovery
}

// MCPServerType defines the type of MCP server.
type MCPServerType string

const (
	MCPServerTypeStdio          MCPServerType = "stdio"
	MCPServerTypeStreamableHTTP MCPServerType = "streamable-http"
	MCPServerTypeSSE            MCPServerType = "sse"
)

const (
	// MCPTransportStreamableHTTP is the streamable HTTP transport.
	MCPTransportStreamableHTTP = "streamable-http"
	// MCPTransportSSE is the Server-Sent Events transport.
	MCPTransportSSE = "sse"
	// MCPTransportStdio is the standard I/O transport.
	MCPTransportStdio = "stdio"
)

// Use MCPServerDefinition from mcpserver package to avoid duplication

// AggregatorConfig defines the configuration for the MCP aggregator service.
type AggregatorConfig struct {
	Port         int    `yaml:"port,omitempty"`         // Port for the aggregator SSE endpoint (default: 8080)
	Host         string `yaml:"host,omitempty"`         // Host to bind to (default: localhost)
	Transport    string `yaml:"transport,omitempty"`    // Transport to use (default: streamable-http)
	MusterPrefix string `yaml:"musterPrefix,omitempty"` // Pre-prefix for all tools (default: "x")

	// OAuth Proxy configuration for remote MCP server authentication
	OAuth OAuthConfig `yaml:"oauth,omitempty"`
}

// OAuthConfig defines the OAuth proxy configuration for remote MCP server authentication.
// When enabled, the Muster Server acts as an OAuth client proxy, handling authentication
// flows on behalf of users without exposing tokens to the Muster Agent.
type OAuthConfig struct {
	// PublicURL is the publicly accessible URL of the Muster Server.
	// This is used to construct OAuth callback URLs (e.g., https://muster.example.com).
	// Required when OAuth is enabled.
	PublicURL string `yaml:"publicUrl,omitempty"`

	// ClientID is the OAuth client identifier.
	// This should be the URL of the Client ID Metadata Document (CIMD).
	// If not set and PublicURL is set, the ClientID will be auto-derived as
	// {PublicURL}/.well-known/oauth-client.json and muster will serve the CIMD itself.
	// Legacy value: "https://giantswarm.github.io/muster/oauth-client.json"
	ClientID string `yaml:"clientId,omitempty"`

	// CallbackPath is the path for the OAuth callback endpoint (default: "/oauth/callback").
	CallbackPath string `yaml:"callbackPath,omitempty"`

	// CIMDPath is the path for serving the Client ID Metadata Document (default: "/.well-known/oauth-client.json").
	// Muster will serve the CIMD at this path when OAuth is enabled and PublicURL is set.
	CIMDPath string `yaml:"cimdPath,omitempty"`

	// Enabled controls whether OAuth proxy functionality is active.
	// When false, remote MCP servers requiring auth will return errors.
	Enabled bool `yaml:"enabled,omitempty"`
}

// GetEffectiveClientID returns the effective OAuth client ID.
// If ClientID is explicitly set, it is returned as-is.
// If ClientID is empty but PublicURL is set, returns the self-hosted CIMD URL.
// Otherwise, returns the default Giant Swarm hosted CIMD URL.
func (c *OAuthConfig) GetEffectiveClientID() string {
	if c.ClientID != "" {
		return c.ClientID
	}

	// Auto-derive from PublicURL if set
	if c.PublicURL != "" {
		cimdPath := c.CIMDPath
		if cimdPath == "" {
			cimdPath = DefaultOAuthCIMDPath
		}
		return strings.TrimSuffix(c.PublicURL, "/") + cimdPath
	}

	// Fall back to default
	return DefaultOAuthClientID
}

// ShouldServeCIMD returns true if muster should serve its own CIMD.
// This is the case when OAuth is enabled, PublicURL is set, and ClientID
// is either empty or matches the auto-derived CIMD URL.
func (c *OAuthConfig) ShouldServeCIMD() bool {
	if !c.Enabled || c.PublicURL == "" {
		return false
	}

	// If ClientID is not set, we should serve our own CIMD
	if c.ClientID == "" {
		return true
	}

	// If ClientID matches what we would auto-generate, serve our own CIMD
	cimdPath := c.CIMDPath
	if cimdPath == "" {
		cimdPath = DefaultOAuthCIMDPath
	}
	autoClientID := strings.TrimSuffix(c.PublicURL, "/") + cimdPath
	return c.ClientID == autoClientID
}

// GetCIMDPath returns the path for serving the CIMD.
func (c *OAuthConfig) GetCIMDPath() string {
	if c.CIMDPath != "" {
		return c.CIMDPath
	}
	return DefaultOAuthCIMDPath
}

// GetRedirectURI returns the full redirect URI for OAuth callbacks.
func (c *OAuthConfig) GetRedirectURI() string {
	callbackPath := c.CallbackPath
	if callbackPath == "" {
		callbackPath = DefaultOAuthCallbackPath
	}
	return strings.TrimSuffix(c.PublicURL, "/") + callbackPath
}
