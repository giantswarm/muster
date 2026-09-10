package metatools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/toolset"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bigCatalogue is a mock handler over n tools of a server and one core tool,
// every description two lines long so a summary is distinguishable from the
// full text; the second line must never appear in a listing.
func bigCatalogue(n int) *mockMetaToolsHandler {
	tools := make([]mcp.Tool, 0, n+1)
	for i := 1; i <= n; i++ {
		t := tagged(fmt.Sprintf("x_srv_t%03d", i), toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: "srv"})
		t.Description = fmt.Sprintf("Server tool %03d.\nFULLDETAILONLY the second line of tool %03d.", i, i)
		tools = append(tools, t)
	}
	tools = append(tools, tagged("core_workflow_list", toolset.ToolOrigin{Kind: toolset.OriginKindCore}))
	return &mockMetaToolsHandler{tools: tools, serversRequiringAuth: []api.ServerAuthInfo{
		{Name: "gh", Status: "auth_required", AuthTool: "core_auth_login"},
	}}
}

func listTools(t *testing.T, p *Provider, args map[string]any) ListToolsResponse {
	t.Helper()
	result, err := p.ExecuteTool(withHeader("", false), "list_tools", args)
	require.NoError(t, err)
	return decodeListTools(t, result)
}

func decodeListTools(t *testing.T, result *api.CallToolResult) ListToolsResponse {
	t.Helper()
	require.NotNil(t, result)
	require.False(t, result.IsError, "expected success, got %v", result.Content)
	var resp ListToolsResponse
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(string)), &resp))
	return resp
}

func TestListTools_DefaultPageIsBoundedAndSummarised(t *testing.T) {
	defer registerMockHandler(bigCatalogue(120))()
	p := NewProvider()

	result, err := p.ExecuteTool(withHeader("", false), "list_tools", nil)
	require.NoError(t, err)
	resp := decodeListTools(t, result)

	assert.Equal(t, defaultListLimit, resp.Filters.Limit)
	assert.Equal(t, 0, resp.Filters.Offset)
	assert.Len(t, resp.Tools, defaultListLimit, "the default page is bounded")
	assert.Equal(t, defaultListLimit, resp.FilteredCount)
	assert.Equal(t, 121, resp.Total)
	assert.Equal(t, 121, resp.TotalTools)
	assert.True(t, resp.Truncated, "the caller is told there is more")
	assert.Equal(t, "x_srv_t001", resp.Tools[0].Name)
	assert.Equal(t, "x_srv_t050", resp.Tools[49].Name)

	text := result.Content[0].(string)
	assert.NotContains(t, text, "FULLDETAILONLY", "full descriptions stay behind describe_tool")
	assert.NotContains(t, text, "inputSchema")
	first := resp.Tools[0]
	assert.Equal(t, "Server tool 001.", first.Summary)
	assert.Empty(t, first.Description)
	assert.Equal(t, "srv", first.Server)
	assert.Equal(t, "tool", first.Kind)

	require.Len(t, resp.ServersRequiringAuth, 1, "servers_requiring_auth is kept")
	assert.Equal(t, "gh", resp.ServersRequiringAuth[0].Name)
	assert.Equal(t, "core_auth_login", resp.ServersRequiringAuth[0].AuthTool)
}

func TestListTools_LimitAndOffsetPageThrough(t *testing.T) {
	defer registerMockHandler(bigCatalogue(7))()
	p := NewProvider()

	page := listTools(t, p, map[string]any{"limit": float64(3)})
	assert.Equal(t, []string{"x_srv_t001", "x_srv_t002", "x_srv_t003"}, names(page.Tools))
	assert.Equal(t, 8, page.Total)
	assert.True(t, page.Truncated)

	page = listTools(t, p, map[string]any{"limit": float64(3), "offset": float64(3)})
	assert.Equal(t, []string{"x_srv_t004", "x_srv_t005", "x_srv_t006"}, names(page.Tools))
	assert.Equal(t, 3, page.Filters.Offset)
	assert.True(t, page.Truncated)

	page = listTools(t, p, map[string]any{"limit": float64(3), "offset": float64(6)})
	assert.Equal(t, []string{"x_srv_t007", "core_workflow_list"}, names(page.Tools))
	assert.Equal(t, 2, page.FilteredCount)
	assert.False(t, page.Truncated, "the last page is not truncated")

	page = listTools(t, p, map[string]any{"offset": float64(100)})
	assert.Empty(t, page.Tools, "an offset past the end is an empty, honest page")
	assert.Equal(t, 8, page.Total)
	assert.False(t, page.Truncated)

	page = listTools(t, p, map[string]any{"limit": 1000})
	assert.Len(t, page.Tools, 8, "a large enough limit is the whole catalogue in one page")
	assert.False(t, page.Truncated)
}

func TestListTools_RejectsInvalidPaging(t *testing.T) {
	defer registerMockHandler(bigCatalogue(3))()
	p := NewProvider()

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"limit": float64(0)}, "limit must be at least 1"},
		{map[string]any{"limit": "ten"}, "limit must be a number"},
		{map[string]any{"offset": float64(-1)}, "offset must be at least 0"},
		{map[string]any{"offset": true}, "offset must be a number"},
	} {
		result, err := p.ExecuteTool(withHeader("", false), "list_tools", tc.args)
		require.NoError(t, err)
		assert.Equal(t, tc.want, errorText(t, result))
	}
}

func TestListTools_EmptyCatalogueIsStructured(t *testing.T) {
	// Unlike filter_tools, list_tools never falls back to a text answer: the
	// servers a sign-in would unlock must stay readable when nothing is
	// listed yet.
	defer registerMockHandler(&mockMetaToolsHandler{serversRequiringAuth: []api.ServerAuthInfo{
		{Name: "gh", Status: "auth_required", AuthTool: "core_auth_login"},
	}})()
	p := NewProvider()

	resp := listTools(t, p, nil)
	assert.Empty(t, resp.Tools)
	assert.Equal(t, 0, resp.Total)
	assert.False(t, resp.Truncated)
	require.Len(t, resp.ServersRequiringAuth, 1)

	// filter_tools keeps its legacy text on the same empty catalogue.
	result, err := p.ExecuteTool(withHeader("", false), "filter_tools", nil)
	require.NoError(t, err)
	assert.Equal(t, "No tools available to filter", result.Content[0].(string))
}

func TestListTools_HeaderToolsetStillBoundsThePage(t *testing.T) {
	defer registerMockHandler(bigCatalogue(60))()
	p := NewProvider()

	result, err := p.ExecuteTool(withHeader("server:srv", true), "list_tools", nil)
	require.NoError(t, err)
	resp := decodeListTools(t, result)
	assert.Equal(t, 60, resp.Total, "the toolset bounds the catalogue")
	assert.Equal(t, 60, resp.TotalTools)
	assert.Len(t, resp.Tools, defaultListLimit, "and the page bounds the answer")
	assert.True(t, resp.Truncated)
	assert.Equal(t, []string{"server:srv"}, resp.Toolset, "the header's toolset is echoed, as filter_tools does")
	assert.NotContains(t, result.Content[0].(string), "core_workflow_list")
	assert.Len(t, resp.ServersRequiringAuth, 1, "servers_requiring_auth is not narrowed by the toolset")

	result, err = p.ExecuteTool(withHeader("server:srv", true), "list_tools", map[string]any{"offset": float64(50)})
	require.NoError(t, err)
	resp = decodeListTools(t, result)
	assert.Len(t, resp.Tools, 10)
	assert.Equal(t, "x_srv_t051", resp.Tools[0].Name)
	assert.False(t, resp.Truncated)

	result, err = p.ExecuteTool(withHeader("preset:none", true), "list_tools", nil)
	require.NoError(t, err)
	resp = decodeListTools(t, result)
	assert.Empty(t, resp.Tools, "preset:none still lists nothing")
	assert.Equal(t, 0, resp.Total)
	assert.False(t, resp.Truncated)

	result, err = p.ExecuteTool(withHeader("preset:foo", true), "list_tools", map[string]any{"limit": float64(5)})
	require.NoError(t, err)
	assert.Equal(t, `toolset [preset:foo] names unknown preset "foo"; known presets: read-only, none, full`, errorText(t, result),
		"the refusal texts are unchanged")
}

func TestListTools_DefaultPageSizeIsTensOfKilobytes(t *testing.T) {
	// The measurement behind #1193: 450 tools with realistic descriptions
	// listed unpaged were 400 KB. The default page must stay far below that
	// however verbose the descriptions are.
	tools := make([]mcp.Tool, 0, 450)
	for i := 0; i < 450; i++ {
		t := tagged(fmt.Sprintf("workflow_incident_runbook_%03d", i), toolset.ToolOrigin{Kind: toolset.OriginKindWorkflow})
		t.Description = strings.Repeat("A very long first line of documentation that keeps going and going. ", 5) +
			"\n" + strings.Repeat("More detail on later lines. ", 40)
		tools = append(tools, t)
	}
	defer registerMockHandler(&mockMetaToolsHandler{tools: tools})()
	p := NewProvider()

	result, err := p.ExecuteTool(withHeader("", false), "list_tools", nil)
	require.NoError(t, err)
	size := len(result.Content[0].(string))
	assert.Less(t, size, 40_000, "default page is %d bytes", size)
	assert.True(t, decodeListTools(t, result).Truncated)
}

func names(tools []ToolInfo) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}
