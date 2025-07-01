package api

import (
	"context"
	"fmt"
)

// OrchestratorAPI defines the interface for orchestrating service lifecycle management.
// It provides methods to start, stop, restart services, check their status, and subscribe
// to state change events. The orchestrator acts as a high-level controller that coordinates
// service operations through the underlying service management infrastructure.
type OrchestratorAPI interface {
	// StartService initiates the startup process for the specified service.
	// It returns an error if the service cannot be started or if the service manager
	// is not available.
	//
	// Args:
	//   - name: The unique name of the service to start
	//
	// Returns:
	//   - error: nil on success, or an error describing why the service could not be started
	StartService(name string) error

	// StopService initiates the shutdown process for the specified service.
	// It returns an error if the service cannot be stopped or if the service manager
	// is not available.
	//
	// Args:
	//   - name: The unique name of the service to stop
	//
	// Returns:
	//   - error: nil on success, or an error describing why the service could not be stopped
	StopService(name string) error

	// RestartService performs a stop followed by a start operation on the specified service.
	// This is equivalent to calling StopService followed by StartService, but may be
	// implemented more efficiently by the underlying service manager.
	//
	// Args:
	//   - name: The unique name of the service to restart
	//
	// Returns:
	//   - error: nil on success, or an error describing why the service could not be restarted
	RestartService(name string) error

	// GetServiceStatus retrieves the current status and metadata for a specific service.
	// The returned ServiceStatus includes state, health, error information, and metadata.
	//
	// Args:
	//   - name: The unique name of the service to query
	//
	// Returns:
	//   - *ServiceStatus: Current status of the service, or nil if service not found
	//   - error: nil on success, or an error if the service could not be found or queried
	GetServiceStatus(name string) (*ServiceStatus, error)

	// GetAllServices returns the status of all registered services in the system.
	// This provides a snapshot of the current state of all services managed by
	// the orchestrator.
	//
	// Returns:
	//   - []ServiceStatus: Slice containing status of all services (empty if none exist)
	GetAllServices() []ServiceStatus

	// SubscribeToStateChanges returns a channel that receives service state change events.
	// Clients can listen to this channel to be notified when any service changes state.
	// The channel will be closed if the service manager becomes unavailable.
	//
	// Returns:
	//   - <-chan ServiceStateChangedEvent: Read-only channel for receiving state change events
	SubscribeToStateChanges() <-chan ServiceStateChangedEvent
}

// orchestratorAPI is the concrete implementation of OrchestratorAPI that delegates
// operations to registered service management handlers through the API layer.
// It follows the service locator pattern to decouple the orchestrator from
// specific service management implementations.
type orchestratorAPI struct {
	// No fields - uses handlers from registry to maintain loose coupling
}

// NewOrchestratorAPI creates a new orchestrator API instance.
// The returned instance uses the API service locator pattern to access
// service management functionality without direct coupling to implementations.
//
// Returns:
//   - OrchestratorAPI: A new orchestrator API instance
func NewOrchestratorAPI() OrchestratorAPI {
	return &orchestratorAPI{}
}

// StartService starts a specific service by delegating to the registered service manager.
// It validates that a service manager is available before attempting the operation.
func (a *orchestratorAPI) StartService(name string) error {
	handler := GetServiceManager()
	if handler == nil {
		return fmt.Errorf("service manager not registered")
	}
	return handler.StartService(name)
}

// StopService stops a specific service by delegating to the registered service manager.
// It validates that a service manager is available before attempting the operation.
func (a *orchestratorAPI) StopService(name string) error {
	handler := GetServiceManager()
	if handler == nil {
		return fmt.Errorf("service manager not registered")
	}
	return handler.StopService(name)
}

// RestartService restarts a specific service by delegating to the registered service manager.
// It validates that a service manager is available before attempting the operation.
func (a *orchestratorAPI) RestartService(name string) error {
	handler := GetServiceManager()
	if handler == nil {
		return fmt.Errorf("service manager not registered")
	}
	return handler.RestartService(name)
}

// SubscribeToStateChanges returns a channel for receiving service state change events.
// If no service manager is registered, it returns a closed channel to prevent blocking.
func (a *orchestratorAPI) SubscribeToStateChanges() <-chan ServiceStateChangedEvent {
	handler := GetServiceManager()
	if handler == nil {
		// Return a closed channel if no handler is registered
		ch := make(chan ServiceStateChangedEvent)
		close(ch)
		return ch
	}
	return handler.SubscribeToStateChanges()
}

// GetServiceStatus retrieves the current status of a specific service.
// It queries the service registry to find the service and constructs a comprehensive
// status report including state, health, errors, and metadata.
func (a *orchestratorAPI) GetServiceStatus(name string) (*ServiceStatus, error) {
	registry := GetServiceRegistry()
	if registry == nil {
		return nil, fmt.Errorf("service registry not registered")
	}

	service, exists := registry.Get(name)
	if !exists {
		return nil, fmt.Errorf("service %s not found", name)
	}

	status := &ServiceStatus{
		Name:        service.GetName(),
		ServiceType: string(service.GetType()),
		State:       service.GetState(),
		Health:      service.GetHealth(),
	}

	// Add error if present
	if err := service.GetLastError(); err != nil {
		status.Error = err.Error()
	}

	// Add metadata if available
	if data := service.GetServiceData(); data != nil {
		status.Metadata = data
	}

	return status, nil
}

// ListServices returns the status of all registered services.
// This method provides a comprehensive view of the entire service ecosystem,
// including their current states, health status, and any error conditions.
func (a *orchestratorAPI) ListServices(ctx context.Context) ([]*ServiceStatus, error) {
	registry := GetServiceRegistry()
	if registry == nil {
		return nil, fmt.Errorf("service registry not registered")
	}

	allServices := registry.GetAll()
	statuses := make([]*ServiceStatus, 0, len(allServices))

	for _, service := range allServices {
		status := &ServiceStatus{
			Name:        service.GetName(),
			ServiceType: string(service.GetType()),
			State:       service.GetState(),
			Health:      service.GetHealth(),
		}

		// Add error if present
		if err := service.GetLastError(); err != nil {
			status.Error = err.Error()
		}

		// Add metadata if available
		if data := service.GetServiceData(); data != nil {
			status.Metadata = data
		}

		statuses = append(statuses, status)
	}

	return statuses, nil
}

// GetAllServices returns the status of all services as a slice of values rather than pointers.
// This method is maintained for backward compatibility and convenience when pointer semantics
// are not required.
func (a *orchestratorAPI) GetAllServices() []ServiceStatus {
	registry := GetServiceRegistry()
	if registry == nil {
		return nil
	}

	allServices := registry.GetAll()
	statuses := make([]ServiceStatus, 0, len(allServices))

	for _, service := range allServices {
		status := ServiceStatus{
			Name:        service.GetName(),
			ServiceType: string(service.GetType()),
			State:       service.GetState(),
			Health:      service.GetHealth(),
		}

		// Add error if present
		if err := service.GetLastError(); err != nil {
			status.Error = err.Error()
		}

		// Add metadata if available
		if data := service.GetServiceData(); data != nil {
			status.Metadata = data
		}

		statuses = append(statuses, status)
	}

	return statuses
}
