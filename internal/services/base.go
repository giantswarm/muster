package services

import (
	"sync"
)

// BaseService provides a base implementation of the Service interface
// that other services can embed to avoid reimplementing common functionality
type BaseService struct {
	mu            sync.RWMutex
	name          string
	serviceType   ServiceType
	dependencies  []string
	state         ServiceState
	health        HealthStatus
	lastError     error
	stateChangeCb StateChangeCallback
}

// NewBaseService creates a new base service
func NewBaseService(name string, serviceType ServiceType, dependencies []string) *BaseService {
	return &BaseService{
		name:         name,
		serviceType:  serviceType,
		dependencies: dependencies,
		state:        StateUnknown,
		health:       HealthUnknown,
	}
}

// GetName returns the service name
func (b *BaseService) GetName() string {
	return b.name
}

// GetType returns the service type
func (b *BaseService) GetType() ServiceType {
	return b.serviceType
}

// GetDependencies returns the service dependencies
func (b *BaseService) GetDependencies() []string {
	return b.dependencies
}

// GetState returns the current state
func (b *BaseService) GetState() ServiceState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

// GetHealth returns the current health status
func (b *BaseService) GetHealth() HealthStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.health
}

// GetLastError returns the last error
func (b *BaseService) GetLastError() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastError
}

// SetStateChangeCallback sets the state change callback
func (b *BaseService) SetStateChangeCallback(callback StateChangeCallback) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stateChangeCb = callback
}

// UpdateState updates the service state and notifies the callback
func (b *BaseService) UpdateState(newState ServiceState, health HealthStatus, err error) {
	b.mu.Lock()
	oldState := b.state
	b.state = newState
	b.health = health
	b.lastError = err
	callback := b.stateChangeCb
	b.mu.Unlock()

	// Call the callback outside of the lock to avoid deadlocks
	if callback != nil && oldState != newState {
		callback(b.name, oldState, newState, health, err)
	}
}

// UpdateHealth updates just the health status
func (b *BaseService) UpdateHealth(health HealthStatus) {
	b.mu.Lock()
	oldHealth := b.health
	b.health = health
	state := b.state
	err := b.lastError
	callback := b.stateChangeCb
	b.mu.Unlock()

	// Notify if health changed
	if callback != nil && oldHealth != health {
		callback(b.name, state, state, health, err)
	}
}

// UpdateError updates the error state
func (b *BaseService) UpdateError(err error) {
	b.mu.Lock()
	b.lastError = err
	state := b.state
	health := b.health
	callback := b.stateChangeCb
	b.mu.Unlock()

	// Notify about error
	if callback != nil && err != nil {
		callback(b.name, state, state, health, err)
	}
}
