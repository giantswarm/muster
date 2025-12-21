package aggregator

import (
	"context"
	"fmt"
	"muster/internal/api"
	"sync"
	"testing"
	"time"
)

// mockOrchestratorAPI implements api.OrchestratorAPI for testing
type mockOrchestratorAPI struct {
	eventChan chan api.ServiceStateChangedEvent
}

func newMockOrchestratorAPI() *mockOrchestratorAPI {
	return &mockOrchestratorAPI{
		eventChan: make(chan api.ServiceStateChangedEvent, 10),
	}
}

// Implement required methods for api.OrchestratorAPI
func (m *mockOrchestratorAPI) StartService(name string) error   { return nil }
func (m *mockOrchestratorAPI) StopService(name string) error    { return nil }
func (m *mockOrchestratorAPI) RestartService(name string) error { return nil }
func (m *mockOrchestratorAPI) GetServiceStatus(name string) (*api.ServiceStatus, error) {
	return nil, nil
}
func (m *mockOrchestratorAPI) GetAllServices() []api.ServiceStatus { return nil }
func (m *mockOrchestratorAPI) SubscribeToStateChanges() <-chan api.ServiceStateChangedEvent {
	return m.eventChan
}

func (m *mockOrchestratorAPI) sendEvent(event api.ServiceStateChangedEvent) {
	m.eventChan <- event
}

// mockCallbacks tracks calls to register/deregister functions
type mockCallbacks struct {
	mu            sync.Mutex
	registers     []string
	deregisters   []string
	registerErr   error
	deregisterErr error
}

func newMockCallbacks() *mockCallbacks {
	return &mockCallbacks{
		registers:   make([]string, 0),
		deregisters: make([]string, 0),
	}
}

func (m *mockCallbacks) register(ctx context.Context, serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.registers = append(m.registers, serverName)
	return m.registerErr
}

func (m *mockCallbacks) deregister(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deregisters = append(m.deregisters, serverName)
	return m.deregisterErr
}

func (m *mockCallbacks) isAuthRequired(serverName string) bool {
	// Default: no server is in auth_required state during tests
	return false
}

func (m *mockCallbacks) getRegisterCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.registers)
}

func (m *mockCallbacks) getDeregisterCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.deregisters)
}

func (m *mockCallbacks) getRegisteredServers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.registers))
	copy(result, m.registers)
	return result
}

func (m *mockCallbacks) getDeregisteredServers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.deregisters))
	copy(result, m.deregisters)
	return result
}

func (m *mockCallbacks) setRegisterError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registerErr = err
}

func (m *mockCallbacks) setDeregisterError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deregisterErr = err
}

func TestEventHandler_NewEventHandler(t *testing.T) {
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacks()

	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired)

	if handler == nil {
		t.Fatal("NewEventHandler returned nil")
	}

	if handler.orchestratorAPI != provider {
		t.Error("OrchestratorAPI not set correctly")
	}

	if handler.IsRunning() {
		t.Error("Handler should not be running initially")
	}
}

func TestEventHandler_StartStop(t *testing.T) {
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacks()
	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Test Start
	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}

	if !handler.IsRunning() {
		t.Error("Handler should be running after Start")
	}

	// Test double start (should not error)
	err = handler.Start(ctx)
	if err != nil {
		t.Errorf("Double start should not error: %v", err)
	}

	// Test Stop
	err = handler.Stop()
	if err != nil {
		t.Fatalf("Failed to stop handler: %v", err)
	}

	if handler.IsRunning() {
		t.Error("Handler should not be running after Stop")
	}

	// Test double stop (should not error)
	err = handler.Stop()
	if err != nil {
		t.Errorf("Double stop should not error: %v", err)
	}
}

func TestEventHandler_FiltersMCPEvents(t *testing.T) {
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacks()
	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}
	defer handler.Stop()

	// Send non-MCP event (should NOT trigger any callbacks)
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "k8s-mc-test",
		ServiceType: "KubeConnection",
		OldState:    "Stopped",
		NewState:    "Running",
		Health:      "Healthy",
	})

	// Send MCP event with healthy state (should trigger register)
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "kubernetes",
		ServiceType: "MCPServer",
		OldState:    "stopped",
		NewState:    "running",
		Health:      "healthy",
	})

	// Send aggregator event (should NOT trigger any callbacks)
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "mcp-aggregator",
		ServiceType: "Aggregator",
		OldState:    "stopped",
		NewState:    "running",
		Health:      "healthy",
	})

	// Give time for events to be processed
	time.Sleep(100 * time.Millisecond)

	// Should have only 1 register call (only the kubernetes event)
	if callbacks.getRegisterCount() != 1 {
		t.Errorf("Expected 1 register call, got %d", callbacks.getRegisterCount())
	}

	if callbacks.getDeregisterCount() != 0 {
		t.Errorf("Expected 0 deregister calls, got %d", callbacks.getDeregisterCount())
	}

	registered := callbacks.getRegisteredServers()
	if len(registered) != 1 || registered[0] != "kubernetes" {
		t.Errorf("Expected ['kubernetes'] to be registered, got %v", registered)
	}
}

func TestEventHandler_HealthBasedRegistration(t *testing.T) {
	testCases := []struct {
		name             string
		oldState         string
		newState         string
		health           string
		expectRegister   bool
		expectDeregister bool
	}{
		{
			name:             "service becomes running and healthy",
			oldState:         "stopped",
			newState:         "running",
			health:           "healthy",
			expectRegister:   true,
			expectDeregister: false,
		},
		{
			name:             "service running but unhealthy",
			oldState:         "stopped",
			newState:         "running",
			health:           "unhealthy",
			expectRegister:   false,
			expectDeregister: true,
		},
		{
			name:             "service running but health unknown",
			oldState:         "stopped",
			newState:         "running",
			health:           "unknown",
			expectRegister:   false,
			expectDeregister: true,
		},
		{
			name:             "healthy service stops",
			oldState:         "running",
			newState:         "stopped",
			health:           "healthy",
			expectRegister:   false,
			expectDeregister: true,
		},
		{
			name:             "service fails",
			oldState:         "running",
			newState:         "failed",
			health:           "unhealthy",
			expectRegister:   false,
			expectDeregister: true,
		},
		{
			name:             "service becomes unhealthy while running",
			oldState:         "running",
			newState:         "running",
			health:           "unhealthy",
			expectRegister:   false,
			expectDeregister: true,
		},
		{
			name:             "service becomes healthy while running",
			oldState:         "running",
			newState:         "running",
			health:           "healthy",
			expectRegister:   true,
			expectDeregister: false,
		},
		{
			name:             "service stays stopped",
			oldState:         "stopped",
			newState:         "stopped",
			health:           "unknown",
			expectRegister:   false,
			expectDeregister: true,
		},
		{
			name:             "service is starting",
			oldState:         "stopped",
			newState:         "starting",
			health:           "unknown",
			expectRegister:   false,
			expectDeregister: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			provider := newMockOrchestratorAPI()
			callbacks := newMockCallbacks()
			handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			err := handler.Start(ctx)
			if err != nil {
				t.Fatalf("Failed to start handler: %v", err)
			}
			defer handler.Stop()

			// Send event
			provider.sendEvent(api.ServiceStateChangedEvent{
				Name:        "kubernetes",
				ServiceType: "MCPServer",
				OldState:    tc.oldState,
				NewState:    tc.newState,
				Health:      tc.health,
			})

			// Give time for event to be processed
			time.Sleep(100 * time.Millisecond)

			expectedRegisters := 0
			if tc.expectRegister {
				expectedRegisters = 1
			}

			expectedDeregisters := 0
			if tc.expectDeregister {
				expectedDeregisters = 1
			}

			if callbacks.getRegisterCount() != expectedRegisters {
				t.Errorf("Expected %d register calls, got %d", expectedRegisters, callbacks.getRegisterCount())
			}

			if callbacks.getDeregisterCount() != expectedDeregisters {
				t.Errorf("Expected %d deregister calls, got %d", expectedDeregisters, callbacks.getDeregisterCount())
			}
		})
	}
}

func TestEventHandler_HandlesErrors(t *testing.T) {
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacks()
	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired)

	// Set errors
	callbacks.setRegisterError(fmt.Errorf("register failed"))
	callbacks.setDeregisterError(fmt.Errorf("deregister failed"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}
	defer handler.Stop()

	// Send event that should trigger register (but will fail)
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "kubernetes",
		ServiceType: "MCPServer",
		OldState:    "stopped",
		NewState:    "running",
		Health:      "healthy",
	})

	// Send event that should trigger deregister (but will fail)
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "muster",
		ServiceType: "MCPServer",
		OldState:    "running",
		NewState:    "stopped",
		Health:      "healthy",
	})

	// Give time for events to be processed
	time.Sleep(100 * time.Millisecond)

	// Handler should still be running despite errors
	if !handler.IsRunning() {
		t.Error("Handler should continue running despite callback errors")
	}

	// Callbacks should have been attempted
	if callbacks.getRegisterCount() != 1 {
		t.Errorf("Expected 1 register attempt, got %d", callbacks.getRegisterCount())
	}

	if callbacks.getDeregisterCount() != 1 {
		t.Errorf("Expected 1 deregister attempt, got %d", callbacks.getDeregisterCount())
	}
}

func TestEventHandler_HandlesChannelClose(t *testing.T) {
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacks()
	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}

	// Close the event channel
	close(provider.eventChan)

	// Give time for handler to detect channel close and stop
	time.Sleep(100 * time.Millisecond)

	// Handler should have stopped itself
	if handler.IsRunning() {
		t.Error("Handler should have stopped when event channel was closed")
	}
}

func TestEventHandler_HandlesContextCancellation(t *testing.T) {
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacks()
	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired)

	ctx, cancel := context.WithCancel(context.Background())

	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}

	// Cancel the context
	cancel()

	// Give time for handler to detect cancellation and stop
	time.Sleep(100 * time.Millisecond)

	// Stop should complete without hanging
	err = handler.Stop()
	if err != nil {
		t.Errorf("Stop failed after context cancellation: %v", err)
	}
}
