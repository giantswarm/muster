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

// mockCallbacks tracks calls to register/deregister functions with proper synchronization
type mockCallbacks struct {
	mu            sync.Mutex
	registers     []string
	deregisters   []string
	registerErr   error
	deregisterErr error
	// callbackChan signals when a callback has been invoked
	callbackChan chan struct{}
}

func newMockCallbacks() *mockCallbacks {
	return &mockCallbacks{
		registers:    make([]string, 0),
		deregisters:  make([]string, 0),
		callbackChan: make(chan struct{}, 100), // Buffered to prevent blocking
	}
}

func (m *mockCallbacks) register(ctx context.Context, serverName string) error {
	m.mu.Lock()
	m.registers = append(m.registers, serverName)
	err := m.registerErr
	m.mu.Unlock()

	// Signal that a callback was invoked
	m.callbackChan <- struct{}{}
	return err
}

func (m *mockCallbacks) deregister(serverName string) error {
	m.mu.Lock()
	m.deregisters = append(m.deregisters, serverName)
	err := m.deregisterErr
	m.mu.Unlock()

	// Signal that a callback was invoked
	m.callbackChan <- struct{}{}
	return err
}

func (m *mockCallbacks) isAuthRequired(serverName string) bool {
	// Default: no server is in auth_required state during tests
	return false
}

func (m *mockCallbacks) isSSOBased(serverName string) bool {
	// Default: no server is SSO-based during tests
	return false
}

// waitForCallbacks waits for exactly n callbacks to be invoked.
// Returns an error if the timeout is reached before all callbacks are received.
func (m *mockCallbacks) waitForCallbacks(n int, timeout time.Duration) error {
	for i := 0; i < n; i++ {
		select {
		case <-m.callbackChan:
			// Callback received
		case <-time.After(timeout):
			return fmt.Errorf("timeout waiting for callback %d of %d", i+1, n)
		}
	}
	return nil
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

	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired, callbacks.isSSOBased)

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
	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired, callbacks.isSSOBased)

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
	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired, callbacks.isSSOBased)

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

	// Wait for exactly 1 callback (only the kubernetes MCP event triggers a callback)
	if err := callbacks.waitForCallbacks(1, 5*time.Second); err != nil {
		t.Fatalf("Failed waiting for callbacks: %v", err)
	}

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
			handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired, callbacks.isSSOBased)

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

			// Wait for exactly 1 callback (every MCP event triggers either register or deregister)
			if err := callbacks.waitForCallbacks(1, 5*time.Second); err != nil {
				t.Fatalf("Failed waiting for callbacks: %v", err)
			}

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
	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired, callbacks.isSSOBased)

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

	// Wait for both callbacks (register + deregister)
	if err := callbacks.waitForCallbacks(2, 5*time.Second); err != nil {
		t.Fatalf("Failed waiting for callbacks: %v", err)
	}

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
	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired, callbacks.isSSOBased)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}

	// Close the event channel
	close(provider.eventChan)

	// Wait for handler to stop (with timeout)
	if err := waitForCondition(func() bool { return !handler.IsRunning() }, 5*time.Second); err != nil {
		t.Error("Handler should have stopped when event channel was closed")
	}
}

func TestEventHandler_HandlesContextCancellation(t *testing.T) {
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacks()
	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired, callbacks.isSSOBased)

	ctx, cancel := context.WithCancel(context.Background())

	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}

	// Cancel the context
	cancel()

	// Wait for handler to stop (with timeout)
	if err := waitForCondition(func() bool { return !handler.IsRunning() }, 5*time.Second); err != nil {
		t.Error("Handler should have stopped after context cancellation")
	}

	// Stop should complete without hanging
	err = handler.Stop()
	if err != nil {
		t.Errorf("Stop failed after context cancellation: %v", err)
	}
}

// waitForCondition polls the condition function until it returns true or timeout is reached.
// This is used to wait for asynchronous state changes without using time.Sleep.
func waitForCondition(condition func() bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return nil
		}
		// Use a very short sleep for polling - this is not the same as using sleep
		// to wait for an unknown duration. We're actively polling a condition.
		time.Sleep(1 * time.Millisecond)
	}
	return fmt.Errorf("condition not met within timeout")
}

// mockCallbacksWithSSO extends mockCallbacks with SSO server tracking
type mockCallbacksWithSSO struct {
	*mockCallbacks
	ssoServers map[string]bool
	mu         sync.Mutex
}

func newMockCallbacksWithSSO() *mockCallbacksWithSSO {
	return &mockCallbacksWithSSO{
		mockCallbacks: newMockCallbacks(),
		ssoServers:    make(map[string]bool),
	}
}

func (m *mockCallbacksWithSSO) isSSOBased(serverName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ssoServers[serverName]
}

func (m *mockCallbacksWithSSO) setServerSSO(serverName string, isSSO bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ssoServers[serverName] = isSSO
}

func TestEventHandler_SkipsRegistrationForSSOServers(t *testing.T) {
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacksWithSSO()

	// Mark "sso-server" as SSO-based
	callbacks.setServerSSO("sso-server", true)

	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired, callbacks.isSSOBased)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}
	defer handler.Stop()

	// Send event for SSO-based server becoming healthy
	// This should NOT trigger registration (SSO servers are handled at session level)
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "sso-server",
		ServiceType: "MCPServer",
		OldState:    "waiting",
		NewState:    "connected",
		Health:      "healthy",
	})

	// Send event for non-SSO server becoming healthy
	// This SHOULD trigger registration
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "regular-server",
		ServiceType: "MCPServer",
		OldState:    "stopped",
		NewState:    "running",
		Health:      "healthy",
	})

	// Wait for exactly 1 callback (only regular-server triggers registration)
	// The SSO event is skipped, so we only wait for the regular-server callback.
	// Since events are processed in order, when we receive the regular-server callback,
	// we know the SSO event was already processed (and skipped).
	if err := callbacks.waitForCallbacks(1, 5*time.Second); err != nil {
		t.Fatalf("Failed waiting for callbacks: %v", err)
	}

	// Should have only 1 register call (only the regular-server, not the sso-server)
	if callbacks.getRegisterCount() != 1 {
		t.Errorf("Expected 1 register call, got %d", callbacks.getRegisterCount())
	}

	registered := callbacks.getRegisteredServers()
	if len(registered) != 1 || registered[0] != "regular-server" {
		t.Errorf("Expected ['regular-server'] to be registered, got %v", registered)
	}

	// SSO server should not be registered
	for _, name := range registered {
		if name == "sso-server" {
			t.Error("SSO server should not have been registered globally")
		}
	}
}

func TestEventHandler_SSOServerDeregistration(t *testing.T) {
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacksWithSSO()

	// Mark "sso-server" as SSO-based
	callbacks.setServerSSO("sso-server", true)

	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired, callbacks.isSSOBased)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}
	defer handler.Stop()

	// SSO server that fails should still trigger deregistration
	// (to clean up any stale state)
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "sso-server",
		ServiceType: "MCPServer",
		OldState:    "connected",
		NewState:    "failed",
		Health:      "unhealthy",
	})

	// Wait for 1 deregister callback
	if err := callbacks.waitForCallbacks(1, 5*time.Second); err != nil {
		t.Fatalf("Failed waiting for callbacks: %v", err)
	}

	// Should have 1 deregister call
	if callbacks.getDeregisterCount() != 1 {
		t.Errorf("Expected 1 deregister call, got %d", callbacks.getDeregisterCount())
	}

	deregistered := callbacks.getDeregisteredServers()
	if len(deregistered) != 1 || deregistered[0] != "sso-server" {
		t.Errorf("Expected ['sso-server'] to be deregistered, got %v", deregistered)
	}
}

// TestEventHandler_Issue318_SSOTokenForwardingRegistrationFailure is a regression test
// for GitHub issue #318: "SSO token forwarding succeeds but MCP server registration
// fails with 'no MCP client available'"
//
// Bug scenario:
//  1. MCPServerService starts and tries to connect to an MCP server with forwardToken: true
//  2. Server returns 401 (requires authentication)
//  3. MCPServerService stops initialization - NO MCP client is created
//  4. Server is registered in "pending auth" state (StatusAuthRequired)
//  5. User authenticates, SSO token forwarding succeeds at session level
//  6. notifyMCPServerConnected() updates service state to "connected" + "healthy"
//  7. EventHandler receives state change and tries to register the server globally
//  8. BUG: registerSingleServer() fails with "no MCP client available" because
//     MCPServerService never created one (it stopped at step 3)
//
// Fix: EventHandler now checks if the server is SSO-based (forwardToken or tokenExchange)
// and skips global registration for such servers. SSO servers are handled at the
// session level, not globally.
func TestEventHandler_Issue318_SSOTokenForwardingRegistrationFailure(t *testing.T) {
	provider := newMockOrchestratorAPI()

	// Track whether SSO server's register was called
	ssoRegisterCalled := false
	var ssoRegisterError error

	// Channel to signal when a callback completes (for synchronization)
	callbackDone := make(chan string, 10)

	// Simulate the bug: registerSingleServer would fail because no MCP client exists
	registerFunc := func(ctx context.Context, serverName string) error {
		if serverName == "sso-forwarding-server" {
			ssoRegisterCalled = true
			// This simulates the error that would occur without the fix
			ssoRegisterError = fmt.Errorf("no MCP client available for %s (service state inconsistent)", serverName)
			callbackDone <- serverName
			return ssoRegisterError
		}
		callbackDone <- serverName
		return nil
	}

	deregisterFunc := func(serverName string) error {
		callbackDone <- serverName
		return nil
	}

	// Server is NOT in auth_required state (it was updated to connected after SSO success)
	isAuthRequired := func(serverName string) bool {
		return false
	}

	// Server IS configured for SSO token forwarding
	isSSOBased := func(serverName string) bool {
		return serverName == "sso-forwarding-server"
	}

	handler := NewEventHandler(provider, registerFunc, deregisterFunc, isAuthRequired, isSSOBased)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}
	defer handler.Stop()

	// Simulate the scenario from the bug:
	// 1. Server was in "waiting" state (pending OAuth)
	// 2. SSO token forwarding succeeded
	// 3. notifyMCPServerConnected() updated state to "connected" + "healthy"
	// 4. This state change event is sent to the EventHandler
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "sso-forwarding-server",
		ServiceType: "MCPServer",
		OldState:    "waiting", // Was waiting for OAuth
		NewState:    "connected",
		Health:      "healthy",
	})

	// Send a probe event to confirm the SSO event was processed.
	// Since events are processed sequentially, when we receive the probe callback,
	// we know the SSO event has already been processed (and should have been skipped).
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "probe-server",
		ServiceType: "MCPServer",
		OldState:    "stopped",
		NewState:    "running",
		Health:      "healthy",
	})

	// Wait for the probe callback
	select {
	case name := <-callbackDone:
		if name != "probe-server" {
			t.Errorf("Expected probe-server callback first, got %s", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for probe callback")
	}

	// CRITICAL ASSERTION: The fix should prevent registerFunc from being called for SSO server
	// Without the fix, registerFunc would be called and would fail with
	// "no MCP client available for sso-forwarding-server (service state inconsistent)"
	if ssoRegisterCalled {
		t.Errorf("Issue #318 regression: registerFunc should NOT have been called for SSO server, "+
			"but it was called and would have failed with: %v", ssoRegisterError)
	}
}

// TestEventHandler_Issue318_NonSSOServerStillRegisters verifies that the fix for
// issue #318 doesn't break normal (non-SSO) server registration.
func TestEventHandler_Issue318_NonSSOServerStillRegisters(t *testing.T) {
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacksWithSSO()

	// Mark only "sso-server" as SSO-based, "normal-server" is not
	callbacks.setServerSSO("sso-server", true)
	// "normal-server" is NOT SSO-based (default)

	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired, callbacks.isSSOBased)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}
	defer handler.Stop()

	// Normal server becomes healthy - should trigger registration
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "normal-server",
		ServiceType: "MCPServer",
		OldState:    "starting",
		NewState:    "running",
		Health:      "healthy",
	})

	// Wait for 1 register callback
	if err := callbacks.waitForCallbacks(1, 5*time.Second); err != nil {
		t.Fatalf("Failed waiting for callbacks: %v", err)
	}

	// Normal server should be registered
	if callbacks.getRegisterCount() != 1 {
		t.Errorf("Expected 1 register call for normal server, got %d", callbacks.getRegisterCount())
	}

	registered := callbacks.getRegisteredServers()
	if len(registered) != 1 || registered[0] != "normal-server" {
		t.Errorf("Expected ['normal-server'] to be registered, got %v", registered)
	}
}

// TestEventHandler_Issue318_MultipleServerTypes tests the scenario where both
// SSO and non-SSO servers emit state change events simultaneously.
func TestEventHandler_Issue318_MultipleServerTypes(t *testing.T) {
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacksWithSSO()

	// Configure server types
	callbacks.setServerSSO("kubernetes", false)    // Regular server
	callbacks.setServerSSO("sso-server-1", true)   // SSO with forwardToken
	callbacks.setServerSSO("sso-server-2", true)   // SSO with tokenExchange
	callbacks.setServerSSO("regular-oauth", false) // OAuth but not SSO

	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, callbacks.isAuthRequired, callbacks.isSSOBased)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := handler.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start handler: %v", err)
	}
	defer handler.Stop()

	// All servers become healthy at roughly the same time
	// (simulating system startup or reconnection scenario)
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "kubernetes",
		ServiceType: "MCPServer",
		OldState:    "starting",
		NewState:    "running",
		Health:      "healthy",
	})

	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "sso-server-1",
		ServiceType: "MCPServer",
		OldState:    "waiting",
		NewState:    "connected",
		Health:      "healthy",
	})

	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "sso-server-2",
		ServiceType: "MCPServer",
		OldState:    "waiting",
		NewState:    "connected",
		Health:      "healthy",
	})

	provider.sendEvent(api.ServiceStateChangedEvent{
		Name:        "regular-oauth",
		ServiceType: "MCPServer",
		OldState:    "starting",
		NewState:    "running",
		Health:      "healthy",
	})

	// Wait for 2 callbacks (only kubernetes and regular-oauth trigger registration)
	// SSO servers are skipped, so we only expect 2 callbacks.
	if err := callbacks.waitForCallbacks(2, 5*time.Second); err != nil {
		t.Fatalf("Failed waiting for callbacks: %v", err)
	}

	// Only non-SSO servers should be registered globally
	// SSO servers (sso-server-1, sso-server-2) should be skipped
	registered := callbacks.getRegisteredServers()

	if callbacks.getRegisterCount() != 2 {
		t.Errorf("Expected 2 register calls (kubernetes, regular-oauth), got %d: %v",
			callbacks.getRegisterCount(), registered)
	}

	// Verify correct servers were registered
	registeredMap := make(map[string]bool)
	for _, name := range registered {
		registeredMap[name] = true
	}

	if !registeredMap["kubernetes"] {
		t.Error("kubernetes server should have been registered")
	}
	if !registeredMap["regular-oauth"] {
		t.Error("regular-oauth server should have been registered")
	}
	if registeredMap["sso-server-1"] {
		t.Error("sso-server-1 should NOT have been registered (SSO with forwardToken)")
	}
	if registeredMap["sso-server-2"] {
		t.Error("sso-server-2 should NOT have been registered (SSO with tokenExchange)")
	}
}
