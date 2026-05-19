package api

// ServiceManagerHandler exposes MCPServer lifecycle through the legacy
// core_service_{list,start,stop,restart,status} MCP tools. The "service"
// naming predates the muster-in-front pivot — every operation now targets
// an MCPServer's upstream-proxy registration in the aggregator.
type ServiceManagerHandler interface {
	StartService(name string) error
	StopService(name string) error
	RestartService(name string) error
	GetServiceStatus(name string) (*ServiceStatus, error)
	GetAllServices() []ServiceStatus

	// ToolProvider integration so the aggregator can advertise service_*.
	ToolProvider
}

// ServiceType represents the type/category of a service.
// This classification helps with service organization and type-specific operations.
type ServiceType string

const (
	// TypeMCPServer represents MCP (Model Context Protocol) server services
	TypeMCPServer ServiceType = "MCPServer"

	// TypeAggregator represents aggregator services that coordinate multiple MCP servers
	TypeAggregator ServiceType = "Aggregator"
)

// ServiceState represents the current operational state of a service.
// This provides a standardized way to track service lifecycle across all service types.
type ServiceState string

const (
	// StateStopped indicates the service is not running
	StateStopped ServiceState = "stopped"

	// StateStarting indicates the service is in the process of starting up
	StateStarting ServiceState = "starting"

	// StateRunning indicates the service is running and operational
	StateRunning ServiceState = "running"

	// StateStopping indicates the service is in the process of shutting down
	StateStopping ServiceState = "stopping"

	// StateError indicates the service encountered an error and may not be functional
	StateError ServiceState = "error"

	// StateFailed indicates the service failed to start or operate correctly
	StateFailed ServiceState = "failed"

	// StateUnknown indicates the service state cannot be determined
	StateUnknown ServiceState = "unknown"

	// StateWaiting indicates the service is waiting for dependencies or resources
	StateWaiting ServiceState = "waiting"

	// StateRetrying indicates the service is retrying a failed operation
	StateRetrying ServiceState = "retrying"

	// StateUnreachable indicates the service endpoint cannot be reached after multiple attempts.
	// This state is used for remote MCP servers (streamable-http, sse) that fail to connect
	// due to network issues, DNS failures, or decommissioned endpoints.
	// Servers in this state will use exponential backoff for retry attempts.
	StateUnreachable ServiceState = "unreachable"

	// StateAuthRequired indicates the service requires OAuth authentication before connecting.
	// This state is used for remote MCP servers that returned a 401 Unauthorized response
	// during initialization, indicating they support OAuth and require authentication.
	// Users should run `muster auth login --server <name>` to authenticate.
	//
	// This is distinct from StateUnreachable:
	// - StateAuthRequired: Server is reachable but requires authentication
	// - StateUnreachable: Server cannot be reached (network/connectivity issue)
	StateAuthRequired ServiceState = "auth_required"

	// StateConnected indicates the service is connected and authenticated.
	// This is an alias for StateRunning for semantic clarity with remote servers.
	// For remote MCP servers, "connected" is more intuitive than "running" since
	// the server itself is running remotely - we are just connected to it.
	StateConnected ServiceState = "connected"

	// StateDisconnected indicates the service was previously connected but is now disconnected.
	// This state is used for remote MCP servers that were successfully connected but
	// the connection was lost (session ended, server restart, etc.).
	StateDisconnected ServiceState = "disconnected"
)

// IsActiveState returns true if the given state indicates the service is actively
// running and operational. This includes both StateRunning (for local stdio servers)
// and StateConnected (for remote servers).
//
// Use this helper when checking if a service is available for use, rather than
// checking for StateRunning directly, to properly handle remote servers.
func IsActiveState(state ServiceState) bool {
	return state == StateRunning || state == StateConnected
}

// ServiceStatus represents the current status of a service for API responses.
// This is a simplified view of service information suitable for status queries
// and monitoring dashboards.
type ServiceStatus struct {
	// Name is the unique identifier of the service
	Name string `json:"name"`

	// ServiceType indicates the type of service (e.g., "MCPServer", "Aggregator")
	ServiceType string `json:"service_type"`

	// State is the current operational state of the service
	State ServiceState `json:"state"`

	// Health is the current health status of the service
	Health HealthStatus `json:"health"`

	// Error contains error information if the service is in an error state
	Error string `json:"error,omitempty"`

	// Metadata contains additional runtime information about the service
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}
