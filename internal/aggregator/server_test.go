package aggregator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMCPClient implements MCPClient for testing
type mockMCPClient struct {
	initialized bool
	tools       []mcp.Tool
	resources   []mcp.Resource
	prompts     []mcp.Prompt
	pingErr     error
	closed      bool
}

func (m *mockMCPClient) Initialize(ctx context.Context) error {
	if m.initialized {
		return errors.New("already initialized")
	}
	m.initialized = true
	return nil
}

func (m *mockMCPClient) Close() error {
	if m.closed {
		return errors.New("already closed")
	}
	m.closed = true
	return nil
}

func (m *mockMCPClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	if !m.initialized {
		return nil, errors.New("not initialized")
	}
	return m.tools, nil
}

func (m *mockMCPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if !m.initialized {
		return nil, errors.New("not initialized")
	}
	// Find the tool
	for _, tool := range m.tools {
		if tool.Name == name {
			// Return a minimal result - the actual structure will be filled by the mcp-go library
			return &mcp.CallToolResult{}, nil
		}
	}
	return nil, errors.New("tool not found")
}

func (m *mockMCPClient) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	if !m.initialized {
		return nil, errors.New("not initialized")
	}
	return m.resources, nil
}

func (m *mockMCPClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	if !m.initialized {
		return nil, errors.New("not initialized")
	}
	// Find the resource
	for _, res := range m.resources {
		if res.URI == uri {
			// Return a minimal result
			return &mcp.ReadResourceResult{
				Contents: []mcp.ResourceContents{},
			}, nil
		}
	}
	return nil, errors.New("resource not found")
}

func (m *mockMCPClient) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	if !m.initialized {
		return nil, errors.New("not initialized")
	}
	return m.prompts, nil
}

func (m *mockMCPClient) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	if !m.initialized {
		return nil, errors.New("not initialized")
	}
	// Find the prompt
	for _, prompt := range m.prompts {
		if prompt.Name == name {
			// Return a minimal result
			return &mcp.GetPromptResult{}, nil
		}
	}
	return nil, errors.New("prompt not found")
}

func (m *mockMCPClient) Ping(ctx context.Context) error {
	if !m.initialized {
		return errors.New("not initialized")
	}
	return m.pingErr
}

func TestAggregatorServer_HandlerTracking(t *testing.T) {
	ctx := context.Background()
	config := AggregatorConfig{
		Host: "localhost",
		Port: 0, // Use any available port
	}

	server := NewAggregatorServer(config)
	require.NotNil(t, server)

	// Start the server
	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop(ctx)

	// Create mock clients with tools
	client1 := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "tool1", Description: "Tool 1"},
			{Name: "shared-tool", Description: "Shared tool"},
		},
		resources: []mcp.Resource{
			{URI: "resource1", Name: "Resource 1"},
		},
		prompts: []mcp.Prompt{
			{Name: "prompt1", Description: "Prompt 1"},
		},
	}

	client2 := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "tool2", Description: "Tool 2"},
			{Name: "shared-tool", Description: "Shared tool from server2"},
		},
	}

	// Register multiple servers
	require.NoError(t, server.RegisterServer(ctx, "server1", client1, ""))
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, server.RegisterServer(ctx, "server2", client2, ""))

	// Give the registry update a moment to process
	// This is needed because updateCapabilities runs in a goroutine
	time.Sleep(50 * time.Millisecond)

	// Verify tools are available
	tools := server.GetTools()
	assert.Len(t, tools, 4) // tool1, tool2, and shared-tool from each server (all prefixed)

	// Verify tools are tracked by checking they exist in GetTools()
	toolMap := make(map[string]bool)
	for _, tool := range tools {
		toolMap[tool.Name] = true
	}
	assert.True(t, toolMap["x_server1_tool1"])
	assert.True(t, toolMap["x_server2_tool2"])
	assert.True(t, toolMap["x_server1_shared-tool"])
	assert.True(t, toolMap["x_server2_shared-tool"])

	// Deregister server1
	err = server.DeregisterServer("server1")
	assert.NoError(t, err)

	// Give the registry update a moment to process
	time.Sleep(50 * time.Millisecond)

	// Get updated tools after deregistration
	tools = server.GetTools()
	assert.Len(t, tools, 2) // tool2 and shared-tool from server2 (still prefixed)

	// Verify tools from server1 are no longer available
	toolMap2 := make(map[string]bool)
	for _, tool := range tools {
		toolMap2[tool.Name] = true
	}
	assert.False(t, toolMap2["x_server1_tool1"])
	assert.False(t, toolMap2["x_server1_shared-tool"])
	assert.True(t, toolMap2["x_server2_tool2"])
	// After deregistering server1, shared-tool from server2 is still prefixed
	assert.True(t, toolMap2["x_server2_shared-tool"])
}

func TestAggregatorServer_InitialRegistration(t *testing.T) {
	ctx := context.Background()
	config := AggregatorConfig{
		Host: "localhost",
		Port: 0,
	}

	server := NewAggregatorServer(config)
	require.NotNil(t, server)

	// Create a mock client with tools before starting the server
	client := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "test-tool", Description: "Test tool"},
		},
	}

	// Start the server
	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop(ctx)

	// Register another server
	require.NoError(t, server.RegisterServer(ctx, "test-server", client, ""))

	// Wait for the asynchronous update to complete
	time.Sleep(50 * time.Millisecond)

	// The tools should be available
	tools := server.GetTools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "x_test-server_test-tool", tools[0].Name)

	// Verify the tool is available via the public API
	found := false
	for _, tool := range tools {
		if tool.Name == "x_test-server_test-tool" {
			found = true
			break
		}
	}
	assert.True(t, found, "x_test-server_test-tool should be available")
}

func TestAggregatorServer_EmptyStart(t *testing.T) {
	ctx := context.Background()
	config := AggregatorConfig{
		Host: "localhost",
		Port: 0,
	}

	server := NewAggregatorServer(config)
	require.NotNil(t, server)

	// Start the server with no registered servers
	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop(ctx)

	// Should have no tools initially
	tools := server.GetTools()
	assert.Len(t, tools, 0)

	// Verify no tools are available
	assert.Empty(t, tools, "Should have no tools when starting empty")

	// Now register a server
	client := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "late-tool", Description: "Tool added after start"},
		},
	}

	err = server.RegisterServer(ctx, "late-server", client, "")
	assert.NoError(t, err)

	// Tool should now be available
	tools = server.GetTools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "x_late-server_late-tool", tools[0].Name)
}

func TestAggregatorServer_HandlerExecution(t *testing.T) {
	ctx := context.Background()
	config := AggregatorConfig{
		Host: "localhost",
		Port: 0,
	}

	server := NewAggregatorServer(config)
	require.NotNil(t, server)

	// Start the server
	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop(ctx)

	// Create and register a mock client
	client := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "exec-tool", Description: "Tool for execution test"},
		},
	}

	err = server.RegisterServer(ctx, "exec-server", client, "")
	assert.NoError(t, err)

	// Wait for the asynchronous update to complete
	time.Sleep(50 * time.Millisecond)

	// Get the tool handler (we can't directly test it, but we can verify it's set up)
	tools := server.GetTools()
	require.Len(t, tools, 1)
	assert.Equal(t, "x_exec-server_exec-tool", tools[0].Name)

	// Verify the tool is available
	found := false
	for _, tool := range tools {
		if tool.Name == "x_exec-server_exec-tool" {
			found = true
			break
		}
	}
	assert.True(t, found, "x_exec-server_exec-tool should be available")

	// Deregister the server
	err = server.DeregisterServer("exec-server")
	assert.NoError(t, err)

	// Wait for the asynchronous update to complete
	time.Sleep(50 * time.Millisecond)

	// Tool should no longer be available
	tools = server.GetTools()
	assert.Empty(t, tools, "No tools should be available after deregistration")
}

func TestAggregatorServer_ToolsRemovedOnServerStop(t *testing.T) {
	// This test specifically verifies that tools are removed when an MCP server stops
	ctx := context.Background()
	config := AggregatorConfig{
		Host: "localhost",
		Port: 0,
	}

	server := NewAggregatorServer(config)
	require.NotNil(t, server)

	// Start the server
	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop(ctx)

	// Create and register two MCP servers
	client1 := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "tool1", Description: "Tool from server 1"},
			{Name: "tool2", Description: "Another tool from server 1"},
		},
	}

	client2 := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "tool3", Description: "Tool from server 2"},
		},
	}

	// Register servers with clients that return errors
	require.NoError(t, server.RegisterServer(ctx, "server1", client1, ""))
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, server.RegisterServer(ctx, "server2", client2, ""))

	// Wait for updates
	time.Sleep(50 * time.Millisecond)

	// Verify all tools are available
	tools := server.GetTools()
	assert.Len(t, tools, 3, "Should have 3 tools total")

	// Find tool names
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}
	assert.True(t, toolNames["x_server1_tool1"])
	assert.True(t, toolNames["x_server1_tool2"])
	assert.True(t, toolNames["x_server2_tool3"])

	// Now stop server1 by deregistering it
	err = server.DeregisterServer("server1")
	assert.NoError(t, err)

	// Wait for updates
	time.Sleep(50 * time.Millisecond)

	// Verify only server2's tools remain
	tools = server.GetTools()
	assert.Len(t, tools, 1, "Should have only 1 tool after server1 is removed")
	assert.Equal(t, "x_server2_tool3", tools[0].Name)

	// Verify server1's tools are no longer available via the public API
	toolNames = make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}
	assert.False(t, toolNames["x_server1_tool1"], "tool1 should not be available")
	assert.False(t, toolNames["x_server1_tool2"], "tool2 should not be available")
	assert.True(t, toolNames["x_server2_tool3"], "tool3 should still be available")
}

func TestAggregatorServer_DynamicToolManagement(t *testing.T) {
	// This test verifies that tools are dynamically added/removed without server restart
	ctx := context.Background()
	config := AggregatorConfig{
		Host: "localhost",
		Port: 0, // Use any available port
	}

	server := NewAggregatorServer(config)
	require.NotNil(t, server)

	// Start the server
	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop(ctx)

	// Capture the server instances - they should NOT change
	server.mu.RLock()
	originalMCPServer := server.server
	originalSSEServer := server.sseServer
	server.mu.RUnlock()

	// Create and register a mock client
	client1 := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "tool1", Description: "Tool 1"},
			{Name: "tool2", Description: "Tool 2"},
		},
	}

	err = server.RegisterServer(ctx, "server1", client1, "")
	assert.NoError(t, err)

	// Wait for the update to complete
	time.Sleep(50 * time.Millisecond)

	// Verify that the server instances have NOT changed
	server.mu.RLock()
	assert.Equal(t, originalMCPServer, server.server, "MCP server instance should remain the same")
	assert.Equal(t, originalSSEServer, server.sseServer, "SSE server instance should remain the same")
	server.mu.RUnlock()

	// Verify tools are available
	tools := server.GetTools()
	assert.Len(t, tools, 2)

	// Create another client and register it
	client2 := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "tool3", Description: "Tool 3"},
		},
	}

	err = server.RegisterServer(ctx, "server2", client2, "")
	assert.NoError(t, err)

	// Wait for the update to complete
	time.Sleep(50 * time.Millisecond)

	// Verify server instances still haven't changed
	server.mu.RLock()
	assert.Equal(t, originalMCPServer, server.server, "MCP server instance should remain the same")
	assert.Equal(t, originalSSEServer, server.sseServer, "SSE server instance should remain the same")
	server.mu.RUnlock()

	// Verify all tools are available
	tools = server.GetTools()
	assert.Len(t, tools, 3)

	// Now deregister server1
	err = server.DeregisterServer("server1")
	assert.NoError(t, err)

	// Wait for the update to complete
	time.Sleep(50 * time.Millisecond)

	// Verify server instances still haven't changed
	server.mu.RLock()
	assert.Equal(t, originalMCPServer, server.server, "MCP server instance should remain the same")
	assert.Equal(t, originalSSEServer, server.sseServer, "SSE server instance should remain the same")
	server.mu.RUnlock()

	// Verify only server2's tools remain
	tools = server.GetTools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "x_server2_tool3", tools[0].Name)

	// Verify correct tools are available via the public API
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}
	assert.Len(t, tools, 1, "Should only have 1 tool available")
	assert.True(t, toolNames["x_server2_tool3"], "tool3 should be available")
	assert.False(t, toolNames["x_server1_tool1"], "tool1 should not be available")
	assert.False(t, toolNames["x_server1_tool2"], "tool2 should not be available")
}

func TestAggregatorServer_NoStaleHandlersAfterRestart(t *testing.T) {
	// This test verifies that after restart, old handlers are completely gone
	ctx := context.Background()
	config := AggregatorConfig{
		Host: "localhost",
		Port: 0,
	}

	server := NewAggregatorServer(config)
	require.NotNil(t, server)

	// Start the server
	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop(ctx)

	// Register server with conflicting tool names
	client1 := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "common-tool", Description: "Tool from server1"},
		},
	}

	err = server.RegisterServer(ctx, "server1", client1, "")
	assert.NoError(t, err)

	// Wait for registration
	time.Sleep(50 * time.Millisecond)

	// Verify tool is available with prefix
	tools := server.GetTools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "x_server1_common-tool", tools[0].Name)

	// Deregister server1
	err = server.DeregisterServer("server1")
	assert.NoError(t, err)

	// Wait for deregistration and restart
	time.Sleep(100 * time.Millisecond)

	// Register server2 with the same tool name
	client2 := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "common-tool", Description: "Tool from server2"},
		},
	}

	err = server.RegisterServer(ctx, "server2", client2, "")
	assert.NoError(t, err)

	// Wait for registration
	time.Sleep(50 * time.Millisecond)

	// Verify tool is available and is from server2
	tools = server.GetTools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "x_server2_common-tool", tools[0].Name)
	assert.Equal(t, "Tool from server2", tools[0].Description)

	// Verify only server2's tool is available (no stale handlers from server1)
	assert.Len(t, tools, 1, "Should have exactly 1 tool")
	assert.Equal(t, "x_server2_common-tool", tools[0].Name, "Tool should be named x_server2_common-tool")
}
