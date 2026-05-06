package api

import (
	"time"
)

// ServiceInfo provides information about a service instance.
// All service implementations must provide this interface to be managed
// by the service registry and orchestrator.
type ServiceInfo interface {
	// GetName returns the unique name/identifier of the service
	GetName() string

	// GetType returns the service type (e.g., TypeMCPServer, TypeAggregator)
	GetType() ServiceType

	// GetState returns the current operational state of the service
	GetState() ServiceState

	// GetHealth returns the current health status of the service
	GetHealth() HealthStatus

	// GetLastError returns the last error encountered by the service, or nil if none
	GetLastError() error

	// GetServiceData returns additional metadata and runtime information about the service
	GetServiceData() map[string]interface{}
}

// ConfigurableService extends ServiceInfo with the ability to detect configuration
// changes and update configuration at runtime. Services implement this interface
// to allow reconcilers to determine whether a restart is needed and to apply
// new configuration before restarting.
//
// Methods accept interface{} instead of a concrete type (e.g. *MCPServer) so
// that the interface remains generic across different service kinds. Each
// implementation performs a type assertion internally.
type ConfigurableService interface {
	ServiceInfo

	// ConfigurationChanged returns true if the new configuration differs from
	// the current one in a way that requires a restart. The service owns this
	// comparison logic because it has typed access to its configuration struct.
	//
	// Args:
	//   - newConfig: The new configuration to compare against (type depends on service implementation)
	//
	// Returns:
	//   - bool: true if the configuration has changed and a restart is needed
	ConfigurationChanged(newConfig interface{}) bool

	// UpdateConfiguration updates the service's internal configuration.
	// This should be called before restarting a service when its definition changes.
	//
	// Args:
	//   - config: The new configuration (type depends on service implementation)
	//
	// Returns:
	//   - error: Error if the configuration is invalid or update fails
	UpdateConfiguration(config interface{}) error
}

// ServiceRegistryHandler provides access to registered services in the system.
// This handler implements the service discovery aspect of the Service Locator Pattern,
// allowing components to find and access service information without direct coupling.
type ServiceRegistryHandler interface {
	// Get retrieves a service by name from the registry.
	//
	// Args:
	//   - name: The unique name of the service to retrieve
	//
	// Returns:
	//   - ServiceInfo: The service information if found
	//   - bool: true if the service exists, false otherwise
	Get(name string) (ServiceInfo, bool)

	// GetAll returns all services currently registered in the system.
	//
	// Returns:
	//   - []ServiceInfo: List of all registered services (both static and dynamic)
	GetAll() []ServiceInfo

	// GetByType returns all services of a specific type.
	//
	// Args:
	//   - serviceType: The type of services to retrieve (e.g., TypeMCPServer)
	//
	// Returns:
	//   - []ServiceInfo: List of services matching the specified type
	GetByType(serviceType ServiceType) []ServiceInfo
}

// ServiceManagerHandler provides lifecycle management for the static services that
// the orchestrator owns (MCP servers, the aggregator). Service registration is
// driven by configuration at startup; this handler exposes start/stop/restart
// operations and status queries for those services.
type ServiceManagerHandler interface {
	// StartService starts a service by name.
	StartService(name string) error

	// StopService stops a running service by name.
	StopService(name string) error

	// RestartService restarts a service by name (stop followed by start).
	RestartService(name string) error

	// GetServiceStatus returns the current status of a service.
	GetServiceStatus(name string) (*ServiceStatus, error)

	// GetAllServices returns the status of all services in the system.
	GetAllServices() []ServiceStatus

	// SubscribeToStateChanges returns a channel for receiving service state change events.
	// The returned channel should be consumed to prevent blocking the event system.
	SubscribeToStateChanges() <-chan ServiceStateChangedEvent

	// ToolProvider integration for exposing service management as MCP tools
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

// ServiceStateChangedEvent represents a service state transition event.
// These events are published whenever a service changes state, allowing
// components to react to service lifecycle changes.
type ServiceStateChangedEvent struct {
	// Name is the unique identifier of the service that changed state
	Name string `json:"name"`

	// ServiceType indicates the type of service (e.g., "MCPServer", "Aggregator")
	ServiceType string `json:"service_type"`

	// OldState is the previous state before the transition
	OldState string `json:"old_state"`

	// NewState is the current state after the transition
	NewState string `json:"new_state"`

	// Health is the current health status of the service
	Health string `json:"health"`

	// Error contains error information if the state change was due to an error
	Error error `json:"error,omitempty"`

	// Timestamp indicates when the state change occurred
	Timestamp time.Time `json:"timestamp"`
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

// ServiceListResponse represents a list of services in API responses.
// This is used by endpoints that return multiple service status information.
type ServiceListResponse struct {
	// Services contains the list of service status information
	Services []ServiceStatus `json:"services"`
}

// StateUpdater is an optional interface for services that allow external state updates.
// This is used to update service state when external events occur, such as SSO
// authentication succeeding at the session level.
//
// Not all services implement this interface; callers should type-assert before use.
type StateUpdater interface {
	// UpdateState updates the service's operational state.
	//
	// Args:
	//   - state: The new service state
	//   - health: The new health status
	//   - err: Optional error associated with the state change
	UpdateState(state ServiceState, health HealthStatus, err error)
}
