package api

// MCPServer represents a single MCP server definition and runtime state
// This consolidates MCPServerDefinition, MCPServerInfo, and MCPServerConfig into one type
type MCPServer struct {
	// Configuration fields (from YAML)
	Name      string        `yaml:"name" json:"name"`
	Type      MCPServerType `yaml:"type" json:"type"`
	AutoStart bool          `yaml:"autoStart" json:"autoStart"`
	ToolPrefix string       `yaml:"toolPrefix,omitempty" json:"toolPrefix,omitempty"`

	// LocalCommand fields
	Command []string          `yaml:"command,omitempty" json:"command,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Container fields
	Image            string            `yaml:"image,omitempty" json:"image,omitempty"`
	ContainerPorts   []string          `yaml:"containerPorts,omitempty" json:"containerPorts,omitempty"`
	ContainerEnv     map[string]string `yaml:"containerEnv,omitempty" json:"containerEnv,omitempty"`
	ContainerVolumes []string          `yaml:"containerVolumes,omitempty" json:"containerVolumes,omitempty"`
	Entrypoint       []string          `yaml:"entrypoint,omitempty" json:"entrypoint,omitempty"`
	ContainerUser    string            `yaml:"containerUser,omitempty" json:"containerUser,omitempty"`

	// Runtime fields (for API responses only)
	Error       string `json:"error,omitempty" yaml:"-"`
	Description string `json:"description,omitempty" yaml:"-"`
}

// MCPServerType defines the type of MCP server
type MCPServerType string

const (
	MCPServerTypeLocalCommand MCPServerType = "localCommand"
	MCPServerTypeContainer    MCPServerType = "container"
)

// MCPServerInfo contains consolidated MCP server information (API response)
type MCPServerInfo struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	AutoStart   bool              `json:"autoStart"`
	Description string            `json:"description,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Image       string            `json:"image,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Error       string            `json:"error,omitempty"`
}

// MCPServerManagerHandler defines the interface for MCP server management operations
type MCPServerManagerHandler interface {
	// MCP server definition management
	ListMCPServers() []MCPServerInfo
	GetMCPServer(name string) (*MCPServerInfo, error)

	// Tool provider interface for exposing MCP server management tools
	ToolProvider
}
