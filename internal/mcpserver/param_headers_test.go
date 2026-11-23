package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
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
	srv := server.NewMCPServer("param-header-backend", "test", server.WithToolCapabilities(true))
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
	counting := &countingLister{}
	c := connect(t, t.Context(), url)
	counting.MCPClient = c.client
	c.client = counting

	for range 3 {
		_, err := c.CallTool(t.Context(), "pull_request_read", map[string]any{"owner": "giantswarm"})
		require.NoError(t, err)
	}
	require.Equal(t, 1, counting.lists)
}

// countingLister counts the tools/list requests sent through an mcp-go client.
type countingLister struct {
	client.MCPClient
	lists int
}

func (c *countingLister) ListTools(ctx context.Context, req mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	c.lists++
	return c.MCPClient.ListTools(ctx, req)
}
