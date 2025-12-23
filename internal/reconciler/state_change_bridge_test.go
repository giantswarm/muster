package reconciler

import (
	"context"
	"testing"
	"time"

	"muster/internal/api"
)

// mockOrchestratorAPI implements api.OrchestratorAPI for testing.
type mockOrchestratorAPI struct {
	eventChan chan api.ServiceStateChangedEvent
}

func newMockOrchestratorAPI() *mockOrchestratorAPI {
	return &mockOrchestratorAPI{
		eventChan: make(chan api.ServiceStateChangedEvent, 100),
	}
}

func (m *mockOrchestratorAPI) StartService(name string) error      { return nil }
func (m *mockOrchestratorAPI) StopService(name string) error       { return nil }
func (m *mockOrchestratorAPI) RestartService(name string) error    { return nil }
func (m *mockOrchestratorAPI) GetAllServices() []api.ServiceStatus { return nil }
func (m *mockOrchestratorAPI) GetServiceStatus(name string) (*api.ServiceStatus, error) {
	return nil, nil
}
func (m *mockOrchestratorAPI) SubscribeToStateChanges() <-chan api.ServiceStateChangedEvent {
	return m.eventChan
}

func (m *mockOrchestratorAPI) sendEvent(event api.ServiceStateChangedEvent) {
	m.eventChan <- event
}

func (m *mockOrchestratorAPI) close() {
	close(m.eventChan)
}

func TestStateChangeBridge_StartStop(t *testing.T) {
	mockOrch := newMockOrchestratorAPI()

	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
	}
	manager := NewManager(config)

	bridge := NewStateChangeBridge(mockOrch, manager, "default")

	ctx := context.Background()

	// Start the bridge
	err := bridge.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start bridge: %v", err)
	}

	if !bridge.IsRunning() {
		t.Error("expected bridge to be running")
	}

	// Start again should be idempotent
	err = bridge.Start(ctx)
	if err != nil {
		t.Fatalf("second start should be idempotent: %v", err)
	}

	// Stop the bridge
	err = bridge.Stop()
	if err != nil {
		t.Fatalf("failed to stop bridge: %v", err)
	}

	if bridge.IsRunning() {
		t.Error("expected bridge to be stopped")
	}

	// Stop again should be idempotent
	err = bridge.Stop()
	if err != nil {
		t.Fatalf("second stop should be idempotent: %v", err)
	}
}

func TestStateChangeBridge_TriggersReconcileOnStateChange(t *testing.T) {
	mockOrch := newMockOrchestratorAPI()

	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
		WorkerCount:    0, // No workers - items stay in queue for verification
	}
	manager := NewManager(config)

	// Register a mock reconciler so the resource type is enabled
	reconciler := &mockReconciler{resourceType: ResourceTypeMCPServer}
	if err := manager.RegisterReconciler(reconciler); err != nil {
		t.Fatalf("failed to register reconciler: %v", err)
	}

	bridge := NewStateChangeBridge(mockOrch, manager, "default")

	ctx := context.Background()
	err := bridge.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start bridge: %v", err)
	}
	defer func() { _ = bridge.Stop() }()

	// Send a state change event
	mockOrch.sendEvent(api.ServiceStateChangedEvent{
		Name:        "test-server",
		ServiceType: "MCPServer",
		OldState:    "starting",
		NewState:    "running",
		Health:      "healthy",
	})

	// Wait a bit for the event to be processed
	time.Sleep(100 * time.Millisecond)

	// Check that a reconcile request was queued
	if manager.GetQueueLength() != 1 {
		t.Errorf("expected queue length 1, got %d", manager.GetQueueLength())
	}
}

func TestStateChangeBridge_IgnoresNonMCPServerEvents(t *testing.T) {
	mockOrch := newMockOrchestratorAPI()

	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
		WorkerCount:    0, // No workers - items stay in queue for verification
	}
	manager := NewManager(config)

	// Register a mock reconciler
	reconciler := &mockReconciler{resourceType: ResourceTypeMCPServer}
	if err := manager.RegisterReconciler(reconciler); err != nil {
		t.Fatalf("failed to register reconciler: %v", err)
	}

	bridge := NewStateChangeBridge(mockOrch, manager, "default")

	ctx := context.Background()
	err := bridge.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start bridge: %v", err)
	}
	defer func() { _ = bridge.Stop() }()

	// Send an event for a non-MCPServer service type
	mockOrch.sendEvent(api.ServiceStateChangedEvent{
		Name:        "some-other-service",
		ServiceType: "SomeOtherType",
		OldState:    "starting",
		NewState:    "running",
		Health:      "healthy",
	})

	// Wait a bit for the event to be processed
	time.Sleep(100 * time.Millisecond)

	// Queue should be empty since the event was for an unknown service type
	if manager.GetQueueLength() != 0 {
		t.Errorf("expected queue length 0 (non-MCPServer ignored), got %d", manager.GetQueueLength())
	}
}

func TestStateChangeBridge_RespectsDisabledResourceTypes(t *testing.T) {
	mockOrch := newMockOrchestratorAPI()

	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
		WorkerCount:    0, // No workers - items stay in queue for verification
		DisabledResourceTypes: map[ResourceType]bool{
			ResourceTypeMCPServer: true, // Disable MCPServer reconciliation
		},
	}
	manager := NewManager(config)

	// Register a mock reconciler (but it's disabled in config)
	reconciler := &mockReconciler{resourceType: ResourceTypeMCPServer}
	if err := manager.RegisterReconciler(reconciler); err != nil {
		t.Fatalf("failed to register reconciler: %v", err)
	}

	bridge := NewStateChangeBridge(mockOrch, manager, "default")

	ctx := context.Background()
	err := bridge.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start bridge: %v", err)
	}
	defer func() { _ = bridge.Stop() }()

	// Send a state change event for MCPServer (which is disabled)
	mockOrch.sendEvent(api.ServiceStateChangedEvent{
		Name:        "test-server",
		ServiceType: "MCPServer",
		OldState:    "starting",
		NewState:    "running",
		Health:      "healthy",
	})

	// Wait a bit for the event to be processed
	time.Sleep(100 * time.Millisecond)

	// Queue should be empty since MCPServer reconciliation is disabled
	if manager.GetQueueLength() != 0 {
		t.Errorf("expected queue length 0 (disabled resource type), got %d", manager.GetQueueLength())
	}
}

func TestStateChangeBridge_HandlesChannelClose(t *testing.T) {
	mockOrch := newMockOrchestratorAPI()

	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
	}
	manager := NewManager(config)

	bridge := NewStateChangeBridge(mockOrch, manager, "default")

	ctx := context.Background()
	err := bridge.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start bridge: %v", err)
	}

	// Close the event channel to simulate orchestrator shutdown
	mockOrch.close()

	// Wait for the bridge to detect the closed channel and stop
	time.Sleep(100 * time.Millisecond)

	// Bridge should no longer be running
	if bridge.IsRunning() {
		t.Error("expected bridge to stop when channel is closed")
	}
}

func TestStateChangeBridge_DefaultNamespace(t *testing.T) {
	mockOrch := newMockOrchestratorAPI()

	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
	}
	manager := NewManager(config)

	// Create bridge with empty namespace - should default to "default"
	bridge := NewStateChangeBridge(mockOrch, manager, "")

	if bridge.namespace != "default" {
		t.Errorf("expected namespace 'default', got '%s'", bridge.namespace)
	}
}

func TestStateChangeBridge_MapServiceTypeToResourceType(t *testing.T) {
	mockOrch := newMockOrchestratorAPI()
	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
	}
	manager := NewManager(config)
	bridge := NewStateChangeBridge(mockOrch, manager, "default")

	tests := []struct {
		serviceType  string
		expectedType ResourceType
	}{
		{"MCPServer", ResourceTypeMCPServer},
		{"SomeOtherType", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.serviceType, func(t *testing.T) {
			result := bridge.mapServiceTypeToResourceType(tt.serviceType)
			if result != tt.expectedType {
				t.Errorf("mapServiceTypeToResourceType(%s) = %s, want %s",
					tt.serviceType, result, tt.expectedType)
			}
		})
	}
}
