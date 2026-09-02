package aggregator

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

type failingToolProvider struct{ err error }

func (p failingToolProvider) GetTools() []api.ToolMetadata { return nil }

func (p failingToolProvider) ExecuteTool(context.Context, string, map[string]any) (*api.CallToolResult, error) {
	return nil, p.err
}

// TestMetaToolHandler_FailureLogNamesArgsWithoutValues pins that a failed
// meta-tool call logs which arguments were sent, not what they held. Meta-tool
// arguments are caller-supplied and can carry credentials -- an MCPServer spec
// with an Authorization header is the obvious case -- and the failure log used
// to print the whole map with %+v.
func TestMetaToolHandler_FailureLogNamesArgsWithoutValues(t *testing.T) {
	logBuf := captureLog(t)

	handler := (&AggregatorServer{}).createMetaToolHandler(failingToolProvider{err: errors.New("spec rejected")}, "core_mcpserver_create")

	req := mcp.CallToolRequest{}
	req.Params.Name = "core_mcpserver_create"
	req.Params.Arguments = map[string]any{
		"name": "backend",
		"headers": map[string]any{
			"Authorization": "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJzZWNyZXQifQ.sig",
		},
	}

	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)

	logged := logBuf.String()
	require.Contains(t, logged, "core_mcpserver_create")
	require.Contains(t, logged, "headers, name", "argument names should be logged, sorted")
	require.NotContains(t, logged, "eyJ", "argument values must not be logged: %s", logged)
	require.NotContains(t, logged, "backend", "argument values must not be logged: %s", logged)
}
