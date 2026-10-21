package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
)

// Defaults of a paginate block, matching the list tools of mcp-kubernetes.
const (
	defaultPaginateItemsField = "items"
	defaultPaginateTokenField = "continue"
	defaultPaginateTokenArg   = "continue"
	// paginationField is the object a paginated result carries: how many
	// pages were fetched and whether maxPages cut the listing short.
	paginationField = "pagination"
)

// callTool calls the step's tool -- page after page when the step paginates
// -- and returns the tool's result and its parsed content.
func (we *WorkflowExecutor) callTool(ctx context.Context, s subStepView, args map[string]interface{}) (*mcp.CallToolResult, interface{}, error) {
	if s.Paginate != nil {
		return we.callPaginated(ctx, s, args)
	}
	result, err := we.toolCaller.CallToolInternal(ctx, s.Tool, args)
	if err != nil {
		return nil, nil, err
	}
	return result, parseResultContent(result), nil
}

// callPaginated calls the tool, then again with the token of each page in the
// token argument, until a page carries no token or maxPages pages were
// fetched. The stored result is the last page with the list replaced by
// every page's items and with pagination.pages and pagination.truncated set;
// a truncated result keeps the token the next page would be fetched with. An
// error result on any page is the step's result.
func (we *WorkflowExecutor) callPaginated(ctx context.Context, s subStepView, args map[string]interface{}) (*mcp.CallToolResult, interface{}, error) {
	itemsField := orDefault(s.Paginate.ItemsField, defaultPaginateItemsField)
	tokenField := orDefault(s.Paginate.TokenField, defaultPaginateTokenField)
	tokenArg := orDefault(s.Paginate.TokenArg, defaultPaginateTokenArg)
	maxPages := s.Paginate.MaxPages
	if maxPages <= 0 {
		maxPages = api.DefaultPaginateMaxPages
	}

	var items []interface{}
	pageArgs := args
	for pages := 1; ; pages++ {
		result, err := we.toolCaller.CallToolInternal(ctx, s.Tool, pageArgs)
		if err != nil {
			if pages > 1 {
				return nil, nil, fmt.Errorf("page %d: %w", pages, err)
			}
			return nil, nil, err
		}
		data := parseResultContent(result)
		if result.IsError {
			return result, data, nil
		}
		page, ok := data.(map[string]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("paginate: page %d is not a JSON object", pages)
		}
		list, err := listAt(page, itemsField)
		if err != nil {
			return nil, nil, fmt.Errorf("paginate: page %d: %w", pages, err)
		}
		items = append(items, list...)

		token, _ := valueAt(page, tokenField).(string)
		if token == "" || pages == maxPages {
			if items == nil {
				items = []interface{}{}
			}
			setAt(page, itemsField, items)
			page[paginationField] = map[string]interface{}{
				"pages":     pages,
				"truncated": token != "",
			}
			return result, page, nil
		}
		pageArgs = make(map[string]interface{}, len(args)+1)
		for k, v := range args {
			pageArgs[k] = v
		}
		pageArgs[tokenArg] = token
	}
}

// parseResultContent returns a tool result's first text content parsed as
// JSON, or the text itself when it is not JSON.
func parseResultContent(result *mcp.CallToolResult) interface{} {
	if result == nil || len(result.Content) == 0 {
		return nil
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return nil
	}
	var data interface{}
	if err := json.Unmarshal([]byte(text.Text), &data); err != nil {
		return text.Text
	}
	return data
}

// valueAt returns the value at a dotted path of nested objects, nil when a
// segment is missing.
func valueAt(m map[string]interface{}, path string) interface{} {
	var v interface{} = m
	for _, key := range strings.Split(path, ".") {
		obj, ok := v.(map[string]interface{})
		if !ok {
			return nil
		}
		v = obj[key]
	}
	return v
}

// listAt returns the list at a dotted path; a missing or null one is empty.
func listAt(m map[string]interface{}, path string) ([]interface{}, error) {
	switch v := valueAt(m, path).(type) {
	case []interface{}:
		return v, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("%q is %T, not a list", path, v)
	}
}

// setAt sets the value at a dotted path, creating missing objects on the way.
func setAt(m map[string]interface{}, path string, value interface{}) {
	keys := strings.Split(path, ".")
	for _, key := range keys[:len(keys)-1] {
		next, ok := m[key].(map[string]interface{})
		if !ok {
			next = make(map[string]interface{})
			m[key] = next
		}
		m = next
	}
	m[keys[len(keys)-1]] = value
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
