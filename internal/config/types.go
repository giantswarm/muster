package config

// MusterConfig is the top-level configuration structure for muster.
type MusterConfig struct {
	Aggregator AggregatorConfig `yaml:"aggregator"`
	Namespace  string           `yaml:"namespace,omitempty"` // Namespace for MCPServer, ServiceClass and Workflow discovery
}

// MCPServerType defines the type of MCP server.
type MCPServerType string

const (
	MCPServerTypeLocalCommand MCPServerType = "localCommand"
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
}
