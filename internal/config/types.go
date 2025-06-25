package config

// MusterConfig is the top-level configuration structure for muster.
type MusterConfig struct {
	GlobalSettings GlobalSettings   `yaml:"globalSettings"`
	Aggregator     AggregatorConfig `yaml:"aggregator"`
}

// GlobalSettings might include things like default log levels, container runtime preferences, etc.
type GlobalSettings struct {
	DefaultContainerRuntime string `yaml:"defaultContainerRuntime,omitempty"` // e.g., "docker", "podman"
	// Add other global settings here
}

// MCPServerType defines the type of MCP server.
type MCPServerType string

const (
	MCPServerTypeLocalCommand MCPServerType = "localCommand"
	MCPServerTypeContainer    MCPServerType = "container"
)

const (
	// MCPTransportStreamableHTTP is the streamable HTTP transport.
	MCPTransportStreamableHTTP = "streamable-http"
	// MCPTransportSSE is the Server-Sent Events transport.
	MCPTransportSSE = "sse"
	// MCPTransportStdio is the standard I/O transport.
	MCPTransportStdio = "stdio"
)

// CapabilityType defines the type of capability that an MCP server can provide.
type CapabilityType string

const (
	CapabilityTypeAuthProvider        CapabilityType = "auth_provider"
	CapabilityTypeDiscoveryProvider   CapabilityType = "discovery_provider"
	CapabilityTypePortforwardProvider CapabilityType = "portforward_provider"
	CapabilityTypeClusterProvider     CapabilityType = "cluster_provider"
)

// CapabilityType is kept for reference but capabilities are now defined in YAML files
// and MCP servers just provide tools without capability awareness

// Use MCPServerDefinition from mcpserver package to avoid duplication

// AggregatorConfig defines the configuration for the MCP aggregator service.
type AggregatorConfig struct {
	Port         int    `yaml:"port,omitempty"`         // Port for the aggregator SSE endpoint (default: 8080)
	Host         string `yaml:"host,omitempty"`         // Host to bind to (default: localhost)
	Transport    string `yaml:"transport,omitempty"`    // Transport to use (default: streamable-http)
	Enabled      bool   `yaml:"enabled,omitempty"`      // Whether the aggregator is enabled (default: true if MCP servers exist)
	MusterPrefix string `yaml:"musterPrefix,omitempty"` // Pre-prefix for all tools (default: "x")
}

// GetDefaultConfig returns the default configuration for muster.
// mcName and wcName are the canonical names provided by the user.
func GetDefaultConfig(mcName, wcName string) MusterConfig {
	// Return minimal defaults - no k8s connection, no MCP servers, no port forwarding
	return GetDefaultConfigWithRoles()
}
