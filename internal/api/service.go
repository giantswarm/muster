package api

import (
	"context"
	"time"
)

// ServiceInfo provides information about a service
type ServiceInfo interface {
	GetName() string
	GetType() ServiceType
	GetState() ServiceState
	GetHealth() HealthStatus
	GetLastError() error
	GetServiceData() map[string]interface{}
}

// ServiceRegistryHandler provides access to registered services
type ServiceRegistryHandler interface {
	Get(name string) (ServiceInfo, bool)
	GetAll() []ServiceInfo
	GetByType(serviceType ServiceType) []ServiceInfo
}

// ServiceManagerHandler provides unified management for both static and ServiceClass-based services
type ServiceManagerHandler interface {
	// Unified service lifecycle management (works for both static and ServiceClass-based services)
	StartService(name string) error
	StopService(name string) error
	RestartService(name string) error

	// Service information and status
	GetServiceStatus(name string) (*ServiceStatus, error)
	GetAllServices() []ServiceStatus
	GetService(name string) (*ServiceInstance, error) // Get detailed service info

	// ServiceClass instance creation and deletion (only for ServiceClass-based services)
	CreateService(ctx context.Context, req CreateServiceInstanceRequest) (*ServiceInstance, error)
	DeleteService(ctx context.Context, name string) error

	// Event subscriptions
	SubscribeToStateChanges() <-chan ServiceStateChangedEvent
	SubscribeToServiceInstanceEvents() <-chan ServiceInstanceEvent

	ToolProvider
}

// Service types
type ServiceType string

const (
	TypeMCPServer  ServiceType = "MCPServer"
	TypeAggregator ServiceType = "Aggregator"
)

// ServiceState represents the current state of a service
type ServiceState string

const (
	StateStopped  ServiceState = "stopped"
	StateStarting ServiceState = "starting"
	StateRunning  ServiceState = "running"
	StateStopping ServiceState = "stopping"
	StateError    ServiceState = "error"
	StateFailed   ServiceState = "failed"
	StateUnknown  ServiceState = "unknown"
	StateWaiting  ServiceState = "waiting"
	StateRetrying ServiceState = "retrying"
)

// Event types
type ServiceStateChangedEvent struct {
	Name        string    `json:"name"`
	ServiceType string    `json:"service_type"`
	OldState    string    `json:"old_state"`
	NewState    string    `json:"new_state"`
	Health      string    `json:"health"`
	Error       error     `json:"error,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// API response types
type ServiceStatus struct {
	Name        string                 `json:"name"`
	ServiceType string                 `json:"service_type"`
	State       ServiceState           `json:"state"`
	Health      HealthStatus           `json:"health"`
	Error       string                 `json:"error,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type ServiceListResponse struct {
	Services []ServiceStatus `json:"services"`
}
