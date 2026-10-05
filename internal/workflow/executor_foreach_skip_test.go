package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/giantswarm/muster/v5/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stepEntries returns the returned document's step entries with the given id.
func stepEntries(t *testing.T, result *mcp.CallToolResult, id string) []map[string]interface{} {
	t.Helper()
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(textContent.Text), &doc))
	steps, ok := doc["steps"].([]interface{})
	require.True(t, ok)
	var entries []map[string]interface{}
	for _, s := range steps {
		step, ok := s.(map[string]interface{})
		require.True(t, ok)
		if step["id"] == id {
			entries = append(entries, step)
		}
	}
	return entries
}

// TestWorkflowExecutor_ForEachSkippedIterationHasNoResult verifies that an
// iteration whose sub-step is skipped by its condition sees no result for it:
// no "<id>_<index>", no plain "<id>" for the later sub-steps of the same
// iteration, and no result in the returned document, whether it comes before
// or after an iteration that ran. After the loop, the plain "<id>" keeps the
// result of the last iteration that ran.
func TestWorkflowExecutor_ForEachSkippedIterationHasNoResult(t *testing.T) {
	mock := &scriptedToolCaller{
		responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			text := `{}`
			if toolName == "list_sub_issues" {
				text = fmt.Sprintf(`{"children_of": %q}`, args["name"])
			}
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(text)}}, nil
		},
	}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "foreach_skip",
		Args: map[string]api.ArgDefinition{"items": {Type: "array", Required: true}},
		Steps: []api.WorkflowStep{
			{
				ID: "loop",
				ForEach: &api.WorkflowForEach{
					Items: "{{ .input.items }}",
					Steps: []api.WorkflowSubStep{
						{
							ID:        "subs",
							Tool:      "list_sub_issues",
							Args:      map[string]interface{}{"name": "{{ .vars.item.name }}"},
							Condition: &api.WorkflowCondition{Template: `{{ eq .vars.item.kind "epic" }}`},
							Store:     true,
						},
						{
							ID:   "report",
							Tool: "report_tool",
							Args: map[string]interface{}{"has_subs": `{{ hasKey .results "subs" }}`},
						},
					},
				},
			},
			{
				ID:   "summary",
				Tool: "summary_tool",
				Args: map[string]interface{}{
					"has0": `{{ hasKey .results "subs_0" }}`,
					"has1": `{{ hasKey .results "subs_1" }}`,
					"has2": `{{ hasKey .results "subs_2" }}`,
					"last": "{{ .results.subs.children_of }}",
				},
			},
		},
	}

	items := []interface{}{
		map[string]interface{}{"name": "task-a", "kind": "task"},
		map[string]interface{}{"name": "epic-b", "kind": "epic"},
		map[string]interface{}{"name": "task-c", "kind": "task"},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{"items": items})
	require.NoError(t, err)

	var reports []interface{}
	var summary map[string]interface{}
	for _, c := range mock.calls {
		switch c.toolName {
		case "report_tool":
			reports = append(reports, c.args["has_subs"])
		case "summary_tool":
			summary = c.args
		}
	}
	assert.Equal(t, []interface{}{false, true, false}, reports,
		"a sub-step skipped in an iteration leaves no plain result for its siblings")
	require.NotNil(t, summary)
	assert.Equal(t, false, summary["has0"], "skipped iteration before any run must have no subs_0")
	assert.Equal(t, true, summary["has1"], "iteration that ran must have subs_1")
	assert.Equal(t, false, summary["has2"], "skipped iteration after a run must not inherit subs_2")
	assert.Equal(t, "epic-b", summary["last"], "plain id keeps the last iteration that ran")

	entries := stepEntries(t, result, "subs")
	require.Len(t, entries, 3)
	for i, want := range []string{statusSkipped, statusCompleted, statusSkipped} {
		assert.Equal(t, want, entries[i]["status"], "iteration %d status", i)
		assert.Equal(t, float64(i), entries[i]["iteration"], "iteration %d index", i)
		if want == statusSkipped {
			assert.NotContains(t, entries[i], "result", "skipped iteration %d must carry no result", i)
		} else {
			assert.Equal(t, map[string]interface{}{"children_of": "epic-b"}, entries[i]["result"])
		}
	}
}

// TestWorkflowExecutor_ForEachFromStepSeesOnlyThisIteration verifies that a
// fromStep condition on a sub-step skipped in this iteration does not pass on
// an earlier iteration's run; like a fromStep on a skipped top-level step, it
// finds no result.
func TestWorkflowExecutor_ForEachFromStepSeesOnlyThisIteration(t *testing.T) {
	mock := &scriptedToolCaller{}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "foreach_fromstep",
		Args: map[string]api.ArgDefinition{"items": {Type: "array", Required: true}},
		Steps: []api.WorkflowStep{
			{
				ID: "loop",
				ForEach: &api.WorkflowForEach{
					Items: "{{ .input.items }}",
					Steps: []api.WorkflowSubStep{
						{
							ID:        "subs",
							Tool:      "list_sub_issues",
							Condition: &api.WorkflowCondition{Template: `{{ eq .vars.item "epic" }}`},
						},
						{
							ID:        "notify",
							Tool:      "notify_tool",
							Condition: &api.WorkflowCondition{FromStep: "subs", Expect: api.WorkflowConditionExpectation{Success: true}},
						},
					},
				},
			},
		},
	}

	_, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{"items": []interface{}{"epic", "task"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "references non-existent step result: subs")
	assert.Equal(t, 1, countCalls(mock, "notify_tool"), "notify runs for the epic only")
}

// TestWorkflowExecutor_ForEachFailedIterationIsIndexed verifies that the
// iteration whose sub-step fails gets its index in the returned document and
// its error result under "<id>_<index>".
func TestWorkflowExecutor_ForEachFailedIterationIsIndexed(t *testing.T) {
	mock := &scriptedToolCaller{
		responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			if args["name"] == "bad" {
				return nil, fmt.Errorf("boom")
			}
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(`{}`)}}, nil
		},
	}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "foreach_failed_index",
		Args: map[string]api.ArgDefinition{"items": {Type: "array", Required: true}},
		Steps: []api.WorkflowStep{
			{
				ID:           "loop",
				AllowFailure: true,
				ForEach: &api.WorkflowForEach{
					Items: "{{ .input.items }}",
					Steps: []api.WorkflowSubStep{
						{ID: "s", Tool: "t", Args: map[string]interface{}{"name": "{{ .vars.item }}"}, Store: true},
					},
				},
			},
			{
				ID:   "after",
				Tool: "after_tool",
				Args: map[string]interface{}{"failed": "{{ .results.s_1.isError }}"},
			},
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{"items": []interface{}{"good", "bad"}})
	require.NoError(t, err)

	require.NotEmpty(t, mock.calls)
	last := mock.calls[len(mock.calls)-1]
	require.Equal(t, "after_tool", last.toolName)
	assert.Equal(t, true, last.args["failed"], "the failed iteration's error result is under s_1")

	entries := stepEntries(t, result, "s")
	require.Len(t, entries, 2)
	assert.Equal(t, statusFailed, entries[1]["status"])
	assert.Equal(t, float64(1), entries[1]["iteration"])
}

func countCalls(m *scriptedToolCaller, toolName string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		if c.toolName == toolName {
			n++
		}
	}
	return n
}
