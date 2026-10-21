package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/giantswarm/muster/v5/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pagedCaller answers each call with the page its continue argument names.
func pagedCaller(pages map[string]func() (*mcp.CallToolResult, error)) *scriptedToolCaller {
	return &scriptedToolCaller{
		responder: func(_ string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			token, _ := args["continue"].(string)
			return pages[token]()
		},
	}
}

func page(text string) func() (*mcp.CallToolResult, error) {
	return func() (*mcp.CallToolResult, error) { return textResult(text), nil }
}

func TestCallPaginated(t *testing.T) {
	step := subStepView{ID: "pods", Tool: "list", Paginate: &api.WorkflowPaginate{}}

	t.Run("an error on a later page names the page", func(t *testing.T) {
		caller := pagedCaller(map[string]func() (*mcp.CallToolResult, error){
			"":   page(`{"items": [1], "continue": "p2"}`),
			"p2": func() (*mcp.CallToolResult, error) { return nil, errors.New("connection reset") },
		})
		_, _, err := NewWorkflowExecutor(caller, nil).callTool(context.Background(), step, map[string]interface{}{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "page 2: connection reset")
	})

	t.Run("an error result on a later page is the step's result", func(t *testing.T) {
		caller := pagedCaller(map[string]func() (*mcp.CallToolResult, error){
			"": page(`{"items": [1], "continue": "p2"}`),
			"p2": func() (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("token expired")}, IsError: true}, nil
			},
		})
		result, data, err := NewWorkflowExecutor(caller, nil).callTool(context.Background(), step, map[string]interface{}{})
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Equal(t, "token expired", data)
	})

	t.Run("a page that is not an object", func(t *testing.T) {
		caller := pagedCaller(map[string]func() (*mcp.CallToolResult, error){"": page(`[1, 2]`)})
		_, _, err := NewWorkflowExecutor(caller, nil).callTool(context.Background(), step, map[string]interface{}{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a JSON object")
	})

	t.Run("items that are not a list", func(t *testing.T) {
		caller := pagedCaller(map[string]func() (*mcp.CallToolResult, error){"": page(`{"items": "many"}`)})
		_, _, err := NewWorkflowExecutor(caller, nil).callTool(context.Background(), step, map[string]interface{}{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"items" is string, not a list`)
	})

	t.Run("a page without items adds none", func(t *testing.T) {
		caller := pagedCaller(map[string]func() (*mcp.CallToolResult, error){
			"":   page(`{"items": [1], "continue": "p2"}`),
			"p2": page(`{"kind": "List"}`),
		})
		_, data, err := NewWorkflowExecutor(caller, nil).callTool(context.Background(), step, map[string]interface{}{"namespace": "shop"})
		require.NoError(t, err)
		got := data.(map[string]interface{})
		assert.Equal(t, []interface{}{float64(1)}, got["items"])
		assert.Equal(t, map[string]interface{}{"pages": 2, "truncated": false}, got[paginationField])
	})

	t.Run("an empty listing stores an empty list", func(t *testing.T) {
		caller := pagedCaller(map[string]func() (*mcp.CallToolResult, error){"": page(`{"items": null}`)})
		_, data, err := NewWorkflowExecutor(caller, nil).callTool(context.Background(), step, map[string]interface{}{})
		require.NoError(t, err)
		assert.Equal(t, []interface{}{}, data.(map[string]interface{})["items"])
	})

	t.Run("the default maxPages", func(t *testing.T) {
		caller := &scriptedToolCaller{
			responder: func(_ string, _ map[string]interface{}) (*mcp.CallToolResult, error) {
				return textResult(`{"items": [1], "continue": "more"}`), nil
			},
		}
		_, data, err := NewWorkflowExecutor(caller, nil).callTool(context.Background(), step, map[string]interface{}{})
		require.NoError(t, err)
		got := data.(map[string]interface{})
		assert.Len(t, got["items"], api.DefaultPaginateMaxPages)
		assert.Equal(t, map[string]interface{}{"pages": api.DefaultPaginateMaxPages, "truncated": true}, got[paginationField])
		assert.Equal(t, "more", got["continue"], "a truncated result keeps the token to resume from")
		assert.Len(t, caller.calls, api.DefaultPaginateMaxPages)
	})

	t.Run("later pages keep the step's other arguments", func(t *testing.T) {
		caller := pagedCaller(map[string]func() (*mcp.CallToolResult, error){
			"":   page(`{"items": [1], "continue": "p2"}`),
			"p2": page(`{"items": [2]}`),
		})
		args := map[string]interface{}{"namespace": "shop"}
		_, _, err := NewWorkflowExecutor(caller, nil).callTool(context.Background(), step, args)
		require.NoError(t, err)
		require.Len(t, caller.calls, 2)
		assert.Equal(t, map[string]interface{}{"namespace": "shop", "continue": "p2"}, caller.calls[1].args)
		assert.Equal(t, map[string]interface{}{"namespace": "shop"}, args, "the step's own arguments are not changed")
	})
}

func TestSetAtCreatesMissingObjects(t *testing.T) {
	m := map[string]interface{}{"data": "scalar"}
	setAt(m, "data.rows", []interface{}{1})
	setAt(m, "meta.page.items", []interface{}{2})
	assert.Equal(t, []interface{}{1}, valueAt(m, "data.rows"))
	assert.Equal(t, []interface{}{2}, valueAt(m, "meta.page.items"))
	assert.Nil(t, valueAt(m, "meta.missing.items"))
}
