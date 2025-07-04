package api

import (
	"context"
	"time"
)

// ServiceInfo provides information about a service instance.
// This interface defines the contract for accessing service metadata and state
// regardless of whether the service is static or ServiceClass-based.
//
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

// ServiceRegistryHandler provides access to registered services in the system.
// This handler implements the service discovery aspect of the Service Locator Pattern,
// allowing components to find and access service information without direct coupling.
//
// The registry maintains both static services (defined in configuration) and
// dynamic ServiceClass-based service instances.
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

// ServiceManagerHandler provides unified management for both static and ServiceClass-based services.
// This is the primary interface for service lifecycle operations in the Service Locator Pattern.
//
// The handler abstracts the differences between static services (defined in configuration)
// and dynamic ServiceClass-based services (created at runtime), providing a unified API
// for service management operations.
type ServiceManagerHandler interface {
	// Unified service lifecycle management (works for both static and ServiceClass-based services)

	// StartService starts a service by name.
	// Works for both static services and ServiceClass-based service instances.
	//
	// Args:
	//   - name: The name of the service to start
	//
	// Returns:
	//   - error: Error if the service doesn't exist or fails to start
	StartService(name string) error

	// StopService stops a running service by name.
	// Works for both static services and ServiceClass-based service instances.
	//
	// Args:
	//   - name: The name of the service to stop
	//
	// Returns:
	//   - error: Error if the service doesn't exist or fails to stop
	StopService(name string) error

	// RestartService restarts a service by name (stop followed by start).
	// Works for both static services and ServiceClass-based service instances.
	//
	// Args:
	//   - name: The name of the service to restart
	//
	// Returns:
	//   - error: Error if the service doesn't exist or fails to restart
	RestartService(name string) error

	// Service information and status

	// GetServiceStatus returns the current status of a service.
	//
	// Args:
	//   - name: The name of the service to query
	//
	// Returns:
	//   - *ServiceStatus: Current status information including state, health, and metadata
	//   - error: Error if the service doesn't exist
	GetServiceStatus(name string) (*ServiceStatus, error)

	// GetAllServices returns the status of all services in the system.
	//
	// Returns:
	//   - []ServiceStatus: List of status information for all services
	GetAllServices() []ServiceStatus

	// GetService returns detailed information about a specific service.
	// This provides more comprehensive information than GetServiceStatus.
	//
	// Args:
	//   - name: The name of the service to query
	//
	// Returns:
	//   - *ServiceInstance: Detailed service information including configuration
	//   - error: Error if the service doesn't exist
	GetService(name string) (*ServiceInstance, error)

	// ServiceClass instance creation and deletion (only for ServiceClass-based services)

	// CreateService creates a new service instance from a ServiceClass template.
	// This operation is only applicable to ServiceClass-based services.
	//
	// Args:
	//   - ctx: Context for the operation, including cancellation and timeout
	//   - req: Request containing ServiceClass name, instance name, and args
	//
	// Returns:
	//   - *ServiceInstance: The created service instance information
	//   - error: Error if the ServiceClass doesn't exist or creation fails
	CreateService(ctx context.Context, req CreateServiceInstanceRequest) (*ServiceInstance, error)

	// DeleteService removes a ServiceClass-based service instance.
	// This operation is only applicable to ServiceClass-based services.
	//
	// Args:
	//   - ctx: Context for the operation, including cancellation and timeout
	//   - name: The name of the service instance to delete
	//
	// Returns:
	//   - error: Error if the service doesn't exist or deletion fails
	DeleteService(ctx context.Context, name string) error

	// Event subscriptions

	// SubscribeToStateChanges returns a channel for receiving service state change events.
	// This allows components to react to service lifecycle events.
	//
	// Returns:
	//   - <-chan ServiceStateChangedEvent: Channel that receives state change notifications
	//
	// Note: The returned channel should be consumed to prevent blocking the event system.
	SubscribeToStateChanges() <-chan ServiceStateChangedEvent

	// SubscribeToServiceInstanceEvents returns a channel for receiving ServiceClass instance events.
	// This provides notifications specific to ServiceClass-based service instances.
	//
	// Returns:
	//   - <-chan ServiceInstanceEvent: Channel that receives instance-specific events
	//
	// Note: The returned channel should be consumed to prevent blocking the event system.
	SubscribeToServiceInstanceEvents() <-chan ServiceInstanceEvent

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
)

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

	// Outputs contains the resolved outputs from the ServiceClass outputs definition.
	// Only populated for ServiceClass-based services that have outputs configured.
	Outputs map[string]interface{} `json:"outputs,omitempty"`
}

// ServiceListResponse represents a list of services in API responses.
// This is used by endpoints that return multiple service status information.
type ServiceListResponse struct {
	// Services contains the list of service status information
	Services []ServiceStatus `json:"services"`
}
