package services

import (
	"fmt"
	"muster/internal/api"
	"sync"
)

// registry is a simple implementation of ServiceRegistry
type registry struct {
	mu       sync.RWMutex
	services map[string]Service
}

// NewRegistry creates a new service registry
func NewRegistry() ServiceRegistry {
	return &registry{
		services: make(map[string]Service),
	}
}

// Register adds a service to the registry
func (r *registry) Register(service Service) error {
	if service == nil {
		return fmt.Errorf("cannot register nil service")
	}

	name := service.GetName()
	if name == "" {
		return fmt.Errorf("service has empty name")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.services[name]; exists {
		return fmt.Errorf("service %s already registered", name)
	}

	r.services[name] = service
	return nil
}

// Unregister removes a service from the registry
func (r *registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.services[name]; !exists {
		return api.NewServiceNotFoundError(name)
	}

	delete(r.services, name)
	return nil
}

// Get returns a service by name
func (r *registry) Get(name string) (Service, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services[name]
	return service, exists
}

// GetAll returns all registered services
func (r *registry) GetAll() []Service {
	r.mu.RLock()
	defer r.mu.RUnlock()

	services := make([]Service, 0, len(r.services))
	for _, service := range r.services {
		services = append(services, service)
	}
	return services
}

// GetByType returns all services of a specific type
func (r *registry) GetByType(serviceType ServiceType) []Service {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var services []Service
	for _, service := range r.services {
		if service.GetType() == serviceType {
			services = append(services, service)
		}
	}
	return services
}
