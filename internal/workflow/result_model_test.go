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

// #874: a workflow output template shapes the returned document, preserving
// JSON structure across steps and omitting the default response.
func TestWorkflowExecutor_OutputTemplate(t *testing.T) {
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

	// The response keys are gone; only the output template remains.
	_, hasSteps := decoded[api.FieldSteps]
	assert.False(t, hasSteps, "output template must omit the steps response")
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

// #874: a computed output template leaf keeps the exact string it renders to —
// versions, zero-padded values and other numeric-looking strings are never
// silently coerced to a number — while genuinely numeric expressions (bare
// reference paths, arithmetic) keep their numeric type. No sprig `quote`
// workaround is required.
func TestWorkflowExecutor_OutputTemplate_ComputedLeafKeepsType(t *testing.T) {
	mock := jsonResponder(map[string]string{
		"release": `{"major": 1, "minor": 20, "build": 8}`,
	})
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name:  "versions",
		Steps: []api.WorkflowStep{{ID: "release", Tool: "release"}},
		Output: map[string]interface{}{
			// Computed strings keep their textual form (JSON numbers decode to
			// float64, so int-convert before %d formatting).
			"version": `{{ printf "%d.%d" (int .results.release.major) (int .results.release.minor) }}`,
			"padded":  `{{ printf "%02d" (int .results.release.build) }}`,
			// Bare reference path keeps its original numeric type.
			"buildNum": "{{ .results.release.build }}",
			// A genuinely numeric computed expression stays a number.
			"nextMajor": "{{ add (int .results.release.major) 1 }}",
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{})
	require.NoError(t, err)

	decoded := decodeResult(t, result)
	assert.Equal(t, "1.20", decoded["version"], "computed string leaf must keep its form")
	assert.Equal(t, "08", decoded["padded"], "zero-padded computed leaf must keep leading zero")
	assert.Equal(t, float64(8), decoded["buildNum"], "bare reference path stays numeric")
	assert.Equal(t, float64(2), decoded["nextMajor"], "numeric computed leaf stays a number")
}

// #877: the reserved _debug arg keeps the full response (execution_id, status,
// steps[] with every recorded result) and surfaces the rendered output template
// under "output", while default mode returns only the output template.
func TestWorkflowExecutor_OutputTemplate_DebugResponse(t *testing.T) {
	mock := jsonResponder(map[string]string{
		"pods":   `{"items": [{"name": "a"}, {"name": "b"}]}`,
		"events": `{"items": [1, 2, 3]}`,
	})
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "shaped",
		Args: map[string]api.ArgDefinition{"cluster": {Type: "string", Required: true}},
		Steps: []api.WorkflowStep{
			{ID: "pods", Tool: "pods"}, // not an output step
			{ID: "events", Tool: "events"},
		},
		Output: map[string]interface{}{
			"cluster":      "{{ .input.cluster }}",
			"backoffCount": "{{ len .results.events.items }}",
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{
		"cluster": "prod",
		"_debug":  true,
	})
	require.NoError(t, err)

	decoded := decodeResult(t, result)

	// The full response is present.
	assert.Equal(t, "shaped", decoded["workflow"])
	assert.Equal(t, "completed", decoded[api.FieldStatus])
	steps, ok := decoded[api.FieldSteps].([]interface{})
	require.True(t, ok, "debug response must include steps[]")
	require.Len(t, steps, 2)

	// Every step's result is surfaced regardless of its output flag.
	for _, s := range steps {
		stepMap := s.(map[string]interface{})
		_, hasResult := stepMap["result"]
		assert.True(t, hasResult, "debug mode must surface result for step %v", stepMap["id"])
	}

	// The rendered output template rides along under "output".
	outputTemplate, ok := decoded[fieldOutput].(map[string]interface{})
	require.True(t, ok, "debug response must carry the rendered output template under 'output'")
	assert.Equal(t, "prod", outputTemplate["cluster"])
	assert.Equal(t, float64(3), outputTemplate["backoffCount"])

	// The reserved _debug arg is stripped from the recorded input and not
	// passed to step tools.
	input := decoded["input"].(map[string]interface{})
	_, hasDebug := input["_debug"]
	assert.False(t, hasDebug, "_debug must be stripped from the recorded input")
}

// #877: debug mode on a plain (no output template) workflow surfaces every recorded
// step result, not just the output-flagged ones.
func TestWorkflowExecutor_DebugResponse_NoOutputTemplate(t *testing.T) {
	mock := jsonResponder(map[string]string{
		"producer": `{"token": "abc"}`,
		"consumer": `{"ok": true}`,
	})
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name: "chain",
		Steps: []api.WorkflowStep{
			{ID: "producer", Tool: "producer"}, // neither output nor store
			{ID: "consumer", Tool: "consumer", Output: boolPtr(true)},
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{"_debug": true})
	require.NoError(t, err)

	decoded := decodeResult(t, result)
	steps := decoded[api.FieldSteps].([]interface{})
	require.Len(t, steps, 2)

	producer := steps[0].(map[string]interface{})
	_, hasResult := producer["result"]
	assert.True(t, hasResult, "debug mode must surface the non-output producer's result")
}

// #877: an output template render error does not discard successful step results. The
// caller gets an error plus a recoverable response carrying every step result
// and the output template error message.
func TestWorkflowExecutor_OutputTemplate_ErrorKeepsResults(t *testing.T) {
	mock := jsonResponder(map[string]string{
		"pods": `{"items": [{"name": "a"}]}`,
	})
	executor := NewWorkflowExecutor(mock, nil)

	workflow := &api.Workflow{
		Name:  "broken-output template",
		Steps: []api.WorkflowStep{{ID: "pods", Tool: "pods"}},
		Output: map[string]interface{}{
			// References a step that never ran: missingkey=error fails the render.
			"value": "{{ .results.missing.field }}",
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), workflow, map[string]interface{}{})
	require.Error(t, err, "an output template render error must surface as an error")
	require.NotNil(t, result, "step results must remain recoverable on an output template error")
	assert.True(t, result.IsError)

	decoded := decodeResult(t, result)
	assert.Contains(t, decoded, "output_error")

	// The successful step's result is still recoverable from the response.
	steps := decoded[api.FieldSteps].([]interface{})
	require.Len(t, steps, 1)
	pods := steps[0].(map[string]interface{})
	_, hasResult := pods["result"]
	assert.True(t, hasResult, "the successful step's result must survive the output template error")
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
