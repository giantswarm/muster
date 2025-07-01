package workflow

import (
	"context"
	"testing"

	"muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	executor := NewWorkflowExecutor(mock)

	workflow := &api.Workflow{
		Name:        "test_workflow",
		Description: "Test workflow",
		InputSchema: api.WorkflowInputSchema{
			Type: "object",
			Args: map[string]api.SchemaProperty{
				"cluster": {
					Type:        "string",
					Description: "Cluster name",
				},
			},
			Required: []string{"cluster"},
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
	executor := NewWorkflowExecutor(nil)

	schema := api.WorkflowInputSchema{
		Type: "object",
		Args: map[string]api.SchemaProperty{
			"required_string": {
				Type:        "string",
				Description: "Required string field",
			},
			"optional_number": {
				Type:        "number",
				Description: "Optional number field",
				Default:     float64(42),
			},
		},
		Required: []string{"required_string"},
	}

	t.Run("valid inputs", func(t *testing.T) {
		args := map[string]interface{}{
			"required_string": "test",
		}
		err := executor.validateInputs(schema, args)
		assert.NoError(t, err)
		assert.Equal(t, float64(42), args["optional_number"]) // Default applied
	})

	t.Run("missing required field", func(t *testing.T) {
		args := map[string]interface{}{}
		err := executor.validateInputs(schema, args)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required field 'required_string' is missing")
	})

	t.Run("wrong type", func(t *testing.T) {
		args := map[string]interface{}{
			"required_string": 123, // Should be string
		}
		err := executor.validateInputs(schema, args)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "field 'required_string' has wrong type")
	})
}

func TestWorkflowExecutor_ResolveTemplate(t *testing.T) {
	executor := NewWorkflowExecutor(nil)

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
			expected: float64(8080), // Numbers are preserved when parsed as JSON
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
	executor := NewWorkflowExecutor(mock)

	workflow := &api.Workflow{
		Name:        "test_workflow",
		Description: "Test workflow with result storage",
		InputSchema: api.WorkflowInputSchema{
			Type: "object",
			Args: map[string]api.SchemaProperty{},
		},
		Steps: []api.WorkflowStep{
			{
				ID:    "step1",
				Tool:  "test_tool",
				Args:  map[string]interface{}{},
				Store: "step1_result",
			},
			{
				ID:   "step2",
				Tool: "test_tool",
				Args: map[string]interface{}{
					"data": "{{ .results.step1_result.status }}",
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
