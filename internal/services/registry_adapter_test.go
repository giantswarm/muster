package services

import (
	"context"
	"testing"

	"muster/internal/api"
)

// mockStateUpdaterService is a mock service that implements StateUpdater
type mockStateUpdaterService struct {
	name        string
	serviceType ServiceType
	state       ServiceState
	health      HealthStatus
	lastError   error
	callback    StateChangeCallback

	// Track UpdateState calls
	updateStateCalled bool
	updatedState      ServiceState
	updatedHealth     HealthStatus
	updatedError      error
}

func (m *mockStateUpdaterService) GetName() string                               { return m.name }
func (m *mockStateUpdaterService) GetType() ServiceType                          { return m.serviceType }
func (m *mockStateUpdaterService) GetState() ServiceState                        { return m.state }
func (m *mockStateUpdaterService) GetHealth() HealthStatus                       { return m.health }
func (m *mockStateUpdaterService) GetLastError() error                           { return m.lastError }
func (m *mockStateUpdaterService) GetDependencies() []string                     { return nil }
func (m *mockStateUpdaterService) Start(ctx context.Context) error               { return nil }
func (m *mockStateUpdaterService) Stop(ctx context.Context) error                { return nil }
func (m *mockStateUpdaterService) Restart(ctx context.Context) error             { return nil }
func (m *mockStateUpdaterService) SetStateChangeCallback(cb StateChangeCallback) { m.callback = cb }

// UpdateState implements StateUpdater
func (m *mockStateUpdaterService) UpdateState(state ServiceState, health HealthStatus, err error) {
	m.updateStateCalled = true
	m.updatedState = state
	m.updatedHealth = health
	m.updatedError = err
	m.state = state
	m.health = health
	m.lastError = err
}

func TestRegistryAdapter_Get(t *testing.T) {
	registry := NewRegistry()
	adapter := NewRegistryAdapter(registry)

	// Register a service
	svc := &mockStateUpdaterService{
		name:        "test-service",
		serviceType: TypeMCPServer,
		state:       StateWaiting,
		health:      HealthUnknown,
	}
	registry.Register(svc)

	// Get the service through the adapter
	result, exists := adapter.Get("test-service")
	if !exists {
		t.Fatal("Expected service to exist")
	}
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.GetName() != "test-service" {
		t.Errorf("Expected name 'test-service', got '%s'", result.GetName())
	}
}

func TestRegistryAdapter_GetAll(t *testing.T) {
	registry := NewRegistry()
	adapter := NewRegistryAdapter(registry)

	// Register multiple services
	svc1 := &mockStateUpdaterService{name: "service-1", serviceType: TypeMCPServer}
	svc2 := &mockStateUpdaterService{name: "service-2", serviceType: TypeMCPServer}
	registry.Register(svc1)
	registry.Register(svc2)

	all := adapter.GetAll()
	if len(all) != 2 {
		t.Errorf("Expected 2 services, got %d", len(all))
	}
}

func TestServiceInfoAdapter_UpdateState(t *testing.T) {
	registry := NewRegistry()
	adapter := NewRegistryAdapter(registry)

	// Register a service that implements StateUpdater
	svc := &mockStateUpdaterService{
		name:        "test-mcp-server",
		serviceType: TypeMCPServer,
		state:       StateWaiting,
		health:      HealthUnknown,
	}
	registry.Register(svc)

	// Get the service through the adapter
	result, exists := adapter.Get("test-mcp-server")
	if !exists {
		t.Fatal("Expected service to exist")
	}

	// Verify it implements api.StateUpdater
	updater, ok := result.(api.StateUpdater)
	if !ok {
		t.Fatal("Expected serviceInfoAdapter to implement api.StateUpdater")
	}

	// Call UpdateState
	updater.UpdateState(api.StateConnected, api.HealthHealthy, nil)

	// Verify the underlying service was updated
	if !svc.updateStateCalled {
		t.Error("Expected UpdateState to be called on underlying service")
	}
	if svc.updatedState != StateConnected {
		t.Errorf("Expected state StateConnected, got %s", svc.updatedState)
	}
	if svc.updatedHealth != HealthHealthy {
		t.Errorf("Expected health HealthHealthy, got %s", svc.updatedHealth)
	}

	// Verify the state is reflected through the adapter
	if result.GetState() != api.StateConnected {
		t.Errorf("Expected GetState() to return StateConnected, got %s", result.GetState())
	}
}

// mockNonUpdatableService is a service that does NOT implement StateUpdater
type mockNonUpdatableService struct {
	name        string
	serviceType ServiceType
	state       ServiceState
	health      HealthStatus
}

func (m *mockNonUpdatableService) GetName() string                               { return m.name }
func (m *mockNonUpdatableService) GetType() ServiceType                          { return m.serviceType }
func (m *mockNonUpdatableService) GetState() ServiceState                        { return m.state }
func (m *mockNonUpdatableService) GetHealth() HealthStatus                       { return m.health }
func (m *mockNonUpdatableService) GetLastError() error                           { return nil }
func (m *mockNonUpdatableService) GetDependencies() []string                     { return nil }
func (m *mockNonUpdatableService) Start(ctx context.Context) error               { return nil }
func (m *mockNonUpdatableService) Stop(ctx context.Context) error                { return nil }
func (m *mockNonUpdatableService) Restart(ctx context.Context) error             { return nil }
func (m *mockNonUpdatableService) SetStateChangeCallback(cb StateChangeCallback) {}

func TestServiceInfoAdapter_UpdateState_NonUpdatableService(t *testing.T) {
	registry := NewRegistry()
	adapter := NewRegistryAdapter(registry)

	// Register a service that does NOT implement StateUpdater
	svc := &mockNonUpdatableService{
		name:        "non-updatable-service",
		serviceType: TypeMCPServer,
		state:       StateWaiting,
		health:      HealthUnknown,
	}
	registry.Register(svc)

	// Get the service through the adapter
	result, exists := adapter.Get("non-updatable-service")
	if !exists {
		t.Fatal("Expected service to exist")
	}

	// The adapter should still implement api.StateUpdater
	updater, ok := result.(api.StateUpdater)
	if !ok {
		t.Fatal("Expected serviceInfoAdapter to implement api.StateUpdater")
	}

	// Calling UpdateState should not panic (gracefully does nothing)
	updater.UpdateState(api.StateConnected, api.HealthHealthy, nil)

	// State should remain unchanged since the underlying service doesn't support updates
	if result.GetState() != api.StateWaiting {
		t.Errorf("Expected state to remain StateWaiting, got %s", result.GetState())
	}
}
