package api

// MCPServer represents a single MCP (Model Context Protocol) server definition and runtime state.
// It consolidates MCPServerDefinition, MCPServerInfo, and MCPServerConfig into a unified type
// that can be used for both configuration persistence (YAML) and API responses (JSON).
//
// MCP servers provide tools and capabilities to the muster system through the aggregator.
// They are configured as local command processes or remote HTTP endpoints with their own
// specific configuration requirements and runtime characteristics.
type MCPServer struct {
	// Name is the unique identifier for this MCP server instance.
	// This name is used for registration, lookup, and management operations.
	Name string `yaml:"name" json:"name"`

	// Type specifies how this MCP server should be executed.
	// Supported values: "local" for local processes, "remote" for remote MCP servers
	Type MCPServerType `yaml:"type" json:"type"`

	// ToolPrefix is an optional prefix that will be prepended to all tool names
	// provided by this MCP server. This helps avoid naming conflicts when multiple
	// servers provide tools with similar names.
	ToolPrefix string `yaml:"toolPrefix,omitempty" json:"toolPrefix,omitempty"`

	// Local contains configuration for local MCP servers (type=local)
	Local *MCPServerLocalConfig `yaml:"local,omitempty" json:"local,omitempty"`

	// Remote contains configuration for remote MCP servers (type=remote)
	Remote *MCPServerRemoteConfig `yaml:"remote,omitempty" json:"remote,omitempty"`

	// Error contains any error message from the most recent server operation.
	// This is runtime information and not persisted to YAML files.
	Error string `json:"error,omitempty" yaml:"-"`

	// Description provides a human-readable description of this MCP server's purpose.
	// This is runtime information populated from server metadata and not persisted to YAML.
	Description string `json:"description,omitempty" yaml:"-"`
}

// MCPServerLocalConfig defines configuration for local MCP servers
type MCPServerLocalConfig struct {
	// AutoStart determines whether this MCP server should be automatically started
	// when the muster system initializes or when dependencies become available.
	AutoStart bool `yaml:"autoStart" json:"autoStart"`

	// Command specifies the command line arguments for local type servers.
	// The first element is the executable path, followed by command line arguments.
	// This field is required when Type is "local".
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`

	// Env contains environment variables to set for local type servers.
	// These are passed to the process when it is started.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

// MCPServerRemoteConfig defines configuration for remote MCP servers
type MCPServerRemoteConfig struct {
	// Endpoint is the URL where the remote MCP server can be reached
	// Examples: http://mcp-server:8080/mcp, https://api.example.com/mcp
	Endpoint string `yaml:"endpoint" json:"endpoint"`

	// Transport specifies the protocol used to communicate with the remote server
	// Supported values: "http" (for HTTP/HTTPS), "sse" (Server-Sent Events), "websocket"
	Transport string `yaml:"transport" json:"transport"`

	// Timeout specifies the connection timeout for remote operations (in seconds)
	Timeout int `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// MCPServerType defines the execution model for an MCP server.
// This determines how the server process is managed and what configuration
// options are available for server deployment.
type MCPServerType string

const (
	// MCPServerTypeLocal indicates that the MCP server should be run as a local process.
	// Local servers are started using the configured command and arguments,
	// with communication typically happening over stdin/stdout or named pipes.
	MCPServerTypeLocal MCPServerType = "local"

	// MCPServerTypeRemote indicates that the MCP server should be accessed remotely.
	// Remote servers are accessed via HTTP/HTTPS endpoints using various transports
	// such as HTTP, SSE (Server-Sent Events), or WebSocket.
	MCPServerTypeRemote MCPServerType = "remote"
)

// MCPServerInfo contains consolidated MCP server information for API responses.
// This type is used when returning server information through the API, providing
// a flattened view of server configuration and runtime state that is convenient
// for clients and user interfaces.
type MCPServerInfo struct {
	// Name is the unique identifier for this MCP server instance.
	Name string `json:"name"`

	// Type indicates the execution model for this server (local or remote).
	Type string `json:"type"`

	// Description provides a human-readable description of the server's purpose and capabilities.
	Description string `json:"description,omitempty"`

	// Local contains configuration for local MCP servers (type=local)
	Local *MCPServerLocalConfig `json:"local,omitempty"`

	// Remote contains configuration for remote MCP servers (type=remote)
	Remote *MCPServerRemoteConfig `json:"remote,omitempty"`

	// Error contains any error message from recent server operations.
	// This field is populated if the server is in an error state.
	Error string `json:"error,omitempty"`

	// Legacy fields for backward compatibility - these are populated from Local config
	// TODO: Remove these in a future version
	AutoStart bool              `json:"autoStart,omitempty"`
	Command   []string          `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
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
