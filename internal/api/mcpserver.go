package api

// MCPServer represents a single MCP (Model Context Protocol) server definition and runtime state.
// It consolidates MCPServerDefinition, MCPServerInfo, and MCPServerConfig into a unified type
// that can be used for both configuration persistence (YAML) and API responses (JSON).
//
// MCP servers provide tools and capabilities to the muster system through the aggregator.
// They can be configured as local command processes or containerized services, each with
// their own specific configuration requirements and runtime characteristics.
type MCPServer struct {
	// Name is the unique identifier for this MCP server instance.
	// This name is used for registration, lookup, and management operations.
	Name string `yaml:"name" json:"name"`

	// Type specifies how this MCP server should be executed.
	// Valid types are "localCommand" for processes and "container" for containerized servers.
	Type MCPServerType `yaml:"type" json:"type"`

	// AutoStart determines whether this MCP server should be automatically started
	// when the muster system initializes or when dependencies become available.
	AutoStart bool `yaml:"autoStart" json:"autoStart"`

	// ToolPrefix is an optional prefix that will be prepended to all tool names
	// provided by this MCP server. This helps avoid naming conflicts when multiple
	// servers provide tools with similar names.
	ToolPrefix string `yaml:"toolPrefix,omitempty" json:"toolPrefix,omitempty"`

	// Command specifies the command line arguments for localCommand type servers.
	// The first element is the executable path, followed by command line arguments.
	// This field is only used when Type is MCPServerTypeLocalCommand.
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`

	// Env contains environment variables to set for localCommand type servers.
	// These are passed to the process when it is started.
	// This field is only used when Type is MCPServerTypeLocalCommand.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Image specifies the container image to use for container type servers.
	// This should be a valid container image reference (e.g., "myregistry/myimage:tag").
	// This field is only used when Type is MCPServerTypeContainer.
	Image string `yaml:"image,omitempty" json:"image,omitempty"`

	// ContainerPorts lists the ports that should be exposed from the container.
	// These are specified in the format "hostPort:containerPort" or just "port".
	// This field is only used when Type is MCPServerTypeContainer.
	ContainerPorts []string `yaml:"containerPorts,omitempty" json:"containerPorts,omitempty"`

	// ContainerEnv contains environment variables to set inside the container.
	// These are passed to the container when it is started.
	// This field is only used when Type is MCPServerTypeContainer.
	ContainerEnv map[string]string `yaml:"containerEnv,omitempty" json:"containerEnv,omitempty"`

	// ContainerVolumes lists volume mounts for the container.
	// These are specified in the format "hostPath:containerPath[:options]".
	// This field is only used when Type is MCPServerTypeContainer.
	ContainerVolumes []string `yaml:"containerVolumes,omitempty" json:"containerVolumes,omitempty"`

	// Entrypoint overrides the default entrypoint for container type servers.
	// If specified, this replaces the container's default entrypoint.
	// This field is only used when Type is MCPServerTypeContainer.
	Entrypoint []string `yaml:"entrypoint,omitempty" json:"entrypoint,omitempty"`

	// ContainerUser specifies the user to run the container as.
	// This can be a username, UID, or "user:group" format.
	// This field is only used when Type is MCPServerTypeContainer.
	ContainerUser string `yaml:"containerUser,omitempty" json:"containerUser,omitempty"`

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
	// MCPServerTypeLocalCommand indicates that the MCP server should be run as a local process.
	// Local command servers are started using the configured command and arguments,
	// with communication typically happening over stdin/stdout or named pipes.
	MCPServerTypeLocalCommand MCPServerType = "localCommand"

	// MCPServerTypeContainer indicates that the MCP server should be run as a containerized service.
	// Container servers are managed using a container runtime (e.g., Docker),
	// with communication happening over network protocols or container networking.
	MCPServerTypeContainer MCPServerType = "container"
)

// MCPServerInfo contains consolidated MCP server information for API responses.
// This type is used when returning server information through the API, providing
// a flattened view of server configuration and runtime state that is convenient
// for clients and user interfaces.
type MCPServerInfo struct {
	// Name is the unique identifier for this MCP server instance.
	Name string `json:"name"`

	// Type indicates the execution model for this server (localCommand or container).
	Type string `json:"type"`

	// AutoStart indicates whether this server is configured to start automatically.
	AutoStart bool `json:"autoStart"`

	// Description provides a human-readable description of the server's purpose and capabilities.
	Description string `json:"description,omitempty"`

	// Command contains the command line used to start local command type servers.
	// This field is empty for container type servers.
	Command []string `json:"command,omitempty"`

	// Image contains the container image reference for container type servers.
	// This field is empty for local command type servers.
	Image string `json:"image,omitempty"`

	// Env contains the environment variables configured for this server.
	// For local commands, this is process environment; for containers, this is container environment.
	Env map[string]string `json:"env,omitempty"`

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
	// Parameters:
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
