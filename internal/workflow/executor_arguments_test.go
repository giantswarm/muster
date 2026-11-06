package workflow

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

func TestWorkflowExecutor_DefaultArguments(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		arguments map[string]interface{}
		message   string
	}{
		{name: "nil arguments", message: "default value"},
		{name: "empty arguments", arguments: map[string]interface{}{}, message: "default value"},
		{name: "explicit argument", arguments: map[string]interface{}{"message": "provided value"}, message: "provided value"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			caller := &mockToolCaller{}
			executor := NewWorkflowExecutor(caller, nil)
			workflow := &api.Workflow{
				Name: "default-arguments",
				Args: map[string]api.ArgDefinition{
					"message": {Type: "string", Default: "default value"},
				},
				Steps: []api.WorkflowStep{{
					ID:   "echo",
					Tool: "echo",
					Args: map[string]interface{}{"text": "{{ .input.message }}"},
				}},
			}

			result, err := executor.ExecuteWorkflow(t.Context(), workflow, testCase.arguments)
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Len(t, caller.calls, 1)
			require.Equal(t, testCase.message, caller.calls[0].args["text"])
			require.Equal(t, map[string]interface{}{"message": testCase.message}, decodeResult(t, result)[api.FieldInput])
		})
	}
}

func TestWorkflowExecutor_NilArgumentsRequiredField(t *testing.T) {
	caller := &mockToolCaller{}
	workflow := &api.Workflow{
		Name: "required-argument",
		Args: map[string]api.ArgDefinition{
			"message": {Type: "string", Required: true},
		},
	}

	result, err := NewWorkflowExecutor(caller, nil).ExecuteWorkflow(t.Context(), workflow, nil)
	require.EqualError(t, err, "input validation failed: required field 'message' is missing")
	require.Nil(t, result)
	require.Empty(t, caller.calls)
}
