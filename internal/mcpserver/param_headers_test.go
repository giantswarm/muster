package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// paramHeaderSchema declares owner as mirrored into Mcp-Param-Owner
// (SEP-2243), as GitHub's hosted MCP server does.
const paramHeaderSchema = `{"type":"object","properties":{"owner":{"type":"string","x-mcp-header":"Owner"}},"required":["owner"]}`

// newParamHeaderBackend serves tools whose owner parameter must travel in
// Mcp-Param-Owner; the mcp-go server refuses a call without it.
func newParamHeaderBackend(t *testing.T, tools ...string) (*server.MCPServer, string) {
	t.Helper()
	srv := server.NewMCPServer("param-header-backend", "test", server.WithToolCapabilities(true),
		server.WithMethodCacheHints(mcp.MethodToolsList, 60000, mcp.CacheScopePrivate))
	for _, name := range tools {
		addParamHeaderTool(srv, name)
	}
	ts := httptest.NewServer(server.NewStreamableHTTPServer(srv))
	t.Cleanup(ts.Close)
	return srv, ts.URL
}

func addParamHeaderTool(srv *server.MCPServer, name string) {
	srv.AddTool(mcp.NewToolWithRawSchema(name, "", json.RawMessage(paramHeaderSchema)),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(name + " " + req.GetString("owner", "")), nil
		})
}

func TestCallTool_MirrorsParamHeadersWithoutPriorList(t *testing.T) {
	_, url := newParamHeaderBackend(t, "pull_request_read")
	c := connect(t, t.Context(), url)

	res, err := c.CallTool(t.Context(), "pull_request_read", map[string]any{"owner": "giantswarm"})
	require.NoError(t, err)
	require.Equal(t, "pull_request_read giantswarm", res.Content[0].(mcp.TextContent).Text)
}

func TestCallTool_MirrorsParamHeadersOfToolAddedAfterList(t *testing.T) {
	srv, url := newParamHeaderBackend(t, "get_me")
	c := connect(t, t.Context(), url)
	_, err := c.ListTools(t.Context())
	require.NoError(t, err)

	addParamHeaderTool(srv, "pull_request_read")

	res, err := c.CallTool(t.Context(), "pull_request_read", map[string]any{"owner": "giantswarm"})
	require.NoError(t, err)
	require.Equal(t, "pull_request_read giantswarm", res.Content[0].(mcp.TextContent).Text)
}

func TestCallTool_ListsOnceForRepeatedCalls(t *testing.T) {
	_, url := newParamHeaderBackend(t, "pull_request_read")
	c := connect(t, t.Context(), url)
	counting := &observedToolClient{MCPClient: c.client}
	c.client = counting

	for range 3 {
		_, err := c.CallTool(t.Context(), "pull_request_read", map[string]any{"owner": "giantswarm"})
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, counting.lists.Load())
}

type observedToolClient struct {
	client.MCPClient
	lists atomic.Int32
	calls atomic.Int32
	list  func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error)
	call  func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

func (c *observedToolClient) ListTools(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	c.lists.Add(1)
	if c.list != nil {
		return c.list(ctx, request)
	}
	return c.MCPClient.ListTools(ctx, request)
}

func (c *observedToolClient) ListToolsByPage(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	c.lists.Add(1)
	if c.list != nil {
		return c.list(ctx, request)
	}
	return c.MCPClient.ListToolsByPage(ctx, request)
}

func (c *observedToolClient) CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c.calls.Add(1)
	if c.call != nil {
		return c.call(ctx, request)
	}
	return c.MCPClient.CallTool(ctx, request)
}

func TestCallTool_MirrorsParamHeadersOfToolAddedAfterMissingCall(t *testing.T) {
	backend, url := newParamHeaderBackend(t, "get_me")
	connection := connect(t, t.Context(), url)
	_, err := connection.CallTool(t.Context(), "pull_request_read", map[string]any{"owner": "giantswarm"})
	require.Error(t, err)

	addParamHeaderTool(backend, "pull_request_read")
	result, err := connection.CallTool(t.Context(), "pull_request_read", map[string]any{"owner": "giantswarm"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "pull_request_read giantswarm", result.Content[0].(mcp.TextContent).Text)
}

func TestCallTool_RefreshesChangedParamHeaders(t *testing.T) {
	backend, url := newParamHeaderBackend(t, "pull_request_read")
	connection := connect(t, t.Context(), url)
	_, err := connection.CallTool(t.Context(), "pull_request_read", map[string]any{"owner": "giantswarm"})
	require.NoError(t, err)

	backend.AddTool(mcp.NewToolWithRawSchema("pull_request_read", "", json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string","x-mcp-header":"Organization"}},"required":["owner"]}`)), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !declaresURLElicitation(declaredCapabilities(ctx)) {
			return mcp.NewToolResultError("URL elicitation not declared"), nil
		}
		return mcp.NewToolResultText("updated schema"), nil
	})
	result, err := connection.CallTool(WithCallerCapabilities(t.Context(), urlCapabilities()), "pull_request_read", map[string]any{"owner": "giantswarm"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "updated schema", result.Content[0].(mcp.TextContent).Text)
}

func TestCallTool_MirrorsParamHeadersFromLaterPages(t *testing.T) {
	backend := server.NewMCPServer("paged", "test", server.WithToolCapabilities(false), server.WithPaginationLimit(1))
	addParamHeaderTool(backend, "first")
	addParamHeaderTool(backend, "second")
	httpServer := httptest.NewServer(server.NewStreamableHTTPServer(backend))
	t.Cleanup(httpServer.Close)
	connection := connect(t, t.Context(), httpServer.URL)
	result, err := connection.CallTool(t.Context(), "second", map[string]any{"owner": "giantswarm"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "second giantswarm", result.Content[0].(mcp.TextContent).Text)
}

func TestCallTool_RediscoversParamHeadersAfterReconnect(t *testing.T) {
	_, url := newParamHeaderBackend(t, "pull_request_read")
	connection := connect(t, t.Context(), url)
	_, err := connection.CallTool(t.Context(), "pull_request_read", map[string]any{"owner": "giantswarm"})
	require.NoError(t, err)
	require.NoError(t, connection.Close())
	require.NoError(t, connection.Initialize(t.Context()))
	result, err := connection.CallTool(t.Context(), "pull_request_read", map[string]any{"owner": "giantswarm"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "pull_request_read giantswarm", result.Content[0].(mcp.TextContent).Text)
}

func TestStdioCall_DoesNotPrefetchParamHeaders(t *testing.T) {
	connection := slowStdioClient(t, DefaultTimeout).(*StdioClient)
	require.Equal(t, mcp.ProtocolVersion20260728, connection.NegotiatedProtocolVersion())
	observed := &observedToolClient{MCPClient: connection.client}
	connection.client = observed
	result, err := connection.CallTool(t.Context(), "sleep", map[string]any{"for": "0s"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Zero(t, observed.lists.Load())
}
