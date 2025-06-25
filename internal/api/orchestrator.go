package api

import (
	"context"
	"fmt"
)

// OrchestratorAPI defines the interface for orchestrating services
type OrchestratorAPI interface {
	// Service lifecycle management
	StartService(name string) error
	StopService(name string) error
	RestartService(name string) error

	// Service status
	GetServiceStatus(name string) (*ServiceStatus, error)
	GetAllServices() []ServiceStatus

	// State change events
	SubscribeToStateChanges() <-chan ServiceStateChangedEvent
}

// orchestratorAPI wraps the orchestrator to implement OrchestratorAPI
type orchestratorAPI struct {
	// No fields - uses handlers from registry
}

// NewOrchestratorAPI creates a new API wrapper for the orchestrator
func NewOrchestratorAPI() OrchestratorAPI {
	return &orchestratorAPI{}
}

// StartService starts a specific service
func (a *orchestratorAPI) StartService(name string) error {
	handler := GetServiceManager()
	if handler == nil {
		return fmt.Errorf("service manager not registered")
	}
	return handler.StartService(name)
}

// StopService stops a specific service
func (a *orchestratorAPI) StopService(name string) error {
	handler := GetServiceManager()
	if handler == nil {
		return fmt.Errorf("service manager not registered")
	}
	return handler.StopService(name)
}

// RestartService restarts a specific service
func (a *orchestratorAPI) RestartService(name string) error {
	handler := GetServiceManager()
	if handler == nil {
		return fmt.Errorf("service manager not registered")
	}
	return handler.RestartService(name)
}

// SubscribeToStateChanges returns a channel for receiving service state change events
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

// GetServiceStatus returns the status of a specific service
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

// ListServices returns the status of all services
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

// GetAllServices returns the status of all services
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
