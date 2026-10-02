package aggregator

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/mcpserver"
)

// TestProgressRelayBridgesDownstreamProgressToTheCaller proves the
// production option chain relays a downstream tool's progress to the client
// that called through muster, under the client's own token, and that a call
// without a token gets none.
func TestProgressRelayBridgesDownstreamProgressToTheCaller(t *testing.T) {
	backend := server.NewMCPServer("backend", "test")
	backend.AddTool(mcp.NewTool("work"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if req.Params.Meta == nil || req.Params.Meta.ProgressToken == nil {
			return mcp.NewToolResultText("no token"), nil
		}
		for i := 1; i <= 3; i++ {
			err := server.ServerFromContext(ctx).SendNotificationToClient(ctx, string(mcp.MethodNotificationProgress), map[string]any{
				"progressToken": req.Params.Meta.ProgressToken,
				"progress":      float64(i),
				"total":         float64(3),
			})
			if err != nil {
				return nil, err
			}
		}
		return mcp.NewToolResultText("done"), nil
	})
	backendTS := httptest.NewServer(server.NewStreamableHTTPServer(backend))
	t.Cleanup(backendTS.Close)

	downstream := mcpserver.NewStreamableHTTPClientWithHeaders(backendTS.URL, nil)
	t.Cleanup(func() { _ = downstream.Close() })
	require.NoError(t, downstream.Initialize(t.Context()))

	muster := server.NewMCPServer("muster-aggregator-test", "test", mcpServerOptions()...)
	muster.AddTool(mcp.NewTool("x_backend_work"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return downstream.CallTool(ctx, "work", nil)
	})
	musterTS := httptest.NewServer(server.NewStreamableHTTPServer(muster))
	t.Cleanup(musterTS.Close)

	caller, err := client.NewStreamableHttpClient(musterTS.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = caller.Close() })
	var progress []map[string]any
	caller.OnNotification(func(n mcp.JSONRPCNotification) {
		if n.Method == string(mcp.MethodNotificationProgress) {
			progress = append(progress, n.Params.AdditionalFields)
		}
	})
	require.NoError(t, caller.Start(t.Context()))
	_, err = caller.Initialize(t.Context(), mcp.InitializeRequest{})
	require.NoError(t, err)

	result, err := caller.CallTool(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "x_backend_work",
		Meta: &mcp.Meta{ProgressToken: "caller-7"},
	}})
	require.NoError(t, err)
	assert.Equal(t, "done", result.Content[0].(mcp.TextContent).Text)
	require.Len(t, progress, 3)
	for i, params := range progress {
		assert.Equal(t, "caller-7", params["progressToken"])
		assert.Equal(t, float64(i+1), params["progress"])
		assert.Equal(t, float64(3), params["total"])
	}

	progress = nil
	result, err = caller.CallTool(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "x_backend_work"}})
	require.NoError(t, err)
	assert.Equal(t, "no token", result.Content[0].(mcp.TextContent).Text)
	assert.Empty(t, progress)
}
