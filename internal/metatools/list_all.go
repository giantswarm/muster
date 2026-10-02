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

// The text list_resources and list_prompts answer with for an empty catalogue
// in place of a JSON array.
const (
	noResourcesAvailable = "No resources available"
	noPromptsAvailable   = "No prompts available"
)

// The start of describe_resource's and describe_prompt's answer for an item
// the caller's catalogue lacks; the URI or name follows.
const (
	resourceNotFound = "Resource not found: "
	promptNotFound   = "Prompt not found: "
)

// ListAllResources calls list_resources and returns every resource of the
// caller's catalogue with the server it comes from: one entry per server
// that exposes a URI.
func ListAllResources(ctx context.Context, call ToolCaller) ([]ResourceInfo, error) {
	return listCapabilities[ResourceInfo](ctx, call, ToolListResources, noResourcesAvailable)
}

// ListAllPrompts calls list_prompts and returns every prompt of the caller's
// catalogue with the server it comes from.
func ListAllPrompts(ctx context.Context, call ToolCaller) ([]PromptInfo, error) {
	return listCapabilities[PromptInfo](ctx, call, ToolListPrompts, noPromptsAvailable)
}

// listCapabilities calls an unpaged list meta-tool and decodes its JSON
// array. The empty text is the tool's answer for an empty catalogue.
func listCapabilities[T any](ctx context.Context, call ToolCaller, tool, empty string) ([]T, error) {
	result, err := call(ctx, tool, map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", tool, err)
	}
	if result == nil {
		return nil, fmt.Errorf("nil result from %s", tool)
	}
	if result.IsError {
		return nil, fmt.Errorf("%s failed: %s", tool, resultText(result))
	}
	text := resultText(result)
	if text == empty {
		return nil, nil
	}
	var items []T
	if err := json.Unmarshal([]byte(text), &items); err != nil {
		return nil, fmt.Errorf("failed to parse %s response: %w", tool, err)
	}
	return items, nil
}

// PromptDetail is a describe_prompt answer: the prompt, the arguments it
// declares and the server it comes from.
type PromptDetail struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Arguments   []mcp.PromptArgument `json:"arguments,omitempty"`
	Server      string               `json:"server"`
}

// DescribePrompt calls describe_prompt for one prompt and returns it with its
// arguments and server; nil when the caller's catalogue has no such prompt.
func DescribePrompt(ctx context.Context, call ToolCaller, name string) (*PromptDetail, error) {
	return describe[PromptDetail](ctx, call, ToolDescribePrompt, map[string]any{"name": name}, promptNotFound)
}

// DescribePromptArguments calls describe_prompt for one prompt and returns
// the arguments it declares, which list_prompts does not report.
func DescribePromptArguments(ctx context.Context, call ToolCaller, name string) ([]mcp.PromptArgument, error) {
	detail, err := DescribePrompt(ctx, call, name)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, fmt.Errorf("%s failed: %s%s", ToolDescribePrompt, promptNotFound, name)
	}
	return detail.Arguments, nil
}

// DescribeResource calls describe_resource for one URI and returns the
// resource with its server; nil when the caller's catalogue has no such
// resource. server picks one of several servers exposing the URI; without
// it such a URI is an error naming them.
func DescribeResource(ctx context.Context, call ToolCaller, uri, server string) (*ResourceInfo, error) {
	args := map[string]any{"uri": uri}
	if server != "" {
		args[ArgServer] = server
	}
	return describe[ResourceInfo](ctx, call, ToolDescribeResource, args, resourceNotFound)
}

// describe calls a describe meta-tool and decodes its JSON answer. The
// tool's not-found answer, which starts with notFound, is a nil result.
func describe[T any](ctx context.Context, call ToolCaller, tool string, args map[string]any, notFound string) (*T, error) {
	result, err := call(ctx, tool, args)
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", tool, err)
	}
	if result == nil {
		return nil, fmt.Errorf("nil result from %s", tool)
	}
	text := resultText(result)
	if result.IsError {
		if strings.HasPrefix(text, notFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s failed: %s", tool, text)
	}
	var detail T
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		return nil, fmt.Errorf("failed to parse %s response: %w", tool, err)
	}
	return &detail, nil
}
