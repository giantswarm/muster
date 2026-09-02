package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// secretMarker stands in for a credential handed to a workflow as an input
// and returned by a step. "eyJ" is the base64url prefix every JWT shares.
const secretMarker = "eyJsecret-marker"

// captureDebugLog routes the package logger into a buffer at debug level for
// the duration of the test -- the value dumps this file guards against were
// all debug lines -- then points it back at a discard writer so later tests
// do not write into a buffer nobody reads.
func captureDebugLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &buf)
	t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, io.Discard) })
	return &buf
}

// echoingToolCaller behaves like a backend that returns the credential it was
// given, so the secret is present in the recorded step result as well as in
// the workflow input.
func echoingToolCaller() *scriptedToolCaller {
	return &scriptedToolCaller{responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
		payload, err := json.Marshal(map[string]interface{}{
			"status": "ok",
			"echoed": args["token"],
			"nested": map[string]interface{}{"credential": args["token"]},
		})
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(string(payload))}}, nil
	}}
}

// TestExecuteWorkflow_DebugLogNamesArgsAndResultsWithoutValues pins that a
// workflow run at debug level logs which arguments and result fields were
// present -- workflow name, step ids, key names -- but never their values.
// The secret travels through input validation, step argument resolution
// (plain and templated), the recorded step result, a from_step condition
// whose json_path expectation is templated from the input, and the merged
// final result; muster 5.7.11 dumped every one of those with %+v.
func TestExecuteWorkflow_DebugLogNamesArgsAndResultsWithoutValues(t *testing.T) {
	logBuf := captureDebugLog(t)
	executor := NewWorkflowExecutor(echoingToolCaller(), nil)

	wf := &api.Workflow{
		Name: "secret-passthrough",
		Args: map[string]api.ArgDefinition{
			"token":  {Type: "string", Required: true},
			"region": {Type: "string", Default: "eu-west-1"},
		},
		Steps: []api.WorkflowStep{
			{
				ID:   "fetch",
				Tool: "x_vault_use_token",
				Args: map[string]interface{}{
					"token": "{{ .input.token }}",
					"label": "token={{ .input.token }}",
				},
				Store: true,
			},
			{
				ID:   "reuse",
				Tool: "x_vault_use_token",
				Args: map[string]interface{}{"token": "{{ .results.fetch.nested.credential }}"},
				Condition: &api.WorkflowCondition{
					FromStep: "fetch",
					Expect: api.WorkflowConditionExpectation{
						Success:  true,
						JsonPath: map[string]interface{}{"echoed": "{{ .input.token }}"},
					},
				},
			},
			{
				ID:   "skipped",
				Tool: "x_vault_use_token",
				Args: map[string]interface{}{"token": "{{ .input.token }}"},
				Condition: &api.WorkflowCondition{
					FromStep: "fetch",
					Expect: api.WorkflowConditionExpectation{
						Success:  true,
						JsonPath: map[string]interface{}{"status": "{{ .input.token }}"},
					},
				},
			},
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"token": secretMarker})
	require.NoError(t, err)
	require.False(t, result.IsError)

	// The redaction is about the logs, not the workflow: the caller still
	// receives the values.
	text := result.Content[0].(mcp.TextContent).Text
	require.Contains(t, text, secretMarker)
	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &decoded))
	require.Equal(t, "secret-passthrough", decoded["workflow"])
	require.Equal(t, secretMarker, decoded["echoed"], "the last step's result is merged into the top level")

	logged := logBuf.String()
	require.NotEmpty(t, logged, "debug logging must be active for this test to prove anything")
	require.Contains(t, logged, "workflow=secret-passthrough")
	require.Contains(t, logged, "args: token (defined: region, token)", "argument names should be logged, sorted")
	require.Contains(t, logged, "final args: region, token", "defaults show up by name once applied")
	require.Contains(t, logged, "Step fetch resolved args: label, token")
	require.Contains(t, logged, "Recorded result from step fetch: object{echoed, nested, status}")
	require.Contains(t, logged, "JSON path validation failed: path=status, expected=string(len=16), actual=string(len=2)")
	require.Contains(t, logged, "Final result for workflow secret-passthrough: keys=")
	require.Contains(t, logged, "Final result JSON for workflow secret-passthrough:")
	require.NotContains(t, logged, secretMarker, "workflow argument and result values must not be logged:\n%s", logged)
	require.NotContains(t, logged, "eu-west-1", "applied default values must not be logged as final args:\n%s", logged)
}

// TestExecuteWorkflow_OutputTemplateLogSizeOnly covers the output-template
// return path, which used to log the rendered document verbatim.
func TestExecuteWorkflow_OutputTemplateLogSizeOnly(t *testing.T) {
	logBuf := captureDebugLog(t)
	executor := NewWorkflowExecutor(echoingToolCaller(), nil)

	wf := &api.Workflow{
		Name: "secret-output",
		Args: map[string]api.ArgDefinition{"token": {Type: "string", Required: true}},
		Steps: []api.WorkflowStep{
			{ID: "fetch", Tool: "x_vault_use_token", Args: map[string]interface{}{"token": "{{ .input.token }}"}},
		},
		Output: map[string]interface{}{
			"credential": "{{ .results.fetch.echoed }}",
			"count":      1,
		},
	}

	result, err := executor.ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"token": secretMarker})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content[0].(mcp.TextContent).Text, secretMarker)

	logged := logBuf.String()
	require.Contains(t, logged, "Rendered output template for workflow secret-output: object{count, credential}")
	require.NotContains(t, logged, secretMarker, "rendered output values must not be logged:\n%s", logged)
}

// TestValidateInputs_MissingRequiredLogsNamesOnly covers the error path: the
// missing-field log used to print the whole args map, values included.
func TestValidateInputs_MissingRequiredLogsNamesOnly(t *testing.T) {
	logBuf := captureDebugLog(t)
	executor := NewWorkflowExecutor(&mockToolCaller{}, nil)

	err := executor.validateInputs(
		map[string]api.ArgDefinition{
			"token":   {Type: "string"},
			"cluster": {Type: "string", Required: true},
		},
		map[string]interface{}{"token": secretMarker},
	)
	require.EqualError(t, err, "required field 'cluster' is missing")

	logged := logBuf.String()
	require.Contains(t, logged, "Required field 'cluster' is missing from args (present: token)")
	require.NotContains(t, logged, secretMarker, "argument values must not be logged:\n%s", logged)
}
