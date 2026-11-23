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

// A backend that mirrors a parameter into Mcp-Param-* (SEP-2243), as GitHub's
// hosted MCP server does, answers a person's call on the connection that
// adopted the subject grant, on the pooled connection after it, and on the
// fresh session client of an authenticated session whose pooled connection is
// gone (evicted, refreshed, or held by another replica).
func TestCallToolWithTokenExchangeRetry_MirrorsParamHeaders(t *testing.T) {
	mcpSrv := mcpgoserver.NewMCPServer("github", "1.0.0", mcpgoserver.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.NewToolWithRawSchema("pull_request_read", "",
		json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string","x-mcp-header":"Owner"}},"required":["owner"]}`)),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("diff of " + req.GetString("owner", "")), nil
		})
	backend, _ := serveGrantBackend(t, mcpSrv, subjectGrantToken)

	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, api.GrantScopeSubject)
	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := sessionContext("sess-1", "alice")
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

// Workflow steps, conditions and pagination call tools through
// CallToolInternal; on a fresh session client their calls carry the
// Mcp-Param-* headers too.
func TestCallToolInternal_MirrorsParamHeadersForWorkflowSteps(t *testing.T) {
	mcpSrv := mcpgoserver.NewMCPServer("github", "1.0.0", mcpgoserver.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.NewToolWithRawSchema("pull_request_read", "",
		json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string","x-mcp-header":"Owner"}},"required":["owner"]}`)),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("diff of " + req.GetString("owner", "")), nil
		})
	backend, _ := serveGrantBackend(t, mcpSrv, subjectGrantToken)

	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, api.GrantScopeSubject)
	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := sessionContext("sess-1", "alice")
	_, err := a.callToolWithTokenExchangeRetry(ctx, "github", "pull_request_read",
		map[string]any{"owner": "giantswarm"}, "sess-1", "alice")
	require.NoError(t, err)
	a.connPool.Evict("sess-1", "github")

	result, err := a.CallToolInternal(ctx, "x_github_pull_request_read", map[string]any{"owner": "giantswarm"})
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	require.Equal(t, "diff of giantswarm", result.Content[0].(mcp.TextContent).Text)
}
