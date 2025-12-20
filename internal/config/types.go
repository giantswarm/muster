package config

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
	// This should be the URL of the Client ID Metadata Document (CIMD),
	// e.g., "https://giantswarm.github.io/muster/oauth-client.json"
	ClientID string `yaml:"clientId,omitempty"`

	// CallbackPath is the path for the OAuth callback endpoint (default: "/oauth/callback").
	CallbackPath string `yaml:"callbackPath,omitempty"`

	// Enabled controls whether OAuth proxy functionality is active.
	// When false, remote MCP servers requiring auth will return errors.
	Enabled bool `yaml:"enabled,omitempty"`
}
