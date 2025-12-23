package reconciler

import (
	"context"
	"fmt"
	"testing"

	"muster/internal/api"
)

// mockWorkflowManager implements WorkflowManager for testing.
type mockWorkflowManager struct {
	workflows map[string]*api.Workflow
}

func newMockWorkflowManager() *mockWorkflowManager {
	return &mockWorkflowManager{
		workflows: make(map[string]*api.Workflow),
	}
}

func (m *mockWorkflowManager) GetWorkflows() []api.Workflow {
	result := make([]api.Workflow, 0, len(m.workflows))
	for _, wf := range m.workflows {
		result = append(result, *wf)
	}
	return result
}

func (m *mockWorkflowManager) GetWorkflow(name string) (*api.Workflow, error) {
	wf, ok := m.workflows[name]
	if !ok {
		return nil, fmt.Errorf("Workflow %s not found", name)
	}
	return wf, nil
}

func (m *mockWorkflowManager) AddWorkflow(wf *api.Workflow) {
	m.workflows[wf.Name] = wf
}

func (m *mockWorkflowManager) RemoveWorkflow(name string) {
	delete(m.workflows, name)
}

func TestWorkflowReconciler_GetResourceType(t *testing.T) {
	mgr := newMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	if reconciler.GetResourceType() != ResourceTypeWorkflow {
		t.Errorf("expected ResourceTypeWorkflow, got %s", reconciler.GetResourceType())
	}
}

func TestWorkflowReconciler_ReconcileCreate(t *testing.T) {
	mgr := newMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	// Add a valid Workflow
	mgr.AddWorkflow(&api.Workflow{
		Name:        "test-workflow",
		Description: "Test Workflow",
		Steps: []api.WorkflowStep{
			{
				ID:   "step-1",
				Tool: "some-tool",
			},
		},
		Available: true,
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
	mgr := newMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	// Do not add the Workflow - simulate a delete scenario
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

func TestWorkflowReconciler_ReconcileValidationError(t *testing.T) {
	mgr := newMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	// Add an invalid Workflow (no steps)
	mgr.AddWorkflow(&api.Workflow{
		Name:        "invalid-workflow",
		Description: "Invalid Workflow",
		Steps:       []api.WorkflowStep{}, // No steps
	})

	req := ReconcileRequest{
		Type:    ResourceTypeWorkflow,
		Name:    "invalid-workflow",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected validation error for invalid Workflow")
	}
	if !result.Requeue {
		t.Error("expected requeue on validation error")
	}
}

func TestWorkflowReconciler_ValidateWorkflow(t *testing.T) {
	mgr := newMockWorkflowManager()
	reconciler := NewWorkflowReconciler(mgr)

	tests := []struct {
		name        string
		wf          *api.Workflow
		expectError bool
	}{
		{
			name: "valid workflow",
			wf: &api.Workflow{
				Name: "valid",
				Steps: []api.WorkflowStep{
					{ID: "step-1", Tool: "tool-1"},
				},
			},
			expectError: false,
		},
		{
			name: "missing name",
			wf: &api.Workflow{
				Name: "",
				Steps: []api.WorkflowStep{
					{ID: "step-1", Tool: "tool-1"},
				},
			},
			expectError: true,
		},
		{
			name: "no steps",
			wf: &api.Workflow{
				Name:  "empty",
				Steps: []api.WorkflowStep{},
			},
			expectError: true,
		},
		{
			name: "step missing id",
			wf: &api.Workflow{
				Name: "test",
				Steps: []api.WorkflowStep{
					{ID: "", Tool: "tool-1"},
				},
			},
			expectError: true,
		},
		{
			name: "step missing tool",
			wf: &api.Workflow{
				Name: "test",
				Steps: []api.WorkflowStep{
					{ID: "step-1", Tool: ""},
				},
			},
			expectError: true,
		},
		{
			name: "duplicate step ids",
			wf: &api.Workflow{
				Name: "test",
				Steps: []api.WorkflowStep{
					{ID: "step-1", Tool: "tool-1"},
					{ID: "step-1", Tool: "tool-2"},
				},
			},
			expectError: true,
		},
		{
			name: "multiple valid steps",
			wf: &api.Workflow{
				Name: "multi",
				Steps: []api.WorkflowStep{
					{ID: "step-1", Tool: "tool-1"},
					{ID: "step-2", Tool: "tool-2"},
					{ID: "step-3", Tool: "tool-3"},
				},
			},
			expectError: false,
		},
		{
			name: "valid with args",
			wf: &api.Workflow{
				Name: "with-args",
				Args: map[string]api.ArgDefinition{
					"input": {Type: "string", Required: true},
				},
				Steps: []api.WorkflowStep{
					{ID: "step-1", Tool: "tool-1"},
				},
			},
			expectError: false,
		},
		{
			name: "arg missing type",
			wf: &api.Workflow{
				Name: "bad-arg",
				Args: map[string]api.ArgDefinition{
					"input": {Type: "", Required: true},
				},
				Steps: []api.WorkflowStep{
					{ID: "step-1", Tool: "tool-1"},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := reconciler.validateWorkflow(tt.wf)
			if tt.expectError && err == nil {
				t.Error("expected validation error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected validation error: %v", err)
			}
		})
	}
}
