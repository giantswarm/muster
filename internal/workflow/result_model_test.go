package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

// decodeResult parses the single JSON text content of a workflow result.
func decodeResult(t *testing.T, result *mcp.CallToolResult) map[string]interface{} {
	t.Helper()
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected text content")
	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(textContent.Text), &decoded))
	return decoded
}

// jsonResponder builds a scripted tool caller returning the given JSON body per
// tool name.
func jsonResponder(bodies map[string]string) *scriptedToolCaller {
	return &scriptedToolCaller{
		responder: func(toolName string, _ map[string]interface{}) (*mcp.CallToolResult, error) {
			body, ok := bodies[toolName]
			if !ok {
				body = `{}`
			}
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(body)}}, nil
		},
	}
}

// #873: a later step can reference an earlier step's result even though the
// earlier step is not an output step, and the returned document excludes the
// referenced-but-not-output step while including an output step's result.
func TestWorkflowExecutor_ReferenceNonOutputStep(t *testing.T) {
	mock := jsonResponder(map[string]string{
		"producer": `{"token": "abc", "items": ["x", "y"]}`,
		"consumer": `{"ok": true}`,
	})
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "chain",
		Steps: []api.WorkflowStep{
			{ID: "producer", Tool: "producer"}, // neither output nor store
			{
				ID:     "consumer",
				Tool:   "consumer",
				Args:   map[string]interface{}{"token": "{{ .results.producer.token }}"},
				Output: boolPtr(true),
			},
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{})
	require.NoError(t, err)

	// The non-output producer's value flowed into the consumer.
	require.Len(t, mock.calls, 2)
	assert.Equal(t, "abc", mock.calls[1].args["token"])

	// The returned document includes the output step's result but not the
	// non-output producer's.
	decoded := decodeResult(t, result)
	steps, ok := decoded[api.FieldSteps].([]interface{})
	require.True(t, ok)
	require.Len(t, steps, 2)

	producer := steps[0].(map[string]interface{})
	assert.Equal(t, "producer", producer["id"])
	_, hasResult := producer["result"]
	assert.False(t, hasResult, "non-output step must not surface its result")

	consumer := steps[1].(map[string]interface{})
	assert.Equal(t, "consumer", consumer["id"])
	_, hasResult = consumer["result"]
	assert.True(t, hasResult, "output step must surface its result")
}

// #873: the deprecated `store` flag keeps working as an alias for `output`.
func TestWorkflowExecutor_StoreAliasEmitsResult(t *testing.T) {
	mock := jsonResponder(map[string]string{"only": `{"value": 1}`})
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "store-alias",
		Steps: []api.WorkflowStep{
			{ID: "only", Tool: "only", Store: true},
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{})
	require.NoError(t, err)

	decoded := decodeResult(t, result)
	steps := decoded[api.FieldSteps].([]interface{})
	step := steps[0].(map[string]interface{})
	assert.Equal(t, "only", step["id"])
	_, hasResult := step["result"]
	assert.True(t, hasResult, "store:true must still surface the result")
}

// #874: a workflow output projection shapes the returned payload, preserving
// JSON structure across steps and omitting the default envelope.
func TestWorkflowExecutor_OutputProjection(t *testing.T) {
	mock := jsonResponder(map[string]string{
		"pods":   `{"items": [{"name": "a"}, {"name": "b"}]}`,
		"events": `{"items": [1, 2, 3]}`,
	})
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "shaped",
		Args: map[string]api.ArgDefinition{"cluster": {Type: "string", Required: true}},
		Steps: []api.WorkflowStep{
			{ID: "pods", Tool: "pods"},
			{ID: "events", Tool: "events"},
		},
		Output: map[string]interface{}{
			"cluster":      "{{ .input.cluster }}",
			"notRunning":   "{{ .results.pods.items }}",
			"backoffCount": "{{ len .results.events.items }}",
			"nested": map[string]interface{}{
				"first": "{{ (index .results.pods.items 0).name }}",
			},
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{"cluster": "prod"})
	require.NoError(t, err)

	decoded := decodeResult(t, result)

	// The envelope keys are gone; only the projection remains.
	_, hasSteps := decoded[api.FieldSteps]
	assert.False(t, hasSteps, "projection must omit the steps envelope")
	_, hasWorkflow := decoded["workflow"]
	assert.False(t, hasWorkflow)

	assert.Equal(t, "prod", decoded["cluster"])

	// Arrays stay arrays.
	notRunning, ok := decoded["notRunning"].([]interface{})
	require.True(t, ok, "array must be preserved as array, got %T", decoded["notRunning"])
	assert.Len(t, notRunning, 2)

	// Numbers stay numbers (JSON round-trip yields float64).
	assert.Equal(t, float64(3), decoded["backoffCount"])

	// Nested objects and array indexing render.
	nested := decoded["nested"].(map[string]interface{})
	assert.Equal(t, "a", nested["first"])
}

// coerceScalar turns numeric strings into numbers but must keep non-finite
// floats ("NaN", "Inf", ...) as strings, otherwise json.Marshal of a projection
// containing such a leaf would fail.
func TestCoerceScalar(t *testing.T) {
	cases := []struct {
		in   string
		want interface{}
	}{
		{"42", int64(42)},
		{"3.14", float64(3.14)},
		{"hello", "hello"},
		{"NaN", "NaN"},
		{"Inf", "Inf"},
		{"+Inf", "+Inf"},
		{"infinity", "infinity"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := coerceScalar(tc.in)
			assert.Equal(t, tc.want, got)
			_, err := json.Marshal(got)
			require.NoError(t, err, "coerced value must be JSON-marshalable")
		})
	}
}

// #875: condition jsonPath supports array indexing and template forms in
// addition to legacy dotted paths.
func TestWorkflowExecutor_JsonPathArrayIndexing(t *testing.T) {
	executor := NewWorkflowExecutor(nil, nil)
	execCtx := &executionContext{
		input:     map[string]interface{}{},
		variables: map[string]interface{}{},
		results:   map[string]interface{}{},
	}

	toolResult := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(
			`{"items": [{"name": "alpha"}, {"name": "beta"}], "data": {"field": "v"}}`,
		)},
	}

	cases := []struct {
		name        string
		expectation map[string]interface{}
		want        bool
	}{
		{"legacy dotted path", map[string]interface{}{"data.field": "v"}, true},
		{"array index bare path", map[string]interface{}{"items[1].name": "beta"}, true},
		{"array index mismatch", map[string]interface{}{"items[0].name": "beta"}, false},
		{"template form", map[string]interface{}{`{{ (index .result.items 0).name }}`: "alpha"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := executor.validateJsonPath(toolResult, tc.expectation, execCtx)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
