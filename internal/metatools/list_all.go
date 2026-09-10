package metatools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolCaller invokes a meta-tool by name — the shape every MCP client in this
// repository exposes for a direct (unwrapped) tool call.
type ToolCaller func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error)

// listAllPageSize is the page ListAllTools asks list_tools for. Large enough
// that a typical catalogue arrives in one round trip, so the listing is a
// single snapshot; the loop below follows truncated for anything bigger.
const listAllPageSize = 1000

// ListAllTools pages through list_tools until the caller's whole catalogue has
// been read and returns it as one response. It is the compatibility path for
// clients that enumerate every tool — the REPL and CLI listings, tab
// completion, the test harness — now that a single list_tools call answers
// with a bounded page. servers_requiring_auth is taken from the first page;
// it is neither paged nor narrowed by a toolset.
func ListAllTools(ctx context.Context, call ToolCaller) (*ListToolsResponse, error) {
	var all *ListToolsResponse
	for offset := 0; ; {
		page, err := listToolsPage(ctx, call, listAllPageSize, offset)
		if err != nil {
			return nil, err
		}
		if all == nil {
			all = page
		} else {
			all.Tools = append(all.Tools, page.Tools...)
			all.FilteredCount = len(all.Tools)
			all.Total, all.Truncated = page.Total, page.Truncated
		}
		// A page carrying no tools cannot advance the offset: stop rather
		// than loop, whatever truncated claims.
		if !page.Truncated || len(page.Tools) == 0 {
			return all, nil
		}
		offset += len(page.Tools)
	}
}

// listToolsPage calls list_tools for one page and decodes it. An error result
// is returned as an error carrying the result's text.
func listToolsPage(ctx context.Context, call ToolCaller, limit, offset int) (*ListToolsResponse, error) {
	result, err := call(ctx, ToolListTools, map[string]any{ArgLimit: limit, ArgOffset: offset})
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", ToolListTools, err)
	}
	return ParseListToolsResult(result)
}

// ParseListToolsResult decodes a list_tools result. An error result becomes an
// error carrying its text; a result without decodable JSON text is an error.
func ParseListToolsResult(result *mcp.CallToolResult) (*ListToolsResponse, error) {
	if result == nil {
		return nil, fmt.Errorf("nil result from %s", ToolListTools)
	}
	if result.IsError {
		return nil, fmt.Errorf("%s failed: %s", ToolListTools, resultText(result))
	}
	for _, content := range result.Content {
		text, ok := mcp.AsTextContent(content)
		if !ok {
			continue
		}
		var resp ListToolsResponse
		if err := json.Unmarshal([]byte(text.Text), &resp); err != nil {
			return nil, fmt.Errorf("failed to parse %s response: %w", ToolListTools, err)
		}
		return &resp, nil
	}
	return nil, fmt.Errorf("no content in %s response", ToolListTools)
}

// resultText joins the text contents of a result, for error messages.
func resultText(result *mcp.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := mcp.AsTextContent(content); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "; ")
}
