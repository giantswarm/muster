package aggregator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"
	oauthstore "github.com/giantswarm/muster/internal/oauth/store"

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

func (m *mockMCPClient) OnNotification(func(mcp.JSONRPCNotification)) {}

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
	defer func() { _ = server.Stop(ctx) }()

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
	require.NoError(t, server.RegisterServer(ctx, ServerRegistration{Name: "server1"}, client1))
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, server.RegisterServer(ctx, ServerRegistration{Name: "server2"}, client2))

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
	defer func() { _ = server.Stop(ctx) }()

	// Register another server
	require.NoError(t, server.RegisterServer(ctx, ServerRegistration{Name: "test-server"}, client))

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
	defer func() { _ = server.Stop(ctx) }()

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

	err = server.RegisterServer(ctx, ServerRegistration{Name: "late-server"}, client)
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
	defer func() { _ = server.Stop(ctx) }()

	// Create and register a mock client
	client := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "exec-tool", Description: "Tool for execution test"},
		},
	}

	err = server.RegisterServer(ctx, ServerRegistration{Name: "exec-server"}, client)
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
	defer func() { _ = server.Stop(ctx) }()

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
	require.NoError(t, server.RegisterServer(ctx, ServerRegistration{Name: "server1"}, client1))
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, server.RegisterServer(ctx, ServerRegistration{Name: "server2"}, client2))

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
	defer func() { _ = server.Stop(ctx) }()

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

	err = server.RegisterServer(ctx, ServerRegistration{Name: "server1"}, client1)
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

	err = server.RegisterServer(ctx, ServerRegistration{Name: "server2"}, client2)
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
	defer func() { _ = server.Stop(ctx) }()

	// Register server with conflicting tool names
	client1 := &mockMCPClient{
		tools: []mcp.Tool{
			{Name: "common-tool", Description: "Tool from server1"},
		},
	}

	err = server.RegisterServer(ctx, ServerRegistration{Name: "server1"}, client1)
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

	err = server.RegisterServer(ctx, ServerRegistration{Name: "server2"}, client2)
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
func (m *callToolMockClient) Ping(_ context.Context) error                 { return nil }
func (m *callToolMockClient) OnNotification(func(mcp.JSONRPCNotification)) {}

// newTestAggregatorWithPool creates a minimal AggregatorServer for testing
// callToolWithTokenExchangeRetry. The server is NOT started; only the
// registry, auth store, capability store, and connection pool are wired.
// The connection pool's reaper goroutine is stopped automatically when
// the test finishes.
func newTestAggregatorWithPool(t *testing.T) *AggregatorServer {
	t.Helper()
	pool := NewSessionConnectionPool(oauthstore.DefaultCapabilityStoreTTL)
	t.Cleanup(pool.Stop)

	return &AggregatorServer{
		config:          AggregatorConfig{MusterPrefix: "x"},
		registry:        NewServerRegistry("x"),
		authStore:       oauthstore.NewInMemorySessionAuthStore(oauthstore.DefaultCapabilityStoreTTL),
		capabilityStore: oauthstore.NewInMemoryCapabilityStore(oauthstore.DefaultCapabilityStoreTTL),
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
	err := a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: serverName, ToolPrefix: ""},
		URL:                "https://server.example.com",
		AuthInfo:           nil,
		AuthConfig:         tokenExchangeAuth,
	})
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &oauthstore.Capabilities{
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
	err := a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: serverName, ToolPrefix: ""},
		URL:                "https://server.example.com",
		AuthInfo:           nil,
		AuthConfig:         tokenExchangeAuth,
	})
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &oauthstore.Capabilities{
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
	err := a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: serverName, ToolPrefix: ""},
		URL:                "https://server.example.com",
		AuthInfo:           nil,
		AuthConfig:         forwardAuth,
	})
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &oauthstore.Capabilities{
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
	err := a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: serverName, ToolPrefix: ""},
		URL:                "https://server.example.com",
		AuthInfo:           nil,
		AuthConfig:         tokenExchangeAuth,
	})
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &oauthstore.Capabilities{
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
	err := a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: serverName, ToolPrefix: ""},
		URL:                "https://server.example.com",
		AuthInfo:           nil,
		AuthConfig:         tokenExchangeAuth,
	})
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &oauthstore.Capabilities{
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
	err := a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: serverName, ToolPrefix: ""},
		URL:                "https://server.example.com",
		AuthInfo:           nil,
		AuthConfig:         tokenExchangeAuth,
	})
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &oauthstore.Capabilities{
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
	err := a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: serverName, ToolPrefix: ""},
		URL:                "https://server.example.com",
		AuthInfo:           nil,
		AuthConfig:         tokenExchangeAuth,
	})
	require.NoError(t, err)

	_ = a.capabilityStore.Set(ctx, sessionID, serverName, &oauthstore.Capabilities{
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

// TestDiscoverProtectedResourceMetadata_Override locks in the contract that
// the override MUST skip PRM probing and MUST verify the operator-pinned
// issuer matches the AS metadata's `issuer` field per RFC 8414 §3.3.
func TestDiscoverProtectedResourceMetadata_Override(t *testing.T) {
	// Stub authorization server: serves /.well-known/oauth-authorization-server
	// with a configurable advertised `issuer` value so tests can exercise the
	// match and mismatch branches independently.
	var advertisedIssuer string
	asMux := http.NewServeMux()
	asMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"code_challenge_methods_supported":["S256"]}`,
			advertisedIssuer, advertisedIssuer+"/authorize", advertisedIssuer+"/token")
	})
	asServer := httptest.NewServer(asMux)
	defer asServer.Close()

	t.Run("override returns synthetic PRM when issuer matches AS metadata", func(t *testing.T) {
		advertisedIssuer = asServer.URL
		override := &api.MCPServerAuthAuthorizationServer{
			Issuer: asServer.URL,
			Scopes: "openid offline_access",
		}
		md, err := discoverProtectedResourceMetadata(context.Background(), "https://mcp.example.com/v1/mcp", override)
		require.NoError(t, err)
		assert.Equal(t, asServer.URL, md.Issuer)
		assert.Equal(t, "openid offline_access", md.Scope)
	})

	t.Run("override rejects when AS metadata reports a different issuer", func(t *testing.T) {
		advertisedIssuer = "https://attacker.example.com"
		override := &api.MCPServerAuthAuthorizationServer{
			Issuer: asServer.URL,
		}
		_, err := discoverProtectedResourceMetadata(context.Background(), "https://mcp.example.com/v1/mcp", override)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "issuer mismatch")
	})

	t.Run("override propagates AS metadata fetch errors", func(t *testing.T) {
		override := &api.MCPServerAuthAuthorizationServer{
			Issuer: "https://black-hole.invalid",
		}
		_, err := discoverProtectedResourceMetadata(context.Background(), "https://mcp.example.com/v1/mcp", override)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "authorizationServer override")
	})
}

// TestDiscoverProtectedResourceMetadata exercises the spec-compliance fallback
// path: WWW-Authenticate resource_metadata= follow, path-based PRM well-known
// (using the raw URL path including /v1/mcp), and RFC 9728 `resource` field
// parsing. These run when no override is set (the second argument is nil).
func TestDiscoverProtectedResourceMetadata(t *testing.T) {
	prmBody := func(issuer, resource string) string {
		return fmt.Sprintf(`{"resource":%q,"authorization_servers":[%q],"scopes_supported":["openid","offline_access"]}`,
			resource, issuer)
	}

	t.Run("WWW-Authenticate resource_metadata= followed", func(t *testing.T) {
		prmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, prmBody("https://issuer.example", "https://mcp.example/v1/mcp"))
		}))
		defer prmServer.Close()
		mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, prmServer.URL))
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer mcp.Close()

		md, err := discoverProtectedResourceMetadata(context.Background(), mcp.URL+"/v1/mcp", nil)
		require.NoError(t, err)
		assert.Equal(t, "https://issuer.example", md.Issuer)
		assert.Equal(t, "openid offline_access", md.Scope)
		assert.Equal(t, "https://mcp.example/v1/mcp", md.Resource)
	})

	t.Run("path-based PRM well-known retains MCP URL path", func(t *testing.T) {
		var seenPaths []string
		mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenPaths = append(seenPaths, r.URL.Path)
			if r.URL.Path == "/.well-known/oauth-protected-resource/v1/mcp" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, prmBody("https://issuer.example", "https://mcp.example/v1/mcp"))
				return
			}
			http.NotFound(w, r)
		}))
		defer mcp.Close()

		md, err := discoverProtectedResourceMetadata(context.Background(), mcp.URL+"/v1/mcp", nil)
		require.NoError(t, err)
		assert.Equal(t, "https://issuer.example", md.Issuer)
		assert.Contains(t, seenPaths, "/.well-known/oauth-protected-resource/v1/mcp",
			"path-based well-known must use the raw MCP URL path")
	})

	t.Run("trailing slash on MCP URL path is stripped before composing well-known", func(t *testing.T) {
		var seenPaths []string
		mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenPaths = append(seenPaths, r.URL.Path)
			if r.URL.Path == "/.well-known/oauth-protected-resource/v1/mcp" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, prmBody("https://issuer.example", "https://mcp.example/v1/mcp"))
				return
			}
			http.NotFound(w, r)
		}))
		defer mcp.Close()

		_, err := discoverProtectedResourceMetadata(context.Background(), mcp.URL+"/v1/mcp/", nil)
		require.NoError(t, err)
		for _, p := range seenPaths {
			assert.NotEqual(t, "/.well-known/oauth-protected-resource/v1/mcp/", p,
				"trailing slash must be stripped from path before composing well-known")
		}
	})

	t.Run("falls back to root well-known when path-based 404s", func(t *testing.T) {
		mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-protected-resource" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, prmBody("https://issuer.example", "https://mcp.example"))
				return
			}
			http.NotFound(w, r)
		}))
		defer mcp.Close()

		md, err := discoverProtectedResourceMetadata(context.Background(), mcp.URL+"/v1/mcp", nil)
		require.NoError(t, err)
		assert.Equal(t, "https://issuer.example", md.Issuer)
	})

	t.Run("logs but accepts PRM with empty `resource` field", func(t *testing.T) {
		mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/.well-known/oauth-protected-resource") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"authorization_servers":["https://issuer.example"]}`)
				return
			}
			http.NotFound(w, r)
		}))
		defer mcp.Close()

		md, err := discoverProtectedResourceMetadata(context.Background(), mcp.URL+"/v1/mcp", nil)
		require.NoError(t, err)
		assert.Equal(t, "", md.Resource, "missing resource field accepted")
	})

	t.Run("returns error when no PRM source resolves", func(t *testing.T) {
		mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		defer mcp.Close()

		_, err := discoverProtectedResourceMetadata(context.Background(), mcp.URL+"/v1/mcp", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "authorizationServer.issuer")
	})
}

// recordingMCPClient embeds mockMCPClient and records the last CallTool
// invocation so tests can assert on the forwarded tool name and args.
type recordingMCPClient struct {
	mockMCPClient
	lastName string
	lastArgs map[string]interface{}
}

func (r *recordingMCPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	r.lastName = name
	r.lastArgs = args
	return r.mockMCPClient.CallTool(ctx, name, args)
}

func TestAggregatorServer_CallToolInternal_FamilyRouting(t *testing.T) {
	ctx := context.Background()

	makeServer := func(t *testing.T) *AggregatorServer {
		t.Helper()
		return NewAggregatorServer(AggregatorConfig{Host: "localhost", Port: 0}, nil)
	}

	makeFamily := func(t *testing.T, server *AggregatorServer) (*recordingMCPClient, *recordingMCPClient) {
		t.Helper()
		clientA := &recordingMCPClient{
			mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: "list_pods", Description: "List pods"}}},
		}
		clientB := &recordingMCPClient{
			mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: "list_pods", Description: "List pods"}}},
		}
		require.NoError(t, server.RegisterServer(ctx, ServerRegistration{
			Name:   "mcp-k8s-graveler",
			Family: &api.MCPServerFamily{Name: "kubernetes", InstanceArg: "management_cluster"},
		}, clientA))
		require.NoError(t, server.RegisterServer(ctx, ServerRegistration{
			Name:   "mcp-k8s-gazelle",
			Family: &api.MCPServerFamily{Name: "kubernetes", InstanceArg: "management_cluster"},
		}, clientB))
		_ = server.registry.GetAllTools() // prime familyMappings
		return clientA, clientB
	}

	t.Run("explicit instance arg routes to matching backend and strips it from forwarded args", func(t *testing.T) {
		server := makeServer(t)
		clientA, clientB := makeFamily(t, server)

		_, err := server.CallToolInternal(ctx, "x_kubernetes_list_pods", map[string]interface{}{
			"management_cluster": "mcp-k8s-graveler",
			"namespace":          "default",
		})
		require.NoError(t, err)
		assert.Equal(t, "list_pods", clientA.lastName)
		assert.Equal(t, map[string]interface{}{"namespace": "default"}, clientA.lastArgs,
			"forwarded args must not include the routing instance arg")
		assert.Empty(t, clientB.lastName, "non-target server must not be called")
	})

	t.Run("invalid instance arg surfaces resolution error without legacy fallback", func(t *testing.T) {
		server := makeServer(t)
		makeFamily(t, server)

		_, err := server.CallToolInternal(ctx, "x_kubernetes_list_pods", map[string]interface{}{
			"management_cluster": "mcp-k8s-typo",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not available on server")
	})

	t.Run("missing instance arg returns parameter-required error for multi-instance families", func(t *testing.T) {
		server := makeServer(t)
		makeFamily(t, server)

		_, err := server.CallToolInternal(ctx, "x_kubernetes_list_pods", map[string]interface{}{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"management_cluster" parameter is required`)
	})
}

// TestAggregatorServer_IsToolAvailable_FamilyTools is the regression guard for
// #761: a family-grouped tool backed by more than one provider must still be
// reported as available even though ResolveToolName intentionally returns an
// ambiguity error for it (the instance-selector arg is required only to route
// a call, not to prove existence).
func TestAggregatorServer_IsToolAvailable_FamilyTools(t *testing.T) {
	ctx := context.Background()

	t.Run("family tool with two providers is available despite ambiguity error", func(t *testing.T) {
		reg := NewServerRegistry("x")
		require.NoError(t, reg.Register(ctx, ServerRegistration{
			Name:   "mc1-mcp-kubernetes",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{{Name: "list", Description: "List"}}}))
		require.NoError(t, reg.Register(ctx, ServerRegistration{
			Name:   "mc2-mcp-kubernetes",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{{Name: "list", Description: "List"}}}))
		_ = reg.GetAllTools() // prime familyMappings

		// Sanity: the call-time resolver is still ambiguous without a selector.
		_, _, err := reg.ResolveToolName("x_kubernetes_list")
		require.Error(t, err)

		a := &AggregatorServer{registry: reg}
		assert.True(t, a.IsToolAvailable("x_kubernetes_list"),
			"a family tool with >= 1 provider must be reported available")
	})

	t.Run("family tool with a single provider is available", func(t *testing.T) {
		reg := NewServerRegistry("x")
		require.NoError(t, reg.Register(ctx, ServerRegistration{
			Name:   "mc1-mcp-kubernetes",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{{Name: "list", Description: "List"}}}))
		_ = reg.GetAllTools()

		a := &AggregatorServer{registry: reg}
		assert.True(t, a.IsToolAvailable("x_kubernetes_list"))
	})

	t.Run("genuinely absent tool is not available", func(t *testing.T) {
		reg := NewServerRegistry("x")
		require.NoError(t, reg.Register(ctx, ServerRegistration{
			Name:   "mc1-mcp-kubernetes",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{{Name: "list", Description: "List"}}}))
		_ = reg.GetAllTools()

		a := &AggregatorServer{registry: reg}
		assert.False(t, a.IsToolAvailable("x_kubernetes_does_not_exist"))
		assert.False(t, a.IsToolAvailable("x_nonexistent_tool"))
	})
}

// registerAuthFamilyMember registers an SSO / auth-protected family member.
// Such servers are skipped by GetAllTools(), so their family tools never enter
// the process-global routing index through the global path.
func registerAuthFamilyMember(t *testing.T, reg *ServerRegistry, name, familyName, instanceArg string) {
	t.Helper()
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{
			Name:   name,
			Family: family(familyName, instanceArg),
		},
		URL:      "https://" + name + ".example.com",
		AuthInfo: &AuthInfo{Issuer: "https://idp.example.com", Scope: "openid"},
	}))
}

// TestAggregatorServer_MissingToolsForSession is the regression guard for
// #764: availability of SSO / auth-protected family tools must be session-aware
// and must not depend on whether the session called list_tools first.
//
// Two auth-protected servers share family "kubernetes", so the bare family tool
// is x_kubernetes_list. GetAllTools() skips auth-protected servers, so the
// process-global familyMappings index has no provider for x_kubernetes_list
// until some session lists tools. With the caller's capabilities present in the
// CapabilityStore, MissingToolsForSession must report the tool available even
// though the non-session-aware IsToolAvailable still reports it missing.
func TestAggregatorServer_MissingToolsForSession(t *testing.T) {
	const sessionID = "session-764"

	// available reports whether the single tool is available for the session.
	available := func(a *AggregatorServer, ctx context.Context, toolName string) bool {
		return len(a.MissingToolsForSession(ctx, []string{toolName})) == 0
	}

	newAggWithSSOFamily := func(t *testing.T) (*AggregatorServer, func()) {
		t.Helper()
		reg := NewServerRegistry("x")
		registerAuthFamilyMember(t, reg, "mc1-mcp-kubernetes", "kubernetes", "management_cluster")
		registerAuthFamilyMember(t, reg, "mc2-mcp-kubernetes", "kubernetes", "management_cluster")

		store := oauthstore.NewInMemoryCapabilityStore(30 * time.Minute)
		ctx := context.Background()
		require.NoError(t, store.Set(ctx, sessionID, "mc1-mcp-kubernetes",
			&oauthstore.Capabilities{Tools: []mcp.Tool{{Name: "list", Description: "List resources"}}}))
		require.NoError(t, store.Set(ctx, sessionID, "mc2-mcp-kubernetes",
			&oauthstore.Capabilities{Tools: []mcp.Tool{{Name: "list", Description: "List resources"}}}))

		a := &AggregatorServer{registry: reg, capabilityStore: store}
		return a, store.Stop
	}

	t.Run("session with caps but no prior list_tools sees the family tool", func(t *testing.T) {
		a, stop := newAggWithSSOFamily(t)
		defer stop()

		// Pre-condition (the #764 bug): without a prior list_tools the global
		// index has no provider for the auth family, so the non-session-aware
		// check reports the tool missing.
		require.False(t, a.IsToolAvailable("x_kubernetes_list"),
			"precondition: global check must miss the SSO family tool before any list_tools")

		ctx := api.WithSessionID(context.Background(), sessionID)
		assert.True(t, available(a, ctx, "x_kubernetes_list"),
			"session-aware check must report the SSO family tool available from the CapabilityStore")
	})

	t.Run("genuinely inaccessible tool stays unavailable for the session", func(t *testing.T) {
		a, stop := newAggWithSSOFamily(t)
		defer stop()

		ctx := api.WithSessionID(context.Background(), sessionID)
		assert.False(t, available(a, ctx, "x_this_tool_does_not_exist"),
			"a tool with no provider in the session must stay unavailable")
	})

	t.Run("no session context falls back to the global view", func(t *testing.T) {
		a, stop := newAggWithSSOFamily(t)
		defer stop()

		assert.False(t, available(a, context.Background(), "x_kubernetes_list"),
			"without a session the auth family tool is not globally seeded and must report unavailable")
	})

	t.Run("core tools are available regardless of session", func(t *testing.T) {
		a, stop := newAggWithSSOFamily(t)
		defer stop()

		assert.True(t, available(a, context.Background(), "core_workflow_list"),
			"core tools resolve via the global, order-independent check")
	})

	t.Run("mixed batch resolves the session set once and preserves order", func(t *testing.T) {
		a, stop := newAggWithSSOFamily(t)
		defer stop()

		ctx := api.WithSessionID(context.Background(), sessionID)
		missing := a.MissingToolsForSession(ctx, []string{
			"core_workflow_list",         // core, globally available
			"x_kubernetes_list",          // SSO family, available via session
			"x_this_tool_does_not_exist", // unavailable
			"x_this_tool_does_not_exist", // duplicate, must be collapsed
		})
		assert.Equal(t, []string{"x_this_tool_does_not_exist"}, missing,
			"only the genuinely missing tool is returned, deduplicated and in input order")
	})
}
