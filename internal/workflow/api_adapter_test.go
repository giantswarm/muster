package workflow

import "testing"

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
