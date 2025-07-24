package api

// MCPServer represents a single MCP (Model Context Protocol) server definition and runtime state.
// It consolidates MCPServerDefinition, MCPServerInfo, and MCPServerConfig into a unified type
// that can be used for both configuration persistence (YAML) and API responses (JSON).
//
// MCP servers provide tools and capabilities to the muster system through the aggregator.
// They are configured as stdio processes or remote HTTP endpoints with their own
// specific configuration requirements and runtime characteristics.
type MCPServer struct {
	// Name is the unique identifier for this MCP server instance.
	// This name is used for registration, lookup, and management operations.
	Name string `yaml:"name" json:"name"`

	// Type specifies how this MCP server should be executed.
	// Supported values: "stdio" for local processes, "streamable-http" for HTTP-based servers, "sse" for Server-Sent Events
	Type MCPServerType `yaml:"type" json:"type"`

	// ToolPrefix is an optional prefix that will be prepended to all tool names
	// provided by this MCP server. This helps avoid naming conflicts when multiple
	// servers provide tools with similar names.
	ToolPrefix string `yaml:"toolPrefix,omitempty" json:"toolPrefix,omitempty"`

	// AutoStart determines whether this MCP server should be automatically started
	// when the muster system initializes or when dependencies become available.
	AutoStart bool `yaml:"autoStart,omitempty" json:"autoStart,omitempty"`

	// Command specifies the executable path for stdio type servers.
	// This field is required when Type is "stdio".
	Command string `yaml:"command,omitempty" json:"command,omitempty"`

	// Args specifies the command line arguments for stdio type servers.
	// This field is only available when Type is "stdio".
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`

	// URL is the endpoint where the remote MCP server can be reached
	// This field is required when Type is "streamable-http" or "sse".
	// Examples: http://mcp-server:8080/mcp, https://api.example.com/mcp
	URL string `yaml:"url,omitempty" json:"url,omitempty"`

	// Env contains environment variables to set for the MCP server.
	// For stdio servers, these are passed to the process when it is started.
	// For remote servers, these can be used for authentication or configuration.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Headers contains HTTP headers to send with requests to remote MCP servers.
	// This field is only relevant when Type is "streamable-http" or "sse".
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`

	// Timeout specifies the connection timeout for remote operations (in seconds)
	Timeout int `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// Error contains any error message from the most recent server operation.
	// This is runtime information and not persisted to YAML files.
	Error string `json:"error,omitempty" yaml:"-"`

	// Description provides a human-readable description of this MCP server's purpose.
	// This is runtime information populated from server metadata and not persisted to YAML.
	Description string `json:"description,omitempty" yaml:"-"`
}

// MCPServerType defines the execution model for an MCP server.
// This determines how the server process is managed and what configuration
// options are available for server deployment.
type MCPServerType string

const (
	// MCPServerTypeStdio indicates that the MCP server should be run as a local process.
	// Stdio servers are started using the configured command and arguments,
	// with communication typically happening over stdin/stdout.
	MCPServerTypeStdio MCPServerType = "stdio"

	// MCPServerTypeStreamableHTTP indicates that the MCP server should be accessed via HTTP.
	// StreamableHTTP servers are accessed via HTTP/HTTPS endpoints with streaming support.
	MCPServerTypeStreamableHTTP MCPServerType = "streamable-http"

	// MCPServerTypeSSE indicates that the MCP server should be accessed via Server-Sent Events.
	// SSE servers are accessed via HTTP/HTTPS endpoints using Server-Sent Events for communication.
	MCPServerTypeSSE MCPServerType = "sse"
)

// MCPServerInfo contains consolidated MCP server information for API responses.
// This type is used when returning server information through the API, providing
// a flattened view of server configuration and runtime state that is convenient
// for clients and user interfaces.
type MCPServerInfo struct {
	// Name is the unique identifier for this MCP server instance.
	Name string `json:"name"`

	// Type indicates the execution model for this server (stdio, streamable-http, or sse).
	Type string `json:"type"`

	// Description provides a human-readable description of the server's purpose and capabilities.
	Description string `json:"description,omitempty"`

	// AutoStart determines whether this MCP server should be automatically started
	AutoStart bool `json:"autoStart,omitempty"`

	// Command specifies the executable path for stdio type servers.
	Command string `json:"command,omitempty"`

	// Args specifies the command line arguments for stdio type servers.
	Args []string `json:"args,omitempty"`

	// URL is the endpoint where the remote MCP server can be reached
	URL string `json:"url,omitempty"`

	// Env contains environment variables to set for the MCP server.
	Env map[string]string `json:"env,omitempty"`

	// Headers contains HTTP headers to send with requests to remote MCP servers.
	Headers map[string]string `json:"headers,omitempty"`

	// Timeout specifies the connection timeout for remote operations (in seconds)
	Timeout int `json:"timeout,omitempty"`

	// ToolPrefix is an optional prefix for tool names.
	ToolPrefix string `json:"toolPrefix,omitempty"`

	// Error contains any error message from recent server operations.
	// This field is populated if the server is in an error state.
	Error string `json:"error,omitempty"`
}

// MCPServerManagerHandler defines the interface for MCP server management operations.
// This interface provides the core functionality for managing MCP server lifecycle,
// configuration, and tool availability. It also implements the ToolProvider interface
// to expose MCP server management capabilities as tools that can be called through
// the aggregator.
type MCPServerManagerHandler interface {
	// ListMCPServers returns information about all registered MCP servers.
	// This includes both configuration and runtime state information for each server.
	//
	// Returns:
	//   - []MCPServerInfo: Slice of server information (empty if no servers exist)
	ListMCPServers() []MCPServerInfo

	// GetMCPServer retrieves detailed information about a specific MCP server.
	// This includes both configuration and runtime state for the requested server.
	//
	// Args:
	//   - name: The unique name of the MCP server to retrieve
	//
	// Returns:
	//   - *MCPServerInfo: Server information, or nil if server not found
	//   - error: nil on success, or an error if the server could not be retrieved
	GetMCPServer(name string) (*MCPServerInfo, error)

	// ToolProvider interface for exposing MCP server management tools.
	// This allows MCP server operations to be performed through the aggregator
	// tool system, enabling programmatic and user-driven server management.
	ToolProvider
}
