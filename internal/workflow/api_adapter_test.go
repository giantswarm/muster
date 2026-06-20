package workflow

import (
	"testing"

	"github.com/giantswarm/muster/internal/api"
)

// TestNestedWorkflowName guards the classification at the heart of the
// nested-workflow availability fix: a workflow_<name> step tool is a nested
// workflow execution tool (whose existence must be verified), while the
// workflow_ management meta-tools and non-workflow tools are not.
func TestNestedWorkflowName(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		wantName string
		wantOK   bool
	}{
		{
			name:     "nested workflow execution tool",
			toolName: "workflow_my-runbook",
			wantName: "my-runbook",
			wantOK:   true,
		},
		{
			name:     "nested workflow name with underscores",
			toolName: "workflow_special_chars_123",
			wantName: "special_chars_123",
			wantOK:   true,
		},
		{
			name:     "management tool workflow_list is not nested",
			toolName: "workflow_list",
			wantOK:   false,
		},
		{
			name:     "management tool workflow_available is not nested",
			toolName: "workflow_available",
			wantOK:   false,
		},
		{
			name:     "management tool workflow_execution_get is not nested",
			toolName: "workflow_execution_get",
			wantOK:   false,
		},
		{
			name:     "core tool is not nested",
			toolName: "core_workflow_list",
			wantOK:   false,
		},
		{
			name:     "backend tool is not nested",
			toolName: "x_kubernetes_list",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotOK := nestedWorkflowName(tt.toolName)
			if gotOK != tt.wantOK {
				t.Fatalf("nestedWorkflowName(%q) ok = %v, want %v", tt.toolName, gotOK, tt.wantOK)
			}
			if gotOK && gotName != tt.wantName {
				t.Fatalf("nestedWorkflowName(%q) name = %q, want %q", tt.toolName, gotName, tt.wantName)
			}
		})
	}
}

// TestValidateWorkflowCondition guards the template-vs-tool/fromStep mutual
// exclusivity the executor relies on.
func TestValidateWorkflowCondition(t *testing.T) {
	tests := []struct {
		name      string
		condition *api.WorkflowCondition
		wantErr   bool
	}{
		{name: "nil condition", condition: nil, wantErr: false},
		{name: "template only", condition: &api.WorkflowCondition{Template: "{{ true }}"}, wantErr: false},
		{name: "tool only", condition: &api.WorkflowCondition{Tool: "check"}, wantErr: false},
		{name: "fromStep only", condition: &api.WorkflowCondition{FromStep: "prev"}, wantErr: false},
		{name: "template and tool", condition: &api.WorkflowCondition{Template: "{{ true }}", Tool: "check"}, wantErr: true},
		{name: "template and fromStep", condition: &api.WorkflowCondition{Template: "{{ true }}", FromStep: "prev"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateWorkflowCondition(tt.condition); (err != nil) != tt.wantErr {
				t.Fatalf("validateWorkflowCondition() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateWorkflowSubSteps covers the sub-step rules enforced for forEach
// bodies, parallel groups, and onFailure handlers.
func TestValidateWorkflowSubSteps(t *testing.T) {
	tests := []struct {
		name    string
		subs    []api.WorkflowSubStep
		wantErr bool
	}{
		{name: "empty list", subs: nil, wantErr: false},
		{name: "valid", subs: []api.WorkflowSubStep{{ID: "a", Tool: "t1"}, {ID: "b", Tool: "t2"}}, wantErr: false},
		{name: "missing id", subs: []api.WorkflowSubStep{{Tool: "t1"}}, wantErr: true},
		{name: "duplicate id", subs: []api.WorkflowSubStep{{ID: "a", Tool: "t1"}, {ID: "a", Tool: "t2"}}, wantErr: true},
		{name: "missing tool", subs: []api.WorkflowSubStep{{ID: "a"}}, wantErr: true},
		{name: "invalid condition", subs: []api.WorkflowSubStep{{ID: "a", Tool: "t1", Condition: &api.WorkflowCondition{Template: "{{ true }}", Tool: "x"}}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateWorkflowSubSteps("group", tt.subs); (err != nil) != tt.wantErr {
				t.Fatalf("validateWorkflowSubSteps() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
