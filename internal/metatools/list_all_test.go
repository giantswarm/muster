package metatools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pagedCatalogue fakes a list_tools server over n tools that honours limit
// and offset exactly like the handler does, recording every call.
type pagedCatalogue struct {
	n     int
	calls []map[string]any
}

func (c *pagedCatalogue) call(_ context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if name != ToolListTools {
		return nil, fmt.Errorf("unexpected tool %s", name)
	}
	c.calls = append(c.calls, args)
	limit, offset := args["limit"].(int), args["offset"].(int)
	start, end, truncated := paginate(c.n, limit, offset)
	resp := ListToolsResponse{ServersRequiringAuth: []api.ServerAuthInfo{{Name: "gh", Status: "auth_required", AuthTool: "core_auth_login"}}}
	resp.Total, resp.Truncated = c.n, truncated
	for i := start; i < end; i++ {
		resp.Tools = append(resp.Tools, ToolInfo{Name: fmt.Sprintf("tool_%03d", i), Summary: "summary"})
	}
	resp.FilteredCount = len(resp.Tools)
	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func TestListAllTools_FollowsTruncatedPages(t *testing.T) {
	cat := &pagedCatalogue{n: 2*listAllPageSize + 7}

	resp, err := ListAllTools(context.Background(), cat.call)
	require.NoError(t, err)
	require.Len(t, resp.Tools, cat.n)
	assert.Equal(t, "tool_000", resp.Tools[0].Name)
	assert.Equal(t, fmt.Sprintf("tool_%03d", cat.n-1), resp.Tools[cat.n-1].Name)
	assert.Equal(t, cat.n, resp.Total)
	assert.Equal(t, cat.n, resp.FilteredCount)
	assert.False(t, resp.Truncated, "the merged response is the whole catalogue")
	assert.Len(t, resp.ServersRequiringAuth, 1, "servers_requiring_auth comes from the first page")

	require.Len(t, cat.calls, 3)
	for i, call := range cat.calls {
		assert.Equal(t, listAllPageSize, call["limit"])
		assert.Equal(t, i*listAllPageSize, call["offset"])
	}
}

func TestListAllTools_SinglePage(t *testing.T) {
	cat := &pagedCatalogue{n: 12}

	resp, err := ListAllTools(context.Background(), cat.call)
	require.NoError(t, err)
	assert.Len(t, resp.Tools, 12)
	assert.Len(t, cat.calls, 1, "a catalogue that fits one page is one round trip")
}

func TestListAllTools_LegacyUnpagedShape(t *testing.T) {
	// A server predating paging answers every tool at once and knows neither
	// total nor truncated; the loop must stop after that first page.
	calls := 0
	call := func(_ context.Context, _ string, _ map[string]any) (*mcp.CallToolResult, error) {
		calls++
		return mcp.NewToolResultText(`{"tools":[{"name":"a","description":"A"},{"name":"b","description":"B"}]}`), nil
	}

	resp, err := ListAllTools(context.Background(), call)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	require.Len(t, resp.Tools, 2)
	assert.Equal(t, "A", resp.Tools[0].Text())
}

func TestListAllTools_StopsOnEmptyTruncatedPage(t *testing.T) {
	// A server that claims more pages but returns none cannot be followed:
	// stop instead of looping forever.
	calls := 0
	call := func(_ context.Context, _ string, _ map[string]any) (*mcp.CallToolResult, error) {
		calls++
		return mcp.NewToolResultText(`{"tools":[],"total":5,"truncated":true}`), nil
	}

	resp, err := ListAllTools(context.Background(), call)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Empty(t, resp.Tools)
}

func TestListAllTools_Errors(t *testing.T) {
	transport := errors.New("connection reset")
	for name, tc := range map[string]struct {
		result *mcp.CallToolResult
		err    error
		want   string
	}{
		"transport error":  {err: transport, want: "list_tools failed: connection reset"},
		"error result":     {result: mcp.NewToolResultError(`toolset [] is empty; use "preset:none"`), want: `list_tools failed: toolset [] is empty`},
		"not json":         {result: mcp.NewToolResultText("No tools available"), want: "failed to parse list_tools response"},
		"no text content":  {result: &mcp.CallToolResult{}, want: "no content in list_tools response"},
		"nil result":       {result: nil, want: "nil result from list_tools"},
		"error result too": {result: &mcp.CallToolResult{IsError: true}, want: "list_tools failed: "},
	} {
		t.Run(name, func(t *testing.T) {
			call := func(context.Context, string, map[string]any) (*mcp.CallToolResult, error) { return tc.result, tc.err }
			_, err := ListAllTools(context.Background(), call)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			if tc.err != nil {
				assert.ErrorIs(t, err, transport)
			}
		})
	}
}

func TestToolInfo_Text(t *testing.T) {
	assert.Equal(t, "full", ToolInfo{Description: "full", Summary: "short"}.Text())
	assert.Equal(t, "short", ToolInfo{Summary: "short"}.Text())
	assert.Equal(t, "", ToolInfo{}.Text())
}
