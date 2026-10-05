package workflow

import (
	"context"
	"fmt"
	"testing"

	"github.com/giantswarm/muster/v5/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkflowExecutor_ForEachFailureErrorOnFailedIterationOnly verifies that
// only the iteration whose sub-step stopped the workflow carries the error in
// the failure document, not the earlier iterations of the same sub-step.
func TestWorkflowExecutor_ForEachFailureErrorOnFailedIterationOnly(t *testing.T) {
	mock := &scriptedToolCaller{
		responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			if args["name"] == "c" {
				return nil, fmt.Errorf("c broke")
			}
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(`{}`)}}, nil
		},
	}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "foreach_failure_error",
		Args: map[string]api.ArgDefinition{"items": {Type: "array", Required: true}},
		Steps: []api.WorkflowStep{
			{
				ID: "loop",
				ForEach: &api.WorkflowForEach{
					Items: "{{ .input.items }}",
					Steps: []api.WorkflowSubStep{
						{ID: "x", Tool: "t", Args: map[string]interface{}{"name": "{{ .vars.item }}"}},
					},
				},
			},
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{"items": []interface{}{"a", "b", "c"}})
	require.Error(t, err)
	require.NotNil(t, result)

	entries := stepEntries(t, result, "x")
	require.Len(t, entries, 3)
	for i := 0; i < 2; i++ {
		assert.Equal(t, statusCompleted, entries[i]["status"], "iteration %d status", i)
		assert.NotContains(t, entries[i], "error", "completed iteration %d must carry no error", i)
	}
	assert.Equal(t, statusFailed, entries[2]["status"])
	assert.Equal(t, "c broke", entries[2]["error"])
}

// TestWorkflowExecutor_FailureErrorNotOnOnFailureStep verifies that an
// onFailure step reusing the failed step's ID does not carry that step's error.
func TestWorkflowExecutor_FailureErrorNotOnOnFailureStep(t *testing.T) {
	mock := &scriptedToolCaller{
		responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			if toolName == "deploy_tool" {
				return nil, fmt.Errorf("deploy broke")
			}
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(`{}`)}}, nil
		},
	}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name:      "failure_error_onfailure",
		Steps:     []api.WorkflowStep{{ID: "deploy", Tool: "deploy_tool"}},
		OnFailure: []api.WorkflowSubStep{{ID: "deploy", Tool: "rollback_tool"}},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{})
	require.Error(t, err)
	require.NotNil(t, result)

	entries := stepEntries(t, result, "deploy")
	require.Len(t, entries, 2)
	assert.Equal(t, "deploy_tool", entries[0]["tool"])
	assert.Equal(t, "deploy broke", entries[0]["error"])
	assert.Equal(t, "rollback_tool", entries[1]["tool"])
	assert.NotContains(t, entries[1], "error", "the onFailure step ran fine and must carry no error")
}

// TestWorkflowExecutor_FailureErrorPerEntry verifies that, in the failure
// document, a failure tolerated by a forEach step's allow_failure carries its
// own error and the step that stopped the workflow carries its own.
func TestWorkflowExecutor_FailureErrorPerEntry(t *testing.T) {
	mock := &scriptedToolCaller{
		responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			return nil, fmt.Errorf("%s broke", toolName)
		},
	}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "failure_error_per_entry",
		Args: map[string]api.ArgDefinition{"items": {Type: "array", Required: true}},
		Steps: []api.WorkflowStep{
			{
				ID:           "loop",
				AllowFailure: true,
				ForEach: &api.WorkflowForEach{
					Items: "{{ .input.items }}",
					Steps: []api.WorkflowSubStep{{ID: "probe", Tool: "probe_tool"}},
				},
			},
			{ID: "deploy", Tool: "deploy_tool"},
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{"items": []interface{}{"a"}})
	require.Error(t, err)
	require.NotNil(t, result)

	probe := stepEntries(t, result, "probe")
	require.Len(t, probe, 1)
	assert.Equal(t, "probe_tool broke", probe[0]["error"])
	deploy := stepEntries(t, result, "deploy")
	require.Len(t, deploy, 1)
	assert.Equal(t, "deploy_tool broke", deploy[0]["error"])
}
