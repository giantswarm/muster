package serviceclass

import (
	"sync"
	"time"

	"muster/internal/api"
)

// ServiceInstanceState provides state management for service instances
// This is the only part that remains local to the serviceclass package
type ServiceInstanceState struct {
	// In-memory state
	instances map[string]*api.ServiceInstance // ID -> instance

	// Synchronization
	mu *sync.RWMutex
}

// NewServiceInstanceState creates a new service instance state manager
func NewServiceInstanceState() *ServiceInstanceState {
	return &ServiceInstanceState{
		instances: make(map[string]*api.ServiceInstance),
		mu:        &sync.RWMutex{},
	}
}

// CreateInstance creates a new service instance
func (s *ServiceInstanceState) CreateInstance(name, serviceClassName, serviceClassType string, parameters map[string]interface{}) *api.ServiceInstance {
	s.mu.Lock()
	defer s.mu.Unlock()

	instance := &api.ServiceInstance{
		Name:                 name,
		ServiceClassName:     serviceClassName,
		ServiceClassType:     serviceClassType,
		State:                api.StateUnknown,
		Health:               api.HealthUnknown,
		Parameters:           parameters,
		ServiceData:          make(map[string]interface{}),
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
		HealthCheckFailures:  0,
		HealthCheckSuccesses: 0,
		Dependencies:         []string{},
		Enabled:              true,
	}

	s.instances[name] = instance

	return instance
}

// GetInstance retrieves a service instance by ID
func (s *ServiceInstanceState) GetInstance(name string) (*api.ServiceInstance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instance, exists := s.instances[name]
	return instance, exists
}

// UpdateInstanceState updates the state of a service instance
func (s *ServiceInstanceState) UpdateInstanceState(name string, state api.ServiceState, health api.HealthStatus, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if instance, exists := s.instances[name]; exists {
		instance.State = state
		instance.Health = health
		if err != nil {
			instance.LastError = err.Error()
		} else {
			instance.LastError = ""
		}
		instance.UpdatedAt = time.Now()
	}
}

// DeleteInstance removes a service instance
func (s *ServiceInstanceState) DeleteInstance(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.instances[name]; exists {
		delete(s.instances, name)
	}
}

// ListInstances returns all service instances
func (s *ServiceInstanceState) ListInstances() []*api.ServiceInstance {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instances := make([]*api.ServiceInstance, 0, len(s.instances))
	for _, instance := range s.instances {
		instances = append(instances, instance)
	}

	return instances
}
