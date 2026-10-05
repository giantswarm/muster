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

// TestWorkflowExecutor_FailureErrorOnStoppingStepOnly verifies that a failure
// tolerated by a forEach step's allow_failure carries no error in the failure
// document; only the step that stopped the workflow does.
func TestWorkflowExecutor_FailureErrorOnStoppingStepOnly(t *testing.T) {
	mock := &scriptedToolCaller{
		responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			return nil, fmt.Errorf("%s broke", toolName)
		},
	}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "failure_error_stopping_step",
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
	assert.Equal(t, statusFailed, probe[0]["status"])
	assert.NotContains(t, probe[0], "error", "a tolerated failure did not stop the workflow")
	deploy := stepEntries(t, result, "deploy")
	require.Len(t, deploy, 1)
	assert.Equal(t, "deploy_tool broke", deploy[0]["error"])
}

// TestWorkflowExecutor_ParallelFailureErrorOnStoppingSubStepOnly verifies that
// when several parallel sub-steps fail, only the one the returned error names
// carries it, at its place after the records of the steps before the group.
func TestWorkflowExecutor_ParallelFailureErrorOnStoppingSubStepOnly(t *testing.T) {
	mock := &scriptedToolCaller{
		responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			if toolName == "prepare_tool" {
				return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(`{}`)}}, nil
			}
			return nil, fmt.Errorf("%s broke", toolName)
		},
	}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "parallel_failure_error",
		Steps: []api.WorkflowStep{
			{ID: "prepare", Tool: "prepare_tool"},
			{
				ID: "group",
				Parallel: []api.WorkflowSubStep{
					{ID: "a", Tool: "a_tool"},
					{ID: "b", Tool: "b_tool"},
				},
			},
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step a failed")
	require.NotNil(t, result)

	prepare := stepEntries(t, result, "prepare")
	require.Len(t, prepare, 1)
	assert.NotContains(t, prepare[0], "error")
	a := stepEntries(t, result, "a")
	require.Len(t, a, 1)
	assert.Equal(t, "a_tool broke", a[0]["error"])
	b := stepEntries(t, result, "b")
	require.Len(t, b, 1)
	assert.Equal(t, statusFailed, b[0]["status"])
	assert.NotContains(t, b[0], "error", "only the sub-step the returned error names carries it")
}
