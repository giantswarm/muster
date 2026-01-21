package services

import (
	"muster/internal/api"
)

// RegistryAdapter adapts the ServiceRegistry to implement api.ServiceRegistryHandler
type RegistryAdapter struct {
	registry ServiceRegistry
}

// NewRegistryAdapter creates a new registry adapter
func NewRegistryAdapter(r ServiceRegistry) *RegistryAdapter {
	return &RegistryAdapter{registry: r}
}

// Get returns a service by name
func (r *RegistryAdapter) Get(name string) (api.ServiceInfo, bool) {
	svc, exists := r.registry.Get(name)
	if !exists {
		return nil, false
	}
	return &serviceInfoAdapter{service: svc}, true
}

// GetAll returns all registered services
func (r *RegistryAdapter) GetAll() []api.ServiceInfo {
	services := r.registry.GetAll()
	result := make([]api.ServiceInfo, 0, len(services))
	for _, svc := range services {
		result = append(result, &serviceInfoAdapter{service: svc})
	}
	return result
}

// GetByType returns all services of a specific type
func (r *RegistryAdapter) GetByType(serviceType api.ServiceType) []api.ServiceInfo {
	services := r.registry.GetByType(ServiceType(serviceType))
	result := make([]api.ServiceInfo, 0, len(services))
	for _, svc := range services {
		result = append(result, &serviceInfoAdapter{service: svc})
	}
	return result
}

// Register registers this adapter with the API package
func (r *RegistryAdapter) Register() {
	api.RegisterServiceRegistry(r)
}

// serviceInfoAdapter adapts a Service to implement api.ServiceInfo
type serviceInfoAdapter struct {
	service Service
}

func (s *serviceInfoAdapter) GetName() string {
	return s.service.GetName()
}

func (s *serviceInfoAdapter) GetType() api.ServiceType {
	return api.ServiceType(s.service.GetType())
}

func (s *serviceInfoAdapter) GetState() api.ServiceState {
	return api.ServiceState(s.service.GetState())
}

func (s *serviceInfoAdapter) GetHealth() api.HealthStatus {
	return api.HealthStatus(s.service.GetHealth())
}

func (s *serviceInfoAdapter) GetLastError() error {
	return s.service.GetLastError()
}

func (s *serviceInfoAdapter) GetServiceData() map[string]interface{} {
	if provider, ok := s.service.(ServiceDataProvider); ok {
		return provider.GetServiceData()
	}
	return nil
}

// UpdateState implements api.StateUpdater by delegating to the underlying service
// if it implements services.StateUpdater. This enables external state updates
// (e.g., from SSO authentication success) to propagate to the actual service.
func (s *serviceInfoAdapter) UpdateState(state api.ServiceState, health api.HealthStatus, err error) {
	if updater, ok := s.service.(StateUpdater); ok {
		updater.UpdateState(ServiceState(state), HealthStatus(health), err)
	}
}
