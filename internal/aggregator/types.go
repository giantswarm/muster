package aggregator

import (
	"context"
	"sync"
	"time"

	"muster/internal/mcpserver"

	"github.com/mark3labs/mcp-go/mcp"
)

// MCPClient defines the interface for MCP client operations.
// This interface abstracts the underlying MCP client implementation
// and will be implemented by the client in the mcpserver package.
// It provides all necessary operations for interacting with MCP servers
// including initialization, tool execution, and resource/prompt access.
type MCPClient interface {
	// Initialize establishes the connection and performs protocol handshake.
	// This must be called before any other operations on the client.
	// Returns an error if the handshake fails or connection cannot be established.
	Initialize(ctx context.Context) error

	// Close cleanly shuts down the client connection.
	// This should be called when the client is no longer needed
	// to ensure proper cleanup of resources.
	Close() error

	// ListTools returns all available tools from the server.
	// This is used to discover what capabilities the server provides.
	ListTools(ctx context.Context) ([]mcp.Tool, error)

	// CallTool executes a specific tool and returns the result.
	// The name arg should match one of the tools returned by ListTools.
	// The args arg contains the tool-specific arguments as key-value pairs.
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error)

	// ListResources returns all available resources from the server.
	// Resources are data sources that can be read by the client.
	ListResources(ctx context.Context) ([]mcp.Resource, error)

	// ReadResource retrieves a specific resource by its URI.
	// The URI should match one of the resources returned by ListResources.
	ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error)

	// ListPrompts returns all available prompts from the server.
	// Prompts are templates or structured text that can be retrieved and used.
	ListPrompts(ctx context.Context) ([]mcp.Prompt, error)

	// GetPrompt retrieves a specific prompt with optional arguments.
	// The name should match one of the prompts returned by ListPrompts.
	// Args can be used to customize the prompt content.
	GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error)

	// Ping checks if the server is responsive.
	// This is used for health checking and connection validation.
	Ping(ctx context.Context) error
}

// ServerInfo contains information about a registered MCP server.
// This structure maintains both the connection details and cached
// capabilities for efficient access. It is thread-safe for concurrent
// access to cached data.
type ServerInfo struct {
	// Name is the unique identifier for this server within the aggregator
	Name string

	// Client is the MCP client instance used to communicate with the server
	Client MCPClient

	// LastUpdate tracks when the server information was last refreshed
	LastUpdate time.Time

	// ToolPrefix is the configured prefix for tools from this server.
	// This is used for name collision resolution.
	ToolPrefix string

	// URL is the server endpoint URL (for remote servers)
	URL string

	// Status indicates the server's connection/authentication status.
	// Can be connected, disconnected, or auth_required.
	Status ServerStatus

	// AuthInfo contains OAuth information if authentication is required.
	// This is populated when a 401 is received during initialization.
	AuthInfo *AuthInfo

	// Cached capabilities - these are updated periodically to avoid
	// repeated calls to the backend server for performance
	mu        sync.RWMutex
	Tools     []mcp.Tool     // Cached list of available tools
	Resources []mcp.Resource // Cached list of available resources
	Prompts   []mcp.Prompt   // Cached list of available prompts
	Connected bool           // Current connection status (deprecated, use Status)
}

// UpdateTools safely updates the server's cached tool list.
// This method is thread-safe and should be used whenever
// the server's available tools change.
func (s *ServerInfo) UpdateTools(tools []mcp.Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Tools = tools
}

// UpdateResources safely updates the server's cached resource list.
// This method is thread-safe and should be used whenever
// the server's available resources change.
func (s *ServerInfo) UpdateResources(resources []mcp.Resource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Resources = resources
}

// UpdatePrompts safely updates the server's cached prompt list.
// This method is thread-safe and should be used whenever
// the server's available prompts change.
func (s *ServerInfo) UpdatePrompts(prompts []mcp.Prompt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Prompts = prompts
}

// SetConnected safely updates the connection status.
// This is used to track whether the server is currently
// available for operations.
func (s *ServerInfo) SetConnected(connected bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Connected = connected
}

// IsConnected returns the current connection status.
// This method is thread-safe and can be called concurrently.
func (s *ServerInfo) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Connected
}

// AggregatorConfig holds configuration args for the aggregator.
// This structure defines how the aggregator should behave and
// what endpoints it should expose.
type AggregatorConfig struct {
	// Port specifies the port number to listen on for the aggregated MCP endpoint
	Port int

	// Host specifies the host address to bind to (default: localhost)
	Host string

	// Transport defines the protocol to use for MCP communication.
	// Supported values: "sse", "streamable-http", "stdio"
	Transport string

	// Yolo disables the security denylist for destructive tools.
	// When true, all tools are allowed regardless of their destructive nature.
	// This should only be enabled in development environments.
	Yolo bool

	// ConfigDir is the user configuration directory for workflows and other configs.
	// This is used to load workflow definitions and make them available as tools.
	ConfigDir string

	// MusterPrefix is the global prefix applied to all aggregated tools.
	// This helps distinguish muster tools from other MCP tools in mixed environments.
	// Default value is "x".
	MusterPrefix string

	// OAuth configuration for remote MCP server authentication
	OAuth OAuthProxyConfig
}

// OAuthProxyConfig holds OAuth proxy configuration for the aggregator.
type OAuthProxyConfig struct {
	// Enabled controls whether OAuth proxy functionality is active.
	Enabled bool

	// PublicURL is the publicly accessible URL of the Muster Server.
	// This is used to construct OAuth callback URLs.
	PublicURL string

	// ClientID is the OAuth client identifier (CIMD URL).
	ClientID string

	// CallbackPath is the path for the OAuth callback endpoint.
	CallbackPath string
}

// RegistrationEvent represents a server registration or deregistration event.
// These events are used internally to coordinate between different components
// when servers are added or removed from the aggregator.
type RegistrationEvent struct {
	// Type indicates whether this is a registration or deregistration event
	Type EventType

	// ServerName is the unique identifier of the server involved in the event
	ServerName string

	// Client is the MCP client associated with the server (may be nil for deregistration)
	Client MCPClient
}

// EventType represents the type of registration event.
// This enumeration defines the possible state changes for server registration.
type EventType int

const (
	// EventRegister indicates a server is being registered with the aggregator
	EventRegister EventType = iota

	// EventDeregister indicates a server is being removed from the aggregator
	EventDeregister
)

// ToolWithStatus represents a tool along with its security blocking status.
// This is used to provide visibility into which tools are available
// and which are blocked by the security denylist.
type ToolWithStatus struct {
	// Tool contains the MCP tool definition
	Tool mcp.Tool

	// Blocked indicates whether this tool is blocked by the security denylist.
	// Blocked tools cannot be executed unless the Yolo flag is enabled.
	Blocked bool
}

// ServerStatus represents the connection status of a server
type ServerStatus string

const (
	// StatusConnected indicates the server is connected and operational
	StatusConnected ServerStatus = "connected"

	// StatusDisconnected indicates the server is disconnected
	StatusDisconnected ServerStatus = "disconnected"

	// StatusAuthRequired indicates the server requires OAuth authentication
	// before it can complete the MCP protocol handshake
	StatusAuthRequired ServerStatus = "auth_required"
)

// AuthInfo is an alias to the mcpserver AuthInfo type for OAuth authentication.
// It contains OAuth authentication information extracted from a 401 response.
// See mcpserver.AuthInfo for detailed field documentation.
type AuthInfo = mcpserver.AuthInfo
