package metatools

import (
	"context"
	"testing"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAdapter(t *testing.T) {
	adapter := NewAdapter()
	require.NotNil(t, adapter)
	assert.NotNil(t, adapter.provider)
}

func TestAdapter_Register(t *testing.T) {
	adapter := NewAdapter()

	// Ensure no handler is registered initially
	api.RegisterMetaTools(nil)
	assert.Nil(t, api.GetMetaTools())

	// Register the adapter
	adapter.Register()

	// Verify the adapter is registered
	handler := api.GetMetaTools()
	require.NotNil(t, handler)

	// Clean up
	api.RegisterMetaTools(nil)
}

func TestAdapter_GetProvider(t *testing.T) {
	adapter := NewAdapter()
	provider := adapter.GetProvider()
	require.NotNil(t, provider)
	assert.Same(t, adapter.provider, provider)
}

func TestAdapter_GetTools(t *testing.T) {
	adapter := NewAdapter()
	tools := adapter.GetTools()

	// Should return all meta-tools
	assert.Len(t, tools, 11)

	// Verify tool names
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	assert.True(t, toolNames["list_tools"])
	assert.True(t, toolNames["call_tool"])
	assert.True(t, toolNames["describe_tool"])
}

func TestAdapter_ExecuteTool(t *testing.T) {
	adapter := NewAdapter()
	ctx := context.Background()

	// Register a mock handler for the test
	mock := &mockMetaToolsHandler{
		tools: []mcp.Tool{
			{Name: "tool1", Description: "Test tool"},
		},
	}
	api.RegisterMetaTools(mock)
	defer api.RegisterMetaTools(nil)

	result, err := adapter.ExecuteTool(ctx, "list_tools", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
}

func TestAdapter_ListTools(t *testing.T) {
	adapter := NewAdapter()
	ctx := context.Background()

	// The standalone adapter returns empty list (awaiting integration)
	tools, err := adapter.ListTools(ctx)
	require.NoError(t, err)
	assert.Empty(t, tools)
}

func TestAdapter_CallTool(t *testing.T) {
	adapter := NewAdapter()
	ctx := context.Background()

	// The standalone adapter returns an error result (awaiting integration)
	result, err := adapter.CallTool(ctx, "some_tool", nil)
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestAdapter_ListResources(t *testing.T) {
	adapter := NewAdapter()
	ctx := context.Background()

	resources, err := adapter.ListResources(ctx)
	require.NoError(t, err)
	assert.Empty(t, resources)
}

func TestAdapter_GetResource(t *testing.T) {
	adapter := NewAdapter()
	ctx := context.Background()

	result, err := adapter.GetResource(ctx, "file://test.txt")
	require.NoError(t, err)
	assert.Empty(t, result.Contents)
}

func TestAdapter_ListPrompts(t *testing.T) {
	adapter := NewAdapter()
	ctx := context.Background()

	prompts, err := adapter.ListPrompts(ctx)
	require.NoError(t, err)
	assert.Empty(t, prompts)
}

func TestAdapter_GetPrompt(t *testing.T) {
	adapter := NewAdapter()
	ctx := context.Background()

	result, err := adapter.GetPrompt(ctx, "test_prompt", nil)
	require.NoError(t, err)
	assert.Empty(t, result.Messages)
}
