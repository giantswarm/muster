package reconciler

import (
	"context"
	"fmt"
	"testing"

	"github.com/giantswarm/muster/internal/api"
)

// =============================================================================
// MockWorkflowManager - Mock for Workflow management
// =============================================================================

// MockWorkflowManager implements WorkflowManager for testing.
type MockWorkflowManager struct {
	workflows map[string]*api.Workflow
}

// NewMockWorkflowManager creates a new mock Workflow manager.
func NewMockWorkflowManager() *MockWorkflowManager {
	return &MockWorkflowManager{
		workflows: make(map[string]*api.Workflow),
	}
}

func (m *MockWorkflowManager) GetWorkflows() []api.Workflow {
	result := make([]api.Workflow, 0, len(m.workflows))
	for _, wf := range m.workflows {
		result = append(result, *wf)
	}
	return result
}

func (m *MockWorkflowManager) GetWorkflow(name string) (*api.Workflow, error) {
	wf, ok := m.workflows[name]
	if !ok {
		return nil, fmt.Errorf("Workflow %s not found", name)
	}
	return wf, nil
}

// AddWorkflow adds a Workflow to the mock (for test setup).
func (m *MockWorkflowManager) AddWorkflow(wf *api.Workflow) {
	m.workflows[wf.Name] = wf
}

// RemoveWorkflow removes a Workflow from the mock (for test setup).
func (m *MockWorkflowManager) RemoveWorkflow(name string) {
	delete(m.workflows, name)
}

// =============================================================================
// WorkflowReconciler Tests
// =============================================================================

func TestWorkflowReconciler_GetResourceType(t *testing.T) {
	mgr := NewMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	if reconciler.GetResourceType() != ResourceTypeWorkflow {
		t.Errorf("expected ResourceTypeWorkflow, got %s", reconciler.GetResourceType())
	}
}

func TestWorkflowReconciler_ReconcileCreate(t *testing.T) {
	mgr := NewMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	// Add a valid Workflow
	mgr.AddWorkflow(&api.Workflow{
		Name:        "test-workflow",
		Description: "Test workflow",
		Available:   true,
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "some-tool"},
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeWorkflow,
		Name:    "test-workflow",
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

func TestWorkflowReconciler_ReconcileDelete(t *testing.T) {
	mgr := NewMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	// Do not add Workflow - simulate delete scenario
	req := ReconcileRequest{
		Type:    ResourceTypeWorkflow,
		Name:    "deleted-workflow",
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

func TestWorkflowReconciler_ReconcileValidationFailure_MissingName(t *testing.T) {
	mgr := NewMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	// Add Workflow with missing name
	mgr.AddWorkflow(&api.Workflow{
		Name: "", // Invalid - name is required
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "some-tool"},
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeWorkflow,
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

func TestWorkflowReconciler_ReconcileValidationFailure_NoSteps(t *testing.T) {
	mgr := NewMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	// Add Workflow with no steps
	mgr.AddWorkflow(&api.Workflow{
		Name:  "test-workflow",
		Steps: []api.WorkflowStep{}, // Invalid - at least one step required
	})

	req := ReconcileRequest{
		Type:    ResourceTypeWorkflow,
		Name:    "test-workflow",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error for missing steps")
	}
	if !result.Requeue {
		t.Error("expected requeue on validation failure")
	}
}

func TestWorkflowReconciler_ReconcileValidationFailure_MissingStepID(t *testing.T) {
	mgr := NewMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	// Add Workflow with step missing ID
	mgr.AddWorkflow(&api.Workflow{
		Name: "test-workflow",
		Steps: []api.WorkflowStep{
			{ID: "", Tool: "some-tool"}, // Invalid - step ID is required
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeWorkflow,
		Name:    "test-workflow",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error for missing step ID")
	}
	if !result.Requeue {
		t.Error("expected requeue on validation failure")
	}
}

func TestWorkflowReconciler_ReconcileValidationFailure_MissingStepTool(t *testing.T) {
	mgr := NewMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	// Add Workflow with step missing tool
	mgr.AddWorkflow(&api.Workflow{
		Name: "test-workflow",
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: ""}, // Invalid - step tool is required
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeWorkflow,
		Name:    "test-workflow",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error for missing step tool")
	}
	if !result.Requeue {
		t.Error("expected requeue on validation failure")
	}
}

func TestWorkflowReconciler_ReconcileValidationFailure_DuplicateStepID(t *testing.T) {
	mgr := NewMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	// Add Workflow with duplicate step IDs
	mgr.AddWorkflow(&api.Workflow{
		Name: "test-workflow",
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "tool1"},
			{ID: "step1", Tool: "tool2"}, // Invalid - duplicate step ID
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeWorkflow,
		Name:    "test-workflow",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error for duplicate step ID")
	}
	if !result.Requeue {
		t.Error("expected requeue on validation failure")
	}
}

func TestWorkflowReconciler_ReconcileValidationFailure_MissingArgType(t *testing.T) {
	mgr := NewMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	// Add Workflow with arg missing type
	mgr.AddWorkflow(&api.Workflow{
		Name: "test-workflow",
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "some-tool"},
		},
		Args: map[string]api.ArgDefinition{
			"my_arg": {Type: ""}, // Invalid - type is required
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeWorkflow,
		Name:    "test-workflow",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error for missing arg type")
	}
	if !result.Requeue {
		t.Error("expected requeue on validation failure")
	}
}

// =============================================================================
// Status Sync Tests
// =============================================================================

func TestWorkflowReconciler_SyncStatus_ValidWorkflow(t *testing.T) {
	mgr := NewMockWorkflowManager()
	statusUpdater := NewMockStatusUpdater()

	reconciler := NewWorkflowReconciler(mgr).
		WithStatusUpdater(statusUpdater, "default")

	// Add valid Workflow
	mgr.AddWorkflow(&api.Workflow{
		Name:      "test-workflow",
		Available: true,
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "tool1"},
			{ID: "step2", Tool: "tool2"},
		},
	})

	req := ReconcileRequest{
		Type:      ResourceTypeWorkflow,
		Name:      "test-workflow",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify status was synced
	if !statusUpdater.GetWorkflowCalled {
		t.Error("expected GetWorkflow to be called for status sync")
	}
	if !statusUpdater.UpdateWorkflowStatusCalled {
		t.Error("expected UpdateWorkflowStatus to be called")
	}

	// Verify status values
	if statusUpdater.LastUpdatedWorkflow == nil {
		t.Fatal("expected LastUpdatedWorkflow to be set")
	}
	if !statusUpdater.LastUpdatedWorkflow.Status.Valid {
		t.Error("expected Valid=true for valid Workflow")
	}
	if len(statusUpdater.LastUpdatedWorkflow.Status.ValidationErrors) != 0 {
		t.Errorf("expected no validation errors, got %v", statusUpdater.LastUpdatedWorkflow.Status.ValidationErrors)
	}
	if statusUpdater.LastUpdatedWorkflow.Status.StepCount != 2 {
		t.Errorf("expected StepCount=2, got %d", statusUpdater.LastUpdatedWorkflow.Status.StepCount)
	}
}

func TestWorkflowReconciler_SyncStatus_InvalidWorkflow(t *testing.T) {
	mgr := NewMockWorkflowManager()
	statusUpdater := NewMockStatusUpdater()

	reconciler := NewWorkflowReconciler(mgr).
		WithStatusUpdater(statusUpdater, "default")

	// Add invalid Workflow (missing step tool)
	mgr.AddWorkflow(&api.Workflow{
		Name: "test-workflow",
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: ""}, // Invalid
		},
	})

	req := ReconcileRequest{
		Type:      ResourceTypeWorkflow,
		Name:      "test-workflow",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	_ = reconciler.Reconcile(ctx, req)

	// Verify status was synced with validation errors
	if !statusUpdater.UpdateWorkflowStatusCalled {
		t.Error("expected UpdateWorkflowStatus to be called")
	}

	if statusUpdater.LastUpdatedWorkflow == nil {
		t.Fatal("expected LastUpdatedWorkflow to be set")
	}
	if statusUpdater.LastUpdatedWorkflow.Status.Valid {
		t.Error("expected Valid=false for invalid Workflow")
	}
	if len(statusUpdater.LastUpdatedWorkflow.Status.ValidationErrors) == 0 {
		t.Error("expected validation errors to be set")
	}
}

func TestWorkflowReconciler_SyncStatus_ExtractReferencedTools(t *testing.T) {
	mgr := NewMockWorkflowManager()
	statusUpdater := NewMockStatusUpdater()

	reconciler := NewWorkflowReconciler(mgr).
		WithStatusUpdater(statusUpdater, "default")

	// Add Workflow with multiple tools
	mgr.AddWorkflow(&api.Workflow{
		Name: "test-workflow",
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "tool-a"},
			{ID: "step2", Tool: "tool-b"},
			{ID: "step3", Tool: "tool-a"}, // Duplicate tool should be deduplicated
			{
				ID:   "step4",
				Tool: "tool-c",
				Condition: &api.WorkflowCondition{
					Tool: "condition-tool", // Tool referenced in condition
				},
			},
		},
	})

	req := ReconcileRequest{
		Type:      ResourceTypeWorkflow,
		Name:      "test-workflow",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	_ = reconciler.Reconcile(ctx, req)

	// Verify referenced tools were extracted and deduplicated
	if statusUpdater.LastUpdatedWorkflow == nil {
		t.Fatal("expected LastUpdatedWorkflow to be set")
	}

	tools := statusUpdater.LastUpdatedWorkflow.Status.ReferencedTools
	if len(tools) != 4 {
		t.Errorf("expected 4 referenced tools (deduplicated), got %d: %v", len(tools), tools)
	}

	// Check all tools are present (sorted alphabetically)
	expectedTools := []string{"condition-tool", "tool-a", "tool-b", "tool-c"}
	for i, expected := range expectedTools {
		if i >= len(tools) || tools[i] != expected {
			t.Errorf("expected tool[%d]=%s, got %v", i, expected, tools)
		}
	}
}

func TestWorkflowReconciler_SyncStatus_NoUpdaterConfigured(t *testing.T) {
	mgr := NewMockWorkflowManager()

	// No status updater configured - should not panic
	reconciler := NewWorkflowReconciler(mgr)

	mgr.AddWorkflow(&api.Workflow{
		Name: "test-workflow",
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "some-tool"},
		},
	})

	req := ReconcileRequest{
		Type:    ResourceTypeWorkflow,
		Name:    "test-workflow",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	// Should complete without error even without status updater
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
}

func TestWorkflowReconciler_SyncStatus_GetWorkflowError(t *testing.T) {
	mgr := NewMockWorkflowManager()
	statusUpdater := NewMockStatusUpdater()

	// Simulate error when getting Workflow CRD
	statusUpdater.GetWorkflowError = fmt.Errorf("CRD not found")

	reconciler := NewWorkflowReconciler(mgr).
		WithStatusUpdater(statusUpdater, "default")

	mgr.AddWorkflow(&api.Workflow{
		Name: "test-workflow",
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "some-tool"},
		},
	})

	req := ReconcileRequest{
		Type:      ResourceTypeWorkflow,
		Name:      "test-workflow",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	// Reconciliation should still succeed even if status sync fails
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// GetWorkflow was called but UpdateWorkflowStatus should not be called
	if !statusUpdater.GetWorkflowCalled {
		t.Error("expected GetWorkflow to be called")
	}
	if statusUpdater.UpdateWorkflowStatusCalled {
		t.Error("expected UpdateWorkflowStatus NOT to be called when GetWorkflow fails")
	}
}

func TestWorkflowReconciler_SyncStatus_UpdateError(t *testing.T) {
	mgr := NewMockWorkflowManager()
	statusUpdater := NewMockStatusUpdater()

	// Simulate error when updating status
	statusUpdater.UpdateWorkflowStatusError = fmt.Errorf("update failed")

	reconciler := NewWorkflowReconciler(mgr).
		WithStatusUpdater(statusUpdater, "default")

	mgr.AddWorkflow(&api.Workflow{
		Name: "test-workflow",
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "some-tool"},
		},
	})

	req := ReconcileRequest{
		Type:      ResourceTypeWorkflow,
		Name:      "test-workflow",
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
	if !statusUpdater.GetWorkflowCalled {
		t.Error("expected GetWorkflow to be called")
	}
	if !statusUpdater.UpdateWorkflowStatusCalled {
		t.Error("expected UpdateWorkflowStatus to be called")
	}
}
