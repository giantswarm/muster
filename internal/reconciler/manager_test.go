package reconciler

import (
	"context"
	"testing"
	"time"
)

// mockReconciler implements Reconciler for testing.
type mockReconciler struct {
	resourceType    ResourceType
	reconcileCalls  []ReconcileRequest
	reconcileResult ReconcileResult
	reconcileFunc   func(ctx context.Context, req ReconcileRequest) ReconcileResult
}

func (m *mockReconciler) Reconcile(ctx context.Context, req ReconcileRequest) ReconcileResult {
	m.reconcileCalls = append(m.reconcileCalls, req)
	if m.reconcileFunc != nil {
		return m.reconcileFunc(ctx, req)
	}
	return m.reconcileResult
}

func (m *mockReconciler) GetResourceType() ResourceType {
	return m.resourceType
}

// mockChangeDetector implements ChangeDetector for testing.
type mockChangeDetector struct {
	source        ChangeSource
	resourceTypes map[ResourceType]bool
	started       bool
	stopped       bool
	changeChan    chan<- ChangeEvent
}

func newMockChangeDetector() *mockChangeDetector {
	return &mockChangeDetector{
		source:        SourceFilesystem,
		resourceTypes: make(map[ResourceType]bool),
	}
}

func (m *mockChangeDetector) Start(ctx context.Context, changes chan<- ChangeEvent) error {
	m.started = true
	m.changeChan = changes
	return nil
}

func (m *mockChangeDetector) Stop() error {
	m.stopped = true
	return nil
}

func (m *mockChangeDetector) GetSource() ChangeSource {
	return m.source
}

func (m *mockChangeDetector) AddResourceType(resourceType ResourceType) error {
	m.resourceTypes[resourceType] = true
	return nil
}

func (m *mockChangeDetector) RemoveResourceType(resourceType ResourceType) error {
	delete(m.resourceTypes, resourceType)
	return nil
}

func (m *mockChangeDetector) SendEvent(event ChangeEvent) {
	if m.changeChan != nil {
		m.changeChan <- event
	}
}

func TestManager_RegisterReconciler(t *testing.T) {
	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: "/tmp/test",
	}
	manager := NewManager(config)

	reconciler := &mockReconciler{
		resourceType: ResourceTypeMCPServer,
	}

	err := manager.RegisterReconciler(reconciler)
	if err != nil {
		t.Fatalf("failed to register reconciler: %v", err)
	}

	// Registering same type again should fail
	err = manager.RegisterReconciler(reconciler)
	if err == nil {
		t.Error("expected error when registering duplicate reconciler")
	}
}

func TestManager_StartStop(t *testing.T) {
	// Create a temporary directory for the test
	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
		WorkerCount:    1,
	}
	manager := NewManager(config)

	reconciler := &mockReconciler{
		resourceType: ResourceTypeMCPServer,
	}
	manager.RegisterReconciler(reconciler)

	ctx := context.Background()

	// Start the manager
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}

	if !manager.IsRunning() {
		t.Error("expected manager to be running")
	}

	// Stop the manager
	err = manager.Stop()
	if err != nil {
		t.Fatalf("failed to stop manager: %v", err)
	}

	if manager.IsRunning() {
		t.Error("expected manager to be stopped")
	}
}

func TestManager_TriggerReconcile(t *testing.T) {
	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
		WorkerCount:    1,
	}
	manager := NewManager(config)

	reconciled := make(chan ReconcileRequest, 1)
	reconciler := &mockReconciler{
		resourceType:    ResourceTypeMCPServer,
		reconcileResult: ReconcileResult{}, // Success
		reconcileFunc: func(ctx context.Context, req ReconcileRequest) ReconcileResult {
			select {
			case reconciled <- req:
			default:
			}
			return ReconcileResult{}
		},
	}

	manager.RegisterReconciler(reconciler)

	ctx := context.Background()
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Trigger a manual reconcile
	manager.TriggerReconcile(ResourceTypeMCPServer, "test-server", "")

	// Wait for reconciliation
	select {
	case req := <-reconciled:
		if req.Name != "test-server" {
			t.Errorf("expected name 'test-server', got '%s'", req.Name)
		}
		if req.Type != ResourceTypeMCPServer {
			t.Errorf("expected type MCPServer, got %s", req.Type)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for reconciliation")
	}
}

func TestManager_StatusTracking(t *testing.T) {
	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
		WorkerCount:    1,
	}
	manager := NewManager(config)

	reconciler := &mockReconciler{
		resourceType:    ResourceTypeMCPServer,
		reconcileResult: ReconcileResult{}, // Success
	}
	manager.RegisterReconciler(reconciler)

	ctx := context.Background()
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Trigger a reconcile
	manager.TriggerReconcile(ResourceTypeMCPServer, "status-test", "")

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Check status
	status, ok := manager.GetStatus(ResourceTypeMCPServer, "status-test", "")
	if !ok {
		t.Fatal("expected to find status")
	}

	if status.Name != "status-test" {
		t.Errorf("expected name 'status-test', got '%s'", status.Name)
	}

	if status.State != StateSynced {
		t.Errorf("expected state Synced, got %s", status.State)
	}
}

func TestManager_RetryOnError(t *testing.T) {
	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
		WorkerCount:    1,
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond, // Fast backoff for testing
		MaxBackoff:     50 * time.Millisecond,
	}
	manager := NewManager(config)

	callCount := 0
	reconciler := &mockReconciler{
		resourceType: ResourceTypeMCPServer,
		reconcileFunc: func(ctx context.Context, req ReconcileRequest) ReconcileResult {
			callCount++
			if callCount < 3 {
				return ReconcileResult{
					Error:   context.DeadlineExceeded,
					Requeue: true,
				}
			}
			return ReconcileResult{}
		},
	}

	manager.RegisterReconciler(reconciler)

	ctx := context.Background()
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Trigger a reconcile
	manager.TriggerReconcile(ResourceTypeMCPServer, "retry-test", "")

	// Wait for retries
	time.Sleep(500 * time.Millisecond)

	if callCount < 3 {
		t.Errorf("expected at least 3 calls, got %d", callCount)
	}
}

func TestManager_QueueLength(t *testing.T) {
	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
		WorkerCount:    0, // No workers - items stay in queue
	}
	manager := NewManager(config)

	// Manually add items to the queue
	for i := 0; i < 5; i++ {
		manager.queue.Add(ReconcileRequest{
			Type:    ResourceTypeMCPServer,
			Name:    "server-" + string(rune('0'+i)),
			Attempt: 1,
		})
	}

	if manager.GetQueueLength() != 5 {
		t.Errorf("expected queue length 5, got %d", manager.GetQueueLength())
	}
}

func TestManager_Defaults(t *testing.T) {
	config := ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: "/tmp/test",
		// Leave other fields at zero values
	}
	manager := NewManager(config)

	// Check defaults were applied
	if manager.config.WorkerCount != 2 {
		t.Errorf("expected default WorkerCount 2, got %d", manager.config.WorkerCount)
	}
	if manager.config.MaxRetries != 5 {
		t.Errorf("expected default MaxRetries 5, got %d", manager.config.MaxRetries)
	}
	if manager.config.InitialBackoff != time.Second {
		t.Errorf("expected default InitialBackoff 1s, got %v", manager.config.InitialBackoff)
	}
	if manager.config.MaxBackoff != 5*time.Minute {
		t.Errorf("expected default MaxBackoff 5m, got %v", manager.config.MaxBackoff)
	}
	if manager.config.DebounceInterval != 500*time.Millisecond {
		t.Errorf("expected default DebounceInterval 500ms, got %v", manager.config.DebounceInterval)
	}
}

