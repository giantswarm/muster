package services

import (
	"context"
	"time"

	"muster/internal/api"
)

// Use API package types instead of duplicating them
type ServiceState = api.ServiceState
type HealthStatus = api.HealthStatus

const (
	StateUnknown  = api.StateUnknown
	StateWaiting  = api.StateWaiting
	StateStarting = api.StateStarting
	StateRunning  = api.StateRunning
	StateStopping = api.StateStopping
	StateStopped  = api.StateStopped
	StateFailed   = api.StateFailed
	StateRetrying = api.StateRetrying
)

const (
	HealthUnknown   = api.HealthUnknown
	HealthHealthy   = api.HealthHealthy
	HealthUnhealthy = api.HealthUnhealthy
	HealthChecking  = api.HealthChecking
)

// ServiceType represents the type of service
type ServiceType string

const (
	TypeMCPServer ServiceType = "MCPServer"
)

// Service is the core interface that all services must implement
type Service interface {
	// Lifecycle management
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error

	// State management
	GetState() ServiceState
	GetHealth() HealthStatus
	GetLastError() error

	// Service metadata
	GetName() string
	GetType() ServiceType
	GetDependencies() []string

	// State change notifications
	// The service should call this callback when its state changes
	SetStateChangeCallback(callback StateChangeCallback)
}

// StateChangeCallback is called when a service's state changes
type StateChangeCallback func(name string, oldState, newState ServiceState, health HealthStatus, err error)

// StateUpdater is an optional interface for services that allow external state updates
// This is used by the orchestrator to set services to StateWaiting when dependencies fail
type StateUpdater interface {
	UpdateState(state ServiceState, health HealthStatus, err error)
}

// ServiceDataProvider is an optional interface for services that expose additional data
type ServiceDataProvider interface {
	// GetServiceData returns service-specific data that can be accessed via the API layer
	// This data should not be stored in the state store
	GetServiceData() map[string]interface{}
}

// HealthChecker is an optional interface for services that support health checking
type HealthChecker interface {
	// CheckHealth performs a health check and returns the current health status
	CheckHealth(ctx context.Context) (HealthStatus, error)

	// GetHealthCheckInterval returns the interval at which health checks should be performed
	GetHealthCheckInterval() time.Duration
}

// ServiceRegistry manages all registered services
type ServiceRegistry interface {
	// Register adds a service to the registry
	Register(service Service) error

	// Unregister removes a service from the registry
	Unregister(name string) error

	// Get returns a service by name
	Get(name string) (Service, bool)

	// GetAll returns all registered services
	GetAll() []Service

	// GetByType returns all services of a specific type
	GetByType(serviceType ServiceType) []Service
}

// ServiceManager orchestrates service lifecycle
type ServiceManager interface {
	// Start starts a service by name
	StartService(ctx context.Context, name string) error

	// Stop stops a service by name
	StopService(ctx context.Context, name string) error

	// Restart restarts a service by name
	RestartService(ctx context.Context, name string) error

	// StartAll starts all registered services respecting dependencies
	StartAll(ctx context.Context) error

	// StopAll stops all registered services
	StopAll(ctx context.Context) error

	// GetServiceState returns the current state of a service
	GetServiceState(name string) (ServiceState, HealthStatus, error)
}
