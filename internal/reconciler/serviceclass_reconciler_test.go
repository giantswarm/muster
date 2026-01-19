package reconciler

import (
	"context"
	"fmt"
	"testing"

	"muster/internal/api"
)

// =============================================================================
// MockServiceClassManager - Mock for ServiceClass management
// =============================================================================

// MockServiceClassManager implements ServiceClassManager for testing.
type MockServiceClassManager struct {
	serviceClasses map[string]*api.ServiceClass
}

// NewMockServiceClassManager creates a new mock ServiceClass manager.
func NewMockServiceClassManager() *MockServiceClassManager {
	return &MockServiceClassManager{
		serviceClasses: make(map[string]*api.ServiceClass),
	}
}

func (m *MockServiceClassManager) ListServiceClasses() []api.ServiceClass {
	result := make([]api.ServiceClass, 0, len(m.serviceClasses))
	for _, sc := range m.serviceClasses {
		result = append(result, *sc)
	}
	return result
}

func (m *MockServiceClassManager) GetServiceClass(name string) (*api.ServiceClass, error) {
	sc, ok := m.serviceClasses[name]
	if !ok {
		return nil, fmt.Errorf("ServiceClass %s not found", name)
	}
	return sc, nil
}

// AddServiceClass adds a ServiceClass to the mock (for test setup).
func (m *MockServiceClassManager) AddServiceClass(sc *api.ServiceClass) {
	m.serviceClasses[sc.Name] = sc
}

// RemoveServiceClass removes a ServiceClass from the mock (for test setup).
func (m *MockServiceClassManager) RemoveServiceClass(name string) {
	delete(m.serviceClasses, name)
}

// =============================================================================
// ServiceClassReconciler Tests
// =============================================================================

func TestServiceClassReconciler_GetResourceType(t *testing.T) {
	mgr := NewMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	if reconciler.GetResourceType() != ResourceTypeServiceClass {
		t.Errorf("expected ResourceTypeServiceClass, got %s", reconciler.GetResourceType())
	}
}

func TestServiceClassReconciler_ReconcileCreate(t *testing.T) {
	mgr := NewMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	// Add a valid ServiceClass
	mgr.AddServiceClass(&api.ServiceClass{
		Name:      "test-serviceclass",
		Available: true,
		ServiceConfig: api.ServiceConfig{
			ServiceType: "test-type",
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: "start-tool"},
				Stop:  api.ToolCall{Tool: "stop-tool"},
			},
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeServiceClass,
		Name:    "test-serviceclass",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
	if result.Requeue {
		t.Error("expected no requeue for successful reconciliation")
	}
}

func TestServiceClassReconciler_ReconcileDelete(t *testing.T) {
	mgr := NewMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	// Do not add ServiceClass - simulate delete scenario
	req := ReconcileRequest{
		Type:    ResourceTypeServiceClass,
		Name:    "deleted-serviceclass",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error for delete: %v", result.Error)
	}
	if result.Requeue {
		t.Error("expected no requeue for delete")
	}
}

func TestServiceClassReconciler_ReconcileValidationFailure_MissingName(t *testing.T) {
	mgr := NewMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	// Add ServiceClass with missing name
	mgr.AddServiceClass(&api.ServiceClass{
		Name: "", // Invalid - name is required
		ServiceConfig: api.ServiceConfig{
			ServiceType: "test-type",
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: "start-tool"},
				Stop:  api.ToolCall{Tool: "stop-tool"},
			},
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeServiceClass,
		Name:    "",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error for missing name")
	}
	if !result.Requeue {
		t.Error("expected requeue on validation failure")
	}
}

func TestServiceClassReconciler_ReconcileValidationFailure_MissingServiceType(t *testing.T) {
	mgr := NewMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	// Add ServiceClass with missing service type
	mgr.AddServiceClass(&api.ServiceClass{
		Name: "test-serviceclass",
		ServiceConfig: api.ServiceConfig{
			ServiceType: "", // Invalid - serviceType is required
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: "start-tool"},
				Stop:  api.ToolCall{Tool: "stop-tool"},
			},
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeServiceClass,
		Name:    "test-serviceclass",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error for missing serviceType")
	}
	if !result.Requeue {
		t.Error("expected requeue on validation failure")
	}
}

func TestServiceClassReconciler_ReconcileValidationFailure_MissingStartTool(t *testing.T) {
	mgr := NewMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	// Add ServiceClass with missing start tool
	mgr.AddServiceClass(&api.ServiceClass{
		Name: "test-serviceclass",
		ServiceConfig: api.ServiceConfig{
			ServiceType: "test-type",
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: ""}, // Invalid - start tool is required
				Stop:  api.ToolCall{Tool: "stop-tool"},
			},
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeServiceClass,
		Name:    "test-serviceclass",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error for missing start tool")
	}
	if !result.Requeue {
		t.Error("expected requeue on validation failure")
	}
}

func TestServiceClassReconciler_ReconcileValidationFailure_MissingStopTool(t *testing.T) {
	mgr := NewMockServiceClassManager()
	reconciler := NewServiceClassReconciler(mgr)

	// Add ServiceClass with missing stop tool
	mgr.AddServiceClass(&api.ServiceClass{
		Name: "test-serviceclass",
		ServiceConfig: api.ServiceConfig{
			ServiceType: "test-type",
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: "start-tool"},
				Stop:  api.ToolCall{Tool: ""}, // Invalid - stop tool is required
			},
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeServiceClass,
		Name:    "test-serviceclass",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error for missing stop tool")
	}
	if !result.Requeue {
		t.Error("expected requeue on validation failure")
	}
}

// =============================================================================
// Status Sync Tests
// =============================================================================

func TestServiceClassReconciler_SyncStatus_ValidServiceClass(t *testing.T) {
	mgr := NewMockServiceClassManager()
	statusUpdater := NewMockStatusUpdater()

	reconciler := NewServiceClassReconciler(mgr).
		WithStatusUpdater(statusUpdater, "default")

	// Add valid ServiceClass
	mgr.AddServiceClass(&api.ServiceClass{
		Name:      "test-serviceclass",
		Available: true,
		ServiceConfig: api.ServiceConfig{
			ServiceType: "test-type",
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: "start-tool"},
				Stop:  api.ToolCall{Tool: "stop-tool"},
			},
		},
	})

	req := ReconcileRequest{
		Type:      ResourceTypeServiceClass,
		Name:      "test-serviceclass",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify status was synced
	if !statusUpdater.GetServiceClassCalled {
		t.Error("expected GetServiceClass to be called for status sync")
	}
	if !statusUpdater.UpdateServiceClassStatusCalled {
		t.Error("expected UpdateServiceClassStatus to be called")
	}

	// Verify status values
	if statusUpdater.LastUpdatedServiceClass == nil {
		t.Fatal("expected LastUpdatedServiceClass to be set")
	}
	if !statusUpdater.LastUpdatedServiceClass.Status.Valid {
		t.Error("expected Valid=true for valid ServiceClass")
	}
	if len(statusUpdater.LastUpdatedServiceClass.Status.ValidationErrors) != 0 {
		t.Errorf("expected no validation errors, got %v", statusUpdater.LastUpdatedServiceClass.Status.ValidationErrors)
	}
}

func TestServiceClassReconciler_SyncStatus_InvalidServiceClass(t *testing.T) {
	mgr := NewMockServiceClassManager()
	statusUpdater := NewMockStatusUpdater()

	reconciler := NewServiceClassReconciler(mgr).
		WithStatusUpdater(statusUpdater, "default")

	// Add invalid ServiceClass (missing stop tool)
	mgr.AddServiceClass(&api.ServiceClass{
		Name: "test-serviceclass",
		ServiceConfig: api.ServiceConfig{
			ServiceType: "test-type",
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: "start-tool"},
				Stop:  api.ToolCall{Tool: ""}, // Invalid
			},
		},
	})

	req := ReconcileRequest{
		Type:      ResourceTypeServiceClass,
		Name:      "test-serviceclass",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	_ = reconciler.Reconcile(ctx, req)

	// Verify status was synced with validation errors
	if !statusUpdater.UpdateServiceClassStatusCalled {
		t.Error("expected UpdateServiceClassStatus to be called")
	}

	if statusUpdater.LastUpdatedServiceClass == nil {
		t.Fatal("expected LastUpdatedServiceClass to be set")
	}
	if statusUpdater.LastUpdatedServiceClass.Status.Valid {
		t.Error("expected Valid=false for invalid ServiceClass")
	}
	if len(statusUpdater.LastUpdatedServiceClass.Status.ValidationErrors) == 0 {
		t.Error("expected validation errors to be set")
	}
}

func TestServiceClassReconciler_SyncStatus_ExtractReferencedTools(t *testing.T) {
	mgr := NewMockServiceClassManager()
	statusUpdater := NewMockStatusUpdater()

	reconciler := NewServiceClassReconciler(mgr).
		WithStatusUpdater(statusUpdater, "default")

	// Add ServiceClass with all lifecycle tools
	mgr.AddServiceClass(&api.ServiceClass{
		Name: "test-serviceclass",
		ServiceConfig: api.ServiceConfig{
			ServiceType: "test-type",
			LifecycleTools: api.LifecycleTools{
				Start:       api.ToolCall{Tool: "start-tool"},
				Stop:        api.ToolCall{Tool: "stop-tool"},
				Restart:     &api.ToolCall{Tool: "restart-tool"},
				HealthCheck: &api.HealthCheckToolCall{Tool: "health-check-tool"},
				Status:      &api.ToolCall{Tool: "status-tool"},
			},
		},
	})

	req := ReconcileRequest{
		Type:      ResourceTypeServiceClass,
		Name:      "test-serviceclass",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	_ = reconciler.Reconcile(ctx, req)

	// Verify referenced tools were extracted
	if statusUpdater.LastUpdatedServiceClass == nil {
		t.Fatal("expected LastUpdatedServiceClass to be set")
	}

	tools := statusUpdater.LastUpdatedServiceClass.Status.ReferencedTools
	if len(tools) != 5 {
		t.Errorf("expected 5 referenced tools, got %d: %v", len(tools), tools)
	}

	// Check all tools are present (sorted alphabetically)
	expectedTools := []string{"health-check-tool", "restart-tool", "start-tool", "status-tool", "stop-tool"}
	for i, expected := range expectedTools {
		if i >= len(tools) || tools[i] != expected {
			t.Errorf("expected tool[%d]=%s, got %v", i, expected, tools)
		}
	}
}

func TestServiceClassReconciler_SyncStatus_NoUpdaterConfigured(t *testing.T) {
	mgr := NewMockServiceClassManager()

	// No status updater configured - should not panic
	reconciler := NewServiceClassReconciler(mgr)

	mgr.AddServiceClass(&api.ServiceClass{
		Name: "test-serviceclass",
		ServiceConfig: api.ServiceConfig{
			ServiceType: "test-type",
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: "start-tool"},
				Stop:  api.ToolCall{Tool: "stop-tool"},
			},
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeServiceClass,
		Name:    "test-serviceclass",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	// Should complete without error even without status updater
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
}

func TestServiceClassReconciler_SyncStatus_GetServiceClassError(t *testing.T) {
	mgr := NewMockServiceClassManager()
	statusUpdater := NewMockStatusUpdater()

	// Simulate error when getting ServiceClass CRD
	statusUpdater.GetServiceClassError = fmt.Errorf("CRD not found")

	reconciler := NewServiceClassReconciler(mgr).
		WithStatusUpdater(statusUpdater, "default")

	mgr.AddServiceClass(&api.ServiceClass{
		Name: "test-serviceclass",
		ServiceConfig: api.ServiceConfig{
			ServiceType: "test-type",
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: "start-tool"},
				Stop:  api.ToolCall{Tool: "stop-tool"},
			},
		},
	})

	req := ReconcileRequest{
		Type:      ResourceTypeServiceClass,
		Name:      "test-serviceclass",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	// Reconciliation should still succeed even if status sync fails
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// GetServiceClass was called but UpdateServiceClassStatus should not be called
	if !statusUpdater.GetServiceClassCalled {
		t.Error("expected GetServiceClass to be called")
	}
	if statusUpdater.UpdateServiceClassStatusCalled {
		t.Error("expected UpdateServiceClassStatus NOT to be called when GetServiceClass fails")
	}
}

func TestServiceClassReconciler_SyncStatus_UpdateError(t *testing.T) {
	mgr := NewMockServiceClassManager()
	statusUpdater := NewMockStatusUpdater()

	// Simulate error when updating status
	statusUpdater.UpdateServiceClassStatusError = fmt.Errorf("update failed")

	reconciler := NewServiceClassReconciler(mgr).
		WithStatusUpdater(statusUpdater, "default")

	mgr.AddServiceClass(&api.ServiceClass{
		Name: "test-serviceclass",
		ServiceConfig: api.ServiceConfig{
			ServiceType: "test-type",
			LifecycleTools: api.LifecycleTools{
				Start: api.ToolCall{Tool: "start-tool"},
				Stop:  api.ToolCall{Tool: "stop-tool"},
			},
		},
	})

	req := ReconcileRequest{
		Type:      ResourceTypeServiceClass,
		Name:      "test-serviceclass",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	// Reconciliation should still succeed even if status update fails
	// (status sync is best-effort)
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Both methods should be called
	if !statusUpdater.GetServiceClassCalled {
		t.Error("expected GetServiceClass to be called")
	}
	if !statusUpdater.UpdateServiceClassStatusCalled {
		t.Error("expected UpdateServiceClassStatus to be called")
	}
}
