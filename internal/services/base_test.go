package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewBaseService(t *testing.T) {
	name := "test-service"
	serviceType := TypeMCPServer
	dependencies := []string{"dep1", "dep2"}

	base := NewBaseService(name, serviceType, dependencies)

	if base == nil {
		t.Fatal("Expected NewBaseService to return non-nil base service")
	}

	if base.GetName() != name {
		t.Errorf("Expected name %s, got %s", name, base.GetName())
	}

	if base.GetType() != serviceType {
		t.Errorf("Expected type %s, got %s", serviceType, base.GetType())
	}

	if len(base.GetDependencies()) != len(dependencies) {
		t.Errorf("Expected %d dependencies, got %d", len(dependencies), len(base.GetDependencies()))
	}

	for i, dep := range base.GetDependencies() {
		if dep != dependencies[i] {
			t.Errorf("Expected dependency %s at index %d, got %s", dependencies[i], i, dep)
		}
	}

	if base.GetState() != StateUnknown {
		t.Errorf("Expected initial state %s, got %s", StateUnknown, base.GetState())
	}

	if base.GetHealth() != HealthUnknown {
		t.Errorf("Expected initial health %s, got %s", HealthUnknown, base.GetHealth())
	}

	if base.GetLastError() != nil {
		t.Errorf("Expected no initial error, got %v", base.GetLastError())
	}
}

func TestBaseServiceGetters(t *testing.T) {
	name := "getter-test"
	serviceType := TypeMCPServer
	dependencies := []string{"service1", "service2", "service3"}

	base := NewBaseService(name, serviceType, dependencies)

	// Test GetName
	if got := base.GetName(); got != name {
		t.Errorf("GetName() = %s, want %s", got, name)
	}

	// Test GetType
	if got := base.GetType(); got != serviceType {
		t.Errorf("GetType() = %s, want %s", got, serviceType)
	}

	// Test GetDependencies
	deps := base.GetDependencies()
	if len(deps) != len(dependencies) {
		t.Errorf("GetDependencies() returned %d items, want %d", len(deps), len(dependencies))
	}
	for i, dep := range deps {
		if dep != dependencies[i] {
			t.Errorf("GetDependencies()[%d] = %s, want %s", i, dep, dependencies[i])
		}
	}
}

func TestBaseServiceStateManagement(t *testing.T) {
	base := NewBaseService("state-test", TypeMCPServer, nil)

	// Test initial state
	if state := base.GetState(); state != StateUnknown {
		t.Errorf("Initial state = %s, want %s", state, StateUnknown)
	}

	if health := base.GetHealth(); health != HealthUnknown {
		t.Errorf("Initial health = %s, want %s", health, HealthUnknown)
	}

	// Test UpdateState
	testError := errors.New("test error")
	base.UpdateState(StateRunning, HealthHealthy, nil)

	if state := base.GetState(); state != StateRunning {
		t.Errorf("State after update = %s, want %s", state, StateRunning)
	}

	if health := base.GetHealth(); health != HealthHealthy {
		t.Errorf("Health after update = %s, want %s", health, HealthHealthy)
	}

	if err := base.GetLastError(); err != nil {
		t.Errorf("Error after successful update = %v, want nil", err)
	}

	// Test UpdateState with error
	base.UpdateState(StateFailed, HealthUnhealthy, testError)

	if state := base.GetState(); state != StateFailed {
		t.Errorf("State after error update = %s, want %s", state, StateFailed)
	}

	if health := base.GetHealth(); health != HealthUnhealthy {
		t.Errorf("Health after error update = %s, want %s", health, HealthUnhealthy)
	}

	if err := base.GetLastError(); err != testError {
		t.Errorf("Error after error update = %v, want %v", err, testError)
	}
}

func TestBaseServiceUpdateHealth(t *testing.T) {
	base := NewBaseService("health-test", TypeMCPServer, nil)

	// Set initial state
	base.UpdateState(StateRunning, HealthHealthy, nil)

	// Update only health
	base.UpdateHealth(HealthUnhealthy)

	if health := base.GetHealth(); health != HealthUnhealthy {
		t.Errorf("Health after UpdateHealth = %s, want %s", health, HealthUnhealthy)
	}

	// State should remain unchanged
	if state := base.GetState(); state != StateRunning {
		t.Errorf("State after UpdateHealth = %s, want %s", state, StateRunning)
	}
}

func TestBaseServiceUpdateError(t *testing.T) {
	base := NewBaseService("error-test", TypeMCPServer, nil)

	// Set initial state
	base.UpdateState(StateRunning, HealthHealthy, nil)

	// Update only error
	testError := errors.New("new error")
	base.UpdateError(testError)

	if err := base.GetLastError(); err != testError {
		t.Errorf("Error after UpdateError = %v, want %v", err, testError)
	}

	// State and health should remain unchanged
	if state := base.GetState(); state != StateRunning {
		t.Errorf("State after UpdateError = %s, want %s", state, StateRunning)
	}

	if health := base.GetHealth(); health != HealthHealthy {
		t.Errorf("Health after UpdateError = %s, want %s", health, HealthHealthy)
	}
}

func TestBaseServiceStateChangeCallback(t *testing.T) {
	base := NewBaseService("callback-test", TypeMCPServer, nil)

	var callbackCalled bool
	var receivedName string
	var receivedOldState, receivedNewState ServiceState
	var receivedHealth HealthStatus
	var receivedErr error

	callback := func(name string, oldState, newState ServiceState, health HealthStatus, err error) {
		callbackCalled = true
		receivedName = name
		receivedOldState = oldState
		receivedNewState = newState
		receivedHealth = health
		receivedErr = err
	}

	base.SetStateChangeCallback(callback)

	// Test state change triggers callback
	base.UpdateState(StateRunning, HealthHealthy, nil)

	if !callbackCalled {
		t.Error("Expected callback to be called on state change")
	}

	if receivedName != "callback-test" {
		t.Errorf("Callback received name %s, want %s", receivedName, "callback-test")
	}

	if receivedOldState != StateUnknown {
		t.Errorf("Callback received old state %s, want %s", receivedOldState, StateUnknown)
	}

	if receivedNewState != StateRunning {
		t.Errorf("Callback received new state %s, want %s", receivedNewState, StateRunning)
	}

	if receivedHealth != HealthHealthy {
		t.Errorf("Callback received health %s, want %s", receivedHealth, HealthHealthy)
	}

	if receivedErr != nil {
		t.Errorf("Callback received error %v, want nil", receivedErr)
	}

	// Reset callback flag
	callbackCalled = false

	// Test same state doesn't trigger callback
	base.UpdateState(StateRunning, HealthHealthy, nil)

	if callbackCalled {
		t.Error("Expected callback not to be called when state doesn't change")
	}

	// Test health change triggers callback
	callbackCalled = false
	base.UpdateHealth(HealthUnhealthy)

	if !callbackCalled {
		t.Error("Expected callback to be called on health change")
	}

	if receivedHealth != HealthUnhealthy {
		t.Errorf("Callback received health %s, want %s", receivedHealth, HealthUnhealthy)
	}

	// Test error update triggers callback
	callbackCalled = false
	testError := errors.New("test error")
	base.UpdateError(testError)

	if !callbackCalled {
		t.Error("Expected callback to be called on error update")
	}

	if receivedErr != testError {
		t.Errorf("Callback received error %v, want %v", receivedErr, testError)
	}
}

func TestBaseServiceNilCallback(t *testing.T) {
	base := NewBaseService("nil-callback-test", TypeMCPServer, nil)

	// Don't set a callback, ensure no panic on state changes
	base.UpdateState(StateRunning, HealthHealthy, nil)
	base.UpdateHealth(HealthUnhealthy)
	base.UpdateError(errors.New("test error"))

	// If we get here without panic, test passes
}

func TestBaseServiceConcurrentAccess(t *testing.T) {
	base := NewBaseService("concurrent-test", TypeMCPServer, nil)

	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 100

	// Set up callback to count state changes
	var stateChanges int
	var mu sync.Mutex
	base.SetStateChangeCallback(func(name string, oldState, newState ServiceState, health HealthStatus, err error) {
		mu.Lock()
		stateChanges++
		mu.Unlock()
	})

	// Start multiple goroutines performing various operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numOperations; j++ {
				switch j % 5 {
				case 0:
					base.UpdateState(StateRunning, HealthHealthy, nil)
				case 1:
					base.UpdateState(StateStopped, HealthUnknown, nil)
				case 2:
					base.UpdateHealth(HealthUnhealthy)
				case 3:
					base.UpdateError(errors.New("concurrent error"))
				case 4:
					// Read operations
					_ = base.GetState()
					_ = base.GetHealth()
					_ = base.GetLastError()
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify the service is in a valid state
	finalState := base.GetState()
	finalHealth := base.GetHealth()

	// The final state should be one of the states we set
	validStates := map[ServiceState]bool{
		StateRunning: true,
		StateStopped: true,
	}

	if !validStates[finalState] {
		t.Errorf("Final state %s is not one of the expected states", finalState)
	}

	validHealths := map[HealthStatus]bool{
		HealthHealthy:   true,
		HealthUnhealthy: true,
		HealthUnknown:   true,
	}

	if !validHealths[finalHealth] {
		t.Errorf("Final health %s is not one of the expected health statuses", finalHealth)
	}
}

func TestBaseServiceCallbackPanic(t *testing.T) {
	base := NewBaseService("panic-test", TypeMCPServer, nil)

	// Set a callback that panics
	base.SetStateChangeCallback(func(name string, oldState, newState ServiceState, health HealthStatus, err error) {
		panic("callback panic")
	})

	// Ensure the panic is recovered and doesn't crash the program
	defer func() {
		if r := recover(); r != nil {
			// Expected panic, test passes
			return
		}
	}()

	// This should panic
	base.UpdateState(StateRunning, HealthHealthy, nil)

	// If we get here without panic, test fails
	t.Error("Expected panic from callback was not triggered")
}

// TestBaseServiceAsService verifies that BaseService can be embedded in other services
type embeddedService struct {
	*BaseService
	customField string
}

func (e *embeddedService) Start(ctx context.Context) error {
	e.UpdateState(StateStarting, HealthUnknown, nil)
	// Simulate some work
	time.Sleep(10 * time.Millisecond)
	e.UpdateState(StateRunning, HealthHealthy, nil)
	return nil
}

func (e *embeddedService) Stop(ctx context.Context) error {
	e.UpdateState(StateStopping, HealthUnknown, nil)
	// Simulate some work
	time.Sleep(10 * time.Millisecond)
	e.UpdateState(StateStopped, HealthUnknown, nil)
	return nil
}

func (e *embeddedService) Restart(ctx context.Context) error {
	if err := e.Stop(ctx); err != nil {
		return err
	}
	return e.Start(ctx)
}

func TestBaseServiceEmbedding(t *testing.T) {
	embedded := &embeddedService{
		BaseService: NewBaseService("embedded-test", TypeMCPServer, []string{"dep1"}),
		customField: "custom value",
	}

	// Verify it implements Service interface
	var _ Service = embedded

	// Test that embedded methods work
	if name := embedded.GetName(); name != "embedded-test" {
		t.Errorf("GetName() = %s, want %s", name, "embedded-test")
	}

	// Test custom Start implementation
	ctx := context.Background()
	err := embedded.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if state := embedded.GetState(); state != StateRunning {
		t.Errorf("State after Start() = %s, want %s", state, StateRunning)
	}

	// Test custom Stop implementation
	err = embedded.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if state := embedded.GetState(); state != StateStopped {
		t.Errorf("State after Stop() = %s, want %s", state, StateStopped)
	}
}
