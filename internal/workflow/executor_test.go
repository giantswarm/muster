package workflow

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedToolCaller is a concurrency-safe ToolCaller whose responses can be
// scripted per tool. It is used by the control-flow tests (forEach, parallel,
// conditions, onFailure).
type scriptedToolCaller struct {
	mu        sync.Mutex
	calls     []toolCall
	responder func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error)
}

func (m *scriptedToolCaller) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	m.mu.Lock()
	m.calls = append(m.calls, toolCall{toolName: toolName, args: args})
	m.mu.Unlock()
	if m.responder != nil {
		return m.responder(toolName, args)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(`{"status": "success"}`)},
		IsError: false,
	}, nil
}

func (m *scriptedToolCaller) calledTools() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	got := make(map[string]bool, len(m.calls))
	for _, c := range m.calls {
		got[c.toolName] = true
	}
	return got
}

// mockToolCaller implements ToolCaller for testing
type mockToolCaller struct {
	calls []toolCall
}

type toolCall struct {
	toolName string
	args     map[string]interface{}
}

func (m *mockToolCaller) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	m.calls = append(m.calls, toolCall{toolName: toolName, args: args})

	// Return a simple success result
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(`{"status": "success", "data": "test result"}`),
		},
		IsError: false,
	}, nil
}

func TestWorkflowExecutor_ExecuteWorkflow(t *testing.T) {
	mock := &mockToolCaller{}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name:        "test_workflow",
		Description: "Test workflow",
		Args: map[string]api.ArgDefinition{
			"cluster": {
				Type:        "string",
				Required:    true,
				Description: "Cluster name",
			},
		},
		Steps: []api.WorkflowStep{
			{
				ID:   "step1",
				Tool: "test_tool",
				Args: map[string]interface{}{
					"cluster": "{{ .input.cluster }}",
					"action":  "login",
				},
			},
		},
	}

	args := map[string]interface{}{
		"cluster": "test-cluster",
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, args)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.IsError)

	// Verify the tool was called with resolved arguments
	assert.Len(t, mock.calls, 1)
	assert.Equal(t, "test_tool", mock.calls[0].toolName)
	assert.Equal(t, "test-cluster", mock.calls[0].args["cluster"])
	assert.Equal(t, "login", mock.calls[0].args["action"])
}

func TestWorkflowExecutor_ValidateInputs(t *testing.T) {
	executor := NewWorkflowExecutor(nil, nil)

	argsDefinition := map[string]api.ArgDefinition{
		"required_string": {
			Type:        "string",
			Required:    true,
			Description: "Required string field",
		},
		"optional_number": {
			Type:        "number",
			Required:    false,
			Description: "Optional number field",
			Default:     float64(42),
		},
	}

	t.Run("valid inputs", func(t *testing.T) {
		args := map[string]interface{}{
			"required_string": "test",
		}
		err := executor.validateInputs(argsDefinition, args)
		assert.NoError(t, err)
		assert.Equal(t, float64(42), args["optional_number"]) // Default applied
	})

	t.Run("missing required field", func(t *testing.T) {
		args := map[string]interface{}{}
		err := executor.validateInputs(argsDefinition, args)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required field 'required_string' is missing")
	})

	t.Run("wrong type", func(t *testing.T) {
		args := map[string]interface{}{
			"required_string": 123, // Should be string
		}
		err := executor.validateInputs(argsDefinition, args)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "field 'required_string' has wrong type")
	})
}

func TestWorkflowExecutor_ResolveTemplate(t *testing.T) {
	executor := NewWorkflowExecutor(nil, nil)

	ctx := &executionContext{
		input: map[string]interface{}{
			"cluster": "test-cluster",
			"port":    8080,
		},
		results: map[string]interface{}{
			"step1": map[string]interface{}{
				"url": "http://localhost:9090",
			},
		},
	}

	tests := []struct {
		name     string
		template string
		expected interface{}
	}{
		{
			name:     "simple string",
			template: "{{ .input.cluster }}",
			expected: "test-cluster",
		},
		{
			name:     "number value",
			template: "{{ .input.port }}",
			expected: 8080, // Numbers are preserved as their original type
		},
		{
			name:     "nested access",
			template: "{{ .results.step1.url }}",
			expected: "http://localhost:9090",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := executor.resolveTemplate(tt.template, ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkflowExecutor_StoreResults(t *testing.T) {
	mock := &mockToolCaller{}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name:        "test_workflow",
		Description: "Test workflow with result storage",
		Args:        map[string]api.ArgDefinition{},
		Steps: []api.WorkflowStep{
			{
				ID:    "step1",
				Tool:  "test_tool",
				Args:  map[string]interface{}{},
				Store: true,
			},
			{
				ID:   "step2",
				Tool: "test_tool",
				Args: map[string]interface{}{
					"data": "{{ .results.step1.status }}",
				},
			},
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{})
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify both tools were called
	assert.Len(t, mock.calls, 2)

	// Second call should have resolved the stored result
	assert.Equal(t, "success", mock.calls[1].args["data"])
}

func TestWorkflowExecutor_ResolveTemplate_StringNumbers(t *testing.T) {
	executor := NewWorkflowExecutor(nil, nil)

	// Test that string templates with numeric values don't get converted to float64
	ctx := &executionContext{
		input: map[string]interface{}{
			"localPort": "18000", // This is a string, not a number
		},
		variables: make(map[string]interface{}),
		results:   make(map[string]interface{}),
	}

	// This should return a string, not a float64
	result, err := executor.resolveTemplate("{{.input.localPort}}", ctx)
	assert.NoError(t, err)
	assert.Equal(t, "18000", result)
	assert.IsType(t, "", result) // Should be string type, not float64
}

func TestWorkflowExecutor_TemplateCondition(t *testing.T) {
	cases := []struct {
		env       string
		wantCalls int
	}{
		{"production", 1}, // condition true -> step runs
		{"staging", 0},    // condition false -> step skipped
	}

	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			mock := &scriptedToolCaller{}
			executor := NewWorkflowExecutor(mock, nil)

			workflow := &api.Workflow{
				Name: "template_condition",
				Args: map[string]api.ArgDefinition{
					"env": {Type: "string", Required: true},
				},
				Steps: []api.WorkflowStep{
					{
						ID:   "deploy",
						Tool: "deploy_tool",
						Condition: &api.WorkflowCondition{
							Template: `{{ eq .input.env "production" }}`,
						},
					},
				},
			}

			_, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{"env": tc.env})
			require.NoError(t, err)
			assert.Len(t, mock.calls, tc.wantCalls)
		})
	}
}

func TestWorkflowExecutor_ForEach(t *testing.T) {
	mock := &scriptedToolCaller{}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "foreach",
		Args: map[string]api.ArgDefinition{
			"clusters": {Type: "array", Required: true},
		},
		Steps: []api.WorkflowStep{
			{
				ID: "fanout",
				ForEach: &api.WorkflowForEach{
					Items: "{{ .input.clusters }}",
					As:    "item",
					Steps: []api.WorkflowSubStep{
						{
							ID:   "deploy",
							Tool: "deploy_tool",
							Args: map[string]interface{}{
								"name": "{{ .vars.item.name }}",
							},
						},
					},
				},
			},
		},
	}

	clusters := []interface{}{
		map[string]interface{}{"name": "alpha"},
		map[string]interface{}{"name": "beta"},
	}

	_, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{"clusters": clusters})
	require.NoError(t, err)

	require.Len(t, mock.calls, 2)
	assert.Equal(t, "alpha", mock.calls[0].args["name"])
	assert.Equal(t, "beta", mock.calls[1].args["name"])
}

func TestWorkflowExecutor_ForEachFailureStops(t *testing.T) {
	mock := &scriptedToolCaller{
		responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			return nil, fmt.Errorf("always fails")
		},
	}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "foreach_fail",
		Args: map[string]api.ArgDefinition{"items": {Type: "array"}},
		Steps: []api.WorkflowStep{
			{
				ID: "loop",
				ForEach: &api.WorkflowForEach{
					Items: "{{ .input.items }}",
					Steps: []api.WorkflowSubStep{{ID: "s", Tool: "t"}},
				},
			},
		},
	}

	_, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{"items": []interface{}{"x", "y"}})
	require.Error(t, err)
	// A non-allowFailure sub-step failure stops the loop after the first item.
	assert.Len(t, mock.calls, 1)
}

func TestWorkflowExecutor_Parallel(t *testing.T) {
	mock := &scriptedToolCaller{}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "parallel",
		Steps: []api.WorkflowStep{
			{
				ID: "group",
				Parallel: []api.WorkflowSubStep{
					{ID: "a", Tool: "tool_a"},
					{ID: "b", Tool: "tool_b"},
					{ID: "c", Tool: "tool_c"},
				},
			},
		},
	}

	_, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{})
	require.NoError(t, err)

	require.Len(t, mock.calls, 3)
	got := mock.calledTools()
	assert.True(t, got["tool_a"] && got["tool_b"] && got["tool_c"], "all parallel sub-steps should run")
}

func TestWorkflowExecutor_ParallelFailure(t *testing.T) {
	mock := &scriptedToolCaller{
		responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			if toolName == "bad" {
				return nil, fmt.Errorf("boom")
			}
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(`{}`)}}, nil
		},
	}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "parallel_fail",
		Steps: []api.WorkflowStep{
			{
				ID: "group",
				Parallel: []api.WorkflowSubStep{
					{ID: "a", Tool: "good"},
					{ID: "b", Tool: "bad"},
				},
			},
		},
	}

	_, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{})
	require.Error(t, err)
}

func TestWorkflowExecutor_OnFailure(t *testing.T) {
	mock := &scriptedToolCaller{
		responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			if toolName == "failing_tool" {
				return nil, fmt.Errorf("boom")
			}
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(`{"ok": true}`)}}, nil
		},
	}
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "with_rollback",
		Steps: []api.WorkflowStep{
			{ID: "main", Tool: "failing_tool"},
		},
		OnFailure: []api.WorkflowSubStep{
			{ID: "rollback", Tool: "cleanup_tool"},
		},
	}

	_, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{})
	require.Error(t, err)
	assert.True(t, mock.calledTools()["cleanup_tool"], "onFailure cleanup tool should have been called")
}
