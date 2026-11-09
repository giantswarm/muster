package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const testClientSecret = "s3cr3t-never-recorded" //nolint:gosec // G101: a test value

// A workflow input passed into a sensitive tool argument (clientCredentials)
// is redacted in the stored execution record; other inputs are kept, and the
// workflow itself runs with the values as given.
func TestRecordedInput_RedactsSensitiveToolArgs(t *testing.T) {
	api.RegisterSensitiveToolArgs("core_mcpserver_create", []api.ArgMetadata{{Name: "name"}, {Name: "clientCredentials", Sensitive: true}})
	t.Cleanup(func() { api.RegisterSensitiveToolArgs("core_mcpserver_create", nil) })

	workflow := &api.Workflow{
		Name: "connect-hubspot",
		Steps: []api.WorkflowStep{{
			ID:   "create",
			Tool: "core_mcpserver_create",
			Args: map[string]interface{}{
				"name": "{{.input.name}}",
				"clientCredentials": map[string]interface{}{
					"clientId":     "{{ .input.clientId }}",
					"clientSecret": `{{ index .input "clientSecret" }}`,
				},
			},
		}},
		OnFailure: []api.WorkflowSubStep{{ID: "again", Tool: "core_mcpserver_create", Args: map[string]interface{}{
			"clientCredentials": "{{.input.fallback}}",
		}}},
	}
	args := map[string]interface{}{"name": "hubspot", "clientId": "id", "clientSecret": testClientSecret, "fallback": testClientSecret}

	storage := NewExecutionStorage(t.TempDir())
	tracker := NewExecutionTracker(storage)
	var ran map[string]interface{}
	_, execution, err := tracker.TrackExecution(context.Background(), workflow.Name, recordedInput(workflow, args), func() (*mcp.CallToolResult, error) {
		ran = args
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("{}")}}, nil
	})
	require.NoError(t, err)

	stored, err := storage.Get(context.Background(), execution.ExecutionID)
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{
		"name": "hubspot", "clientId": api.RedactedValue, "clientSecret": api.RedactedValue, "fallback": api.RedactedValue,
	}, stored.Input)
	raw, err := json.Marshal(stored)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), testClientSecret)
	assert.Equal(t, testClientSecret, ran["clientSecret"], "the workflow runs with the value")
	assert.Equal(t, testClientSecret, args["clientSecret"], "the caller's args are not changed")
}

func TestRecordedInput_NothingSensitive(t *testing.T) {
	workflow := &api.Workflow{Steps: []api.WorkflowStep{{ID: "list", Tool: "core_service_list", Args: map[string]interface{}{"x": "{{.input.x}}"}}}}
	args := map[string]interface{}{"x": "y"}
	assert.Equal(t, args, recordedInput(workflow, args))
}
