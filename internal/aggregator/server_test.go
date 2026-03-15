package aggregator

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/client/transport"
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

	server := NewAggregatorServer(config, nil)
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

	server := NewAggregatorServer(config, nil)
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

	server := NewAggregatorServer(config, nil)
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

	server := NewAggregatorServer(config, nil)
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

	server := NewAggregatorServer(config, nil)
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

	server := NewAggregatorServer(config, nil)
	require.NotNil(t, server)

	// Start the server
	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop(ctx)

	// Capture the server instances - they should NOT change
	server.mu.RLock()
	originalMCPServer := server.mcpServer
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
	assert.Equal(t, originalMCPServer, server.mcpServer, "MCP server instance should remain the same")
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
	assert.Equal(t, originalMCPServer, server.mcpServer, "MCP server instance should remain the same")
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
	assert.Equal(t, originalMCPServer, server.mcpServer, "MCP server instance should remain the same")
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

	server := NewAggregatorServer(config, nil)
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

// callToolMockClient is a mock MCP client that returns configurable results
// from CallTool. Used to test retry-on-401 logic.
type callToolMockClient struct {
	callToolResult *mcp.CallToolResult
	callToolErr    error
	callCount      int
}

func (m *callToolMockClient) Initialize(_ context.Context) error { return nil }
func (m *callToolMockClient) Close() error                       { return nil }
func (m *callToolMockClient) ListTools(_ context.Context) ([]mcp.Tool, error) {
	return nil, nil
}
func (m *callToolMockClient) CallTool(_ context.Context, _ string, _ map[string]interface{}) (*mcp.CallToolResult, error) {
	m.callCount++
	return m.callToolResult, m.callToolErr
}
func (m *callToolMockClient) ListResources(_ context.Context) ([]mcp.Resource, error) {
	return nil, nil
}
func (m *callToolMockClient) ReadResource(_ context.Context, _ string) (*mcp.ReadResourceResult, error) {
	return nil, nil
}
func (m *callToolMockClient) ListPrompts(_ context.Context) ([]mcp.Prompt, error) {
	return nil, nil
}
func (m *callToolMockClient) GetPrompt(_ context.Context, _ string, _ map[string]interface{}) (*mcp.GetPromptResult, error) {
	return nil, nil
}
func (m *callToolMockClient) Ping(_ context.Context) error { return nil }

// newTestAggregatorWithPool creates a minimal AggregatorServer for testing
// callToolWithTokenExchangeRetry. The server is NOT started; only the
// registry, auth store, capability store, and connection pool are wired.
// The connection pool's reaper goroutine is stopped automatically when
// the test finishes.
func newTestAggregatorWithPool(t *testing.T) *AggregatorServer {
	t.Helper()
	pool := NewSessionConnectionPool(DefaultCapabilityStoreTTL)
	t.Cleanup(pool.Stop)

	return &AggregatorServer{
		config:          AggregatorConfig{MusterPrefix: "x"},
		registry:        NewServerRegistry("x"),
		authStore:       NewInMemorySessionAuthStore(DefaultCapabilityStoreTTL),
		capabilityStore: NewInMemoryCapabilityStore(DefaultCapabilityStoreTTL),
		connPool:        pool,
	}
}

func TestCallToolWithTokenExchangeRetry_SuccessNoRetry(t *testing.T) {
	a := newTestAggregatorWithPool(t)
	ctx := context.Background()
	sessionID := "test-session"
	serverName := "exchange-server"

	tokenExchangeAuth := &api.MCPServerAuth{
		TokenExchange: &api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex.example.com/token",
			ConnectorID:      "ldap",
		},
	}
	err := a.registry.RegisterPendingAuthWithConfig(serverName, "https://server.example.com", "", nil, tokenExchangeAuth)
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &Capabilities{
		Tools: []mcp.Tool{{Name: "my-tool"}},
	})
	_ = a.authStore.MarkAuthenticated(ctx, sessionID, serverName)

	expectedResult := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent("ok")},
	}
	mockClient := &callToolMockClient{callToolResult: expectedResult}
	a.connPool.Put(sessionID, serverName, mockClient)

	result, err := a.callToolWithTokenExchangeRetry(ctx, serverName, "my-tool", nil, sessionID, "user-sub")

	require.NoError(t, err)
	assert.Equal(t, expectedResult, result)
	assert.Equal(t, 1, mockClient.callCount, "should call tool exactly once")
	assert.Equal(t, 1, a.connPool.Len(), "pool entry should remain")
}

func TestCallToolWithTokenExchangeRetry_EvictsPoolOn401ForTokenExchange(t *testing.T) {
	a := newTestAggregatorWithPool(t)
	ctx := context.Background()
	sessionID := "test-session"
	serverName := "exchange-server"

	tokenExchangeAuth := &api.MCPServerAuth{
		TokenExchange: &api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex.example.com/token",
			ConnectorID:      "ldap",
		},
	}
	err := a.registry.RegisterPendingAuthWithConfig(serverName, "https://server.example.com", "", nil, tokenExchangeAuth)
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &Capabilities{
		Tools: []mcp.Tool{{Name: "my-tool"}},
	})
	_ = a.authStore.MarkAuthenticated(ctx, sessionID, serverName)

	unauthorizedErr := fmt.Errorf("failed to call tool: %w", transport.ErrUnauthorized)
	mockClient := &callToolMockClient{callToolErr: unauthorizedErr}
	a.connPool.Put(sessionID, serverName, mockClient)

	_, err = a.callToolWithTokenExchangeRetry(ctx, serverName, "my-tool", nil, sessionID, "user-sub")

	// The retry attempt will fail (no OAuth handler for re-exchange),
	// but the pool entry must have been evicted before the retry.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retry after token re-exchange failed")
	assert.Equal(t, 0, a.connPool.Len(), "stale pool entry must be evicted")
	assert.Equal(t, 1, mockClient.callCount, "original client called once before eviction")
}

func TestCallToolWithTokenExchangeRetry_NoRetryForNonTokenExchange(t *testing.T) {
	a := newTestAggregatorWithPool(t)
	ctx := context.Background()
	sessionID := "test-session"
	serverName := "forward-server"

	forwardAuth := &api.MCPServerAuth{
		ForwardToken: true,
	}
	err := a.registry.RegisterPendingAuthWithConfig(serverName, "https://server.example.com", "", nil, forwardAuth)
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &Capabilities{
		Tools: []mcp.Tool{{Name: "my-tool"}},
	})
	_ = a.authStore.MarkAuthenticated(ctx, sessionID, serverName)

	unauthorizedErr := fmt.Errorf("failed to call tool: %w", transport.ErrUnauthorized)
	mockClient := &callToolMockClient{callToolErr: unauthorizedErr}
	a.connPool.Put(sessionID, serverName, mockClient)

	_, err = a.callToolWithTokenExchangeRetry(ctx, serverName, "my-tool", nil, sessionID, "user-sub")

	require.Error(t, err)
	assert.True(t, is401Error(err), "original 401 error should be returned as-is")
	assert.Equal(t, 1, a.connPool.Len(), "pool entry should NOT be evicted for non-token-exchange server")
}

func TestCallToolWithTokenExchangeRetry_NoRetryForNon401Error(t *testing.T) {
	a := newTestAggregatorWithPool(t)
	ctx := context.Background()
	sessionID := "test-session"
	serverName := "exchange-server"

	tokenExchangeAuth := &api.MCPServerAuth{
		TokenExchange: &api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex.example.com/token",
			ConnectorID:      "ldap",
		},
	}
	err := a.registry.RegisterPendingAuthWithConfig(serverName, "https://server.example.com", "", nil, tokenExchangeAuth)
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &Capabilities{
		Tools: []mcp.Tool{{Name: "my-tool"}},
	})
	_ = a.authStore.MarkAuthenticated(ctx, sessionID, serverName)

	nonAuthErr := errors.New("connection reset by peer")
	mockClient := &callToolMockClient{callToolErr: nonAuthErr}
	a.connPool.Put(sessionID, serverName, mockClient)

	_, err = a.callToolWithTokenExchangeRetry(ctx, serverName, "my-tool", nil, sessionID, "user-sub")

	require.Error(t, err)
	assert.Equal(t, nonAuthErr, err, "original error should be returned as-is")
	assert.Equal(t, 1, a.connPool.Len(), "pool entry should NOT be evicted for non-401 error")
}

func TestGetOrCreateClientForToolCall_ExpiringSoonReturnsClientAndTriggersBackgroundRefresh(t *testing.T) {
	a := newTestAggregatorWithPool(t)
	ctx := context.Background()
	sessionID := "test-session"
	serverName := "exchange-server"

	tokenExchangeAuth := &api.MCPServerAuth{
		TokenExchange: &api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex.example.com/token",
			ConnectorID:      "ldap",
		},
	}
	err := a.registry.RegisterPendingAuthWithConfig(serverName, "https://server.example.com", "", nil, tokenExchangeAuth)
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &Capabilities{
		Tools: []mcp.Tool{{Name: "my-tool"}},
	})
	_ = a.authStore.MarkAuthenticated(ctx, sessionID, serverName)

	// Pool a client with a token that expires in 2 minutes (within the 5-minute margin).
	expectedResult := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent("ok")},
	}
	mockClient := &callToolMockClient{callToolResult: expectedResult}
	a.connPool.PutWithExpiry(sessionID, serverName, mockClient, time.Now().Add(2*time.Minute))

	// getOrCreateClientForToolCall should return the still-valid pooled client
	// immediately and trigger background refresh (which will fail silently
	// because there's no OAuth handler, but that's fine for this test).
	client, cleanup, clientErr := a.getOrCreateClientForToolCall(ctx, serverName, sessionID, "user-sub")
	require.NoError(t, clientErr)
	defer cleanup()

	assert.Equal(t, mockClient, client, "should return the still-valid pooled client")
	assert.Equal(t, 1, a.connPool.Len(), "pool entry should remain (background refresh is async)")
}

func TestGetOrCreateClientForToolCall_ExpiredTokenEvictsSynchronously(t *testing.T) {
	a := newTestAggregatorWithPool(t)
	ctx := context.Background()
	sessionID := "test-session"
	serverName := "exchange-server"

	tokenExchangeAuth := &api.MCPServerAuth{
		TokenExchange: &api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex.example.com/token",
			ConnectorID:      "ldap",
		},
	}
	err := a.registry.RegisterPendingAuthWithConfig(serverName, "https://server.example.com", "", nil, tokenExchangeAuth)
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &Capabilities{
		Tools: []mcp.Tool{{Name: "my-tool"}},
	})
	_ = a.authStore.MarkAuthenticated(ctx, sessionID, serverName)

	// Pool a client with a token that already expired 10 seconds ago.
	mockClient := &callToolMockClient{callToolResult: &mcp.CallToolResult{}}
	a.connPool.PutWithExpiry(sessionID, serverName, mockClient, time.Now().Add(-10*time.Second))

	// getOrCreateClientForToolCall should evict the expired entry and try to
	// create a fresh client synchronously (which fails -- no OAuth handler).
	_, _, clientErr := a.getOrCreateClientForToolCall(ctx, serverName, sessionID, "user-sub")

	require.Error(t, clientErr, "should fail because no OAuth handler for re-exchange")
	assert.Equal(t, 0, a.connPool.Len(), "expired pool entry must be evicted synchronously")
}

func TestGetOrCreateClientForToolCall_NoEvictionWhenTokenFresh(t *testing.T) {
	a := newTestAggregatorWithPool(t)
	ctx := context.Background()
	sessionID := "test-session"
	serverName := "exchange-server"

	tokenExchangeAuth := &api.MCPServerAuth{
		TokenExchange: &api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex.example.com/token",
			ConnectorID:      "ldap",
		},
	}
	err := a.registry.RegisterPendingAuthWithConfig(serverName, "https://server.example.com", "", nil, tokenExchangeAuth)
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &Capabilities{
		Tools: []mcp.Tool{{Name: "my-tool"}},
	})
	_ = a.authStore.MarkAuthenticated(ctx, sessionID, serverName)

	// Pool a client with a token that expires in 10 minutes (well within safe range).
	expectedResult := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent("ok")},
	}
	mockClient := &callToolMockClient{callToolResult: expectedResult}
	a.connPool.PutWithExpiry(sessionID, serverName, mockClient, time.Now().Add(10*time.Minute))

	client, cleanup, clientErr := a.getOrCreateClientForToolCall(ctx, serverName, sessionID, "user-sub")
	require.NoError(t, clientErr)
	defer cleanup()

	assert.Equal(t, mockClient, client, "should return the pooled client without eviction")
	assert.Equal(t, 1, a.connPool.Len(), "pool entry should remain")
}
