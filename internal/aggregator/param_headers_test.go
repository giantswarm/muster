package aggregator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

func TestSubjectGrantCall_MirrorsParamHeadersAfterPoolEviction(t *testing.T) {
	mcpSrv := mcpgoserver.NewMCPServer("github", "1.0.0", mcpgoserver.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.NewToolWithRawSchema("pull_request_read", "",
		json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string","x-mcp-header":"Owner"}},"required":["owner"]}`)),
		func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("diff of " + request.GetString("owner", "")), nil
		})
	backend, _ := serveGrantBackend(t, mcpSrv, subjectGrantToken)

	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, api.GrantScopeSubject)
	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := api.WithSessionID(api.WithSubject(t.Context(), "alice"), "sess-1")
	for i := range 3 {
		if i == 2 {
			a.connPool.Evict("sess-1", "github")
		}
		result, err := a.callToolWithTokenExchangeRetry(ctx, "github", "pull_request_read",
			map[string]any{"owner": "giantswarm"}, "sess-1", "alice")
		require.NoError(t, err)
		require.False(t, result.IsError, "%v", result.Content)
		require.Equal(t, "diff of giantswarm", result.Content[0].(mcp.TextContent).Text)
	}
}

func TestCallToolInternal_MirrorsParamHeadersAfterPoolEviction(t *testing.T) {
	mcpSrv := mcpgoserver.NewMCPServer("github", "1.0.0", mcpgoserver.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.NewToolWithRawSchema("pull_request_read", "",
		json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string","x-mcp-header":"Owner"}},"required":["owner"]}`)),
		func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("diff of " + request.GetString("owner", "")), nil
		})
	backend, _ := serveGrantBackend(t, mcpSrv, subjectGrantToken)

	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, api.GrantScopeSubject)
	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := api.WithSessionID(api.WithSubject(t.Context(), "alice"), "sess-1")
	_, err := a.callToolWithTokenExchangeRetry(ctx, "github", "pull_request_read",
		map[string]any{"owner": "giantswarm"}, "sess-1", "alice")
	require.NoError(t, err)
	a.connPool.Evict("sess-1", "github")

	result, err := a.CallToolInternal(ctx, "x_github_pull_request_read", map[string]any{"owner": "giantswarm"})
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	require.Equal(t, "diff of giantswarm", result.Content[0].(mcp.TextContent).Text)
}
