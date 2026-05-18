package aggregator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMapWorkflowToolName verifies that the workflow provider's internal tool
// names are rewritten correctly for the user-facing MCP surface.
//
// Regression coverage for: workflow execution tools were previously advertised
// as "core_action_<workflow-name>" — a name that the aggregator's call routing
// does not recognize, leaving clients with non-functional tools. Per the muster
// architecture, execution tools must be exposed as "workflow_<workflow-name>".
func TestMapWorkflowToolName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "management tool gets core_ prefix",
			in:   "workflow_list",
			want: "core_workflow_list",
		},
		{
			name: "management tool with multi-word verb",
			in:   "workflow_execution_get",
			want: "core_workflow_execution_get",
		},
		{
			name: "execution tool action_ becomes workflow_",
			in:   "action_deploy-app",
			want: "workflow_deploy-app",
		},
		{
			name: "execution tool keeps remainder verbatim",
			in:   "action_complex.workflow-name",
			want: "workflow_complex.workflow-name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mapWorkflowToolName(tc.in))
		})
	}
}
