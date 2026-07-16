package aggregator

import (
	"encoding/json"
	"testing"

	"github.com/giantswarm/muster/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestConvertToMCPResult_StructuredContent verifies that StructuredContent is
// propagated to the MCP result alongside the text content, and stays absent
// when the internal result does not set it.
func TestConvertToMCPResult_StructuredContent(t *testing.T) {
	payload := map[string]any{"status": "auth_required", "auth_url": "https://idp.example.com/authorize"}

	result := convertToMCPResult(&api.CallToolResult{
		Content:           []any{"some text"},
		StructuredContent: payload,
	})
	assert.Equal(t, payload, result.StructuredContent)
	assert.Len(t, result.Content, 1)

	// The MCP wire format carries the payload under the structuredContent key.
	wire, err := json.Marshal(result)
	require.NoError(t, err)
	assert.Contains(t, string(wire), `"structuredContent":{"auth_url":"https://idp.example.com/authorize","status":"auth_required"}`)

	result = convertToMCPResult(&api.CallToolResult{Content: []any{"some text"}})
	assert.Nil(t, result.StructuredContent)
}
