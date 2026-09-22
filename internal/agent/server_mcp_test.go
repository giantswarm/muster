package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	mcp_client "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockMCPGoClient is a mock implementation of the mcp-go MCPClient for testing
// the transport bridge pattern. It simulates server responses to meta-tool calls.
type MockMCPGoClient struct {
	mcp_client.MCPClient
	tools []mcp.Tool
	// callToolResponses maps tool names to their mock responses
	callToolResponses map[string]*mcp.CallToolResult
	// lastCallToolRequest captures the last CallTool request for verification
	lastCallToolRequest mcp.CallToolRequest
	// callToolError can be set to simulate errors
	callToolError error
	// callToolDelay makes CallTool answer only after this long, the way a slow
	// tool behind the aggregator does; the request's context can cut it short.
	callToolDelay time.Duration
	// lastCallToolDeadline captures the deadline of the last CallTool context.
	lastCallToolDeadline time.Time
}

func (m *MockMCPGoClient) ListTools(ctx context.Context, req mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return &mcp.ListToolsResult{
		Tools: m.tools,
	}, nil
}

func (m *MockMCPGoClient) Initialize(ctx context.Context, req mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	return &mcp.InitializeResult{}, nil
}

func (m *MockMCPGoClient) CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	m.lastCallToolRequest = req
	m.lastCallToolDeadline, _ = ctx.Deadline()

	if m.callToolError != nil {
		return nil, m.callToolError
	}

	if m.callToolDelay > 0 {
		select {
		case <-time.After(m.callToolDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	toolName := req.Params.Name
	if response, ok := m.callToolResponses[toolName]; ok {
		return response, nil
	}

	// Default response
	return mcp.NewToolResultText("default mock response"), nil
}

func (m *MockMCPGoClient) ListResources(ctx context.Context, req mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error) {
	return &mcp.ListResourcesResult{}, nil
}

func (m *MockMCPGoClient) ReadResource(ctx context.Context, req mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{}, nil
}

func (m *MockMCPGoClient) ListPrompts(ctx context.Context, req mcp.ListPromptsRequest) (*mcp.ListPromptsResult, error) {
	return &mcp.ListPromptsResult{}, nil
}

func (m *MockMCPGoClient) GetPrompt(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{}, nil
}

func (m *MockMCPGoClient) OnNotification(handler func(notification mcp.JSONRPCNotification)) {}
func (m *MockMCPGoClient) Close() error                                                      { return nil }

// TestForwardToServerMetaTool tests that the transport bridge correctly forwards
// meta-tool calls to the server and returns the results.
func TestForwardToServerMetaTool(t *testing.T) {
	// Create mock client with test responses
	mockMCPClient := &MockMCPGoClient{
		callToolResponses: map[string]*mcp.CallToolResult{
			"list_tools": mcp.NewToolResultText(`[{"name":"tool1","description":"Tool 1"},{"name":"tool2","description":"Tool 2"}]`),
		},
	}

	// Create agent client with mock MCP client
	client := &Client{
		client:     mockMCPClient,
		mu:         sync.RWMutex{},
		formatters: NewFormatters(),
	}

	// Create MCP server
	server := &MCPServer{
		client: client,
		logger: NewLogger(false, false, false),
	}

	// Test the forwarding handler
	handler := server.forwardToServerMetaTool("list_tools")
	result, err := handler(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)

	// Verify the call was forwarded to the server
	assert.Equal(t, "list_tools", mockMCPClient.lastCallToolRequest.Params.Name)

	// Verify result was returned
	assert.False(t, result.IsError)
	require.Len(t, result.Content, 1)

	textContent, ok := mcp.AsTextContent(result.Content[0])
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "tool1")
	assert.Contains(t, textContent.Text, "tool2")
}

// TestForwardToServerMetaToolWithArguments tests that arguments are correctly
// forwarded to the server meta-tool.
func TestForwardToServerMetaToolWithArguments(t *testing.T) {
	mockMCPClient := &MockMCPGoClient{
		callToolResponses: map[string]*mcp.CallToolResult{
			"describe_tool": mcp.NewToolResultText(`{"name":"test_tool","description":"Test tool"}`),
		},
	}

	client := &Client{
		client:     mockMCPClient,
		mu:         sync.RWMutex{},
		formatters: NewFormatters(),
	}

	server := &MCPServer{
		client: client,
		logger: NewLogger(false, false, false),
	}

	// Create request with arguments
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"name": "test_tool",
			},
		},
	}

	handler := server.forwardToServerMetaTool("describe_tool")
	result, err := handler(context.Background(), req)
	require.NoError(t, err)

	// Verify the call was forwarded with arguments
	assert.Equal(t, "describe_tool", mockMCPClient.lastCallToolRequest.Params.Name)

	args, ok := mockMCPClient.lastCallToolRequest.Params.Arguments.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "test_tool", args["name"])

	// Verify result
	assert.False(t, result.IsError)
}

// TestForwardToServerMetaToolError tests error handling in the transport bridge.
func TestForwardToServerMetaToolError(t *testing.T) {
	mockMCPClient := &MockMCPGoClient{
		callToolError: fmt.Errorf("connection failed"),
	}

	client := &Client{
		client:     mockMCPClient,
		mu:         sync.RWMutex{},
		formatters: NewFormatters(),
	}

	server := &MCPServer{
		client: client,
		logger: NewLogger(false, false, false),
	}

	handler := server.forwardToServerMetaTool("list_tools")
	result, err := handler(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err) // Handler returns error in result, not as error

	// Verify error result
	assert.True(t, result.IsError)
	textContent, ok := mcp.AsTextContent(result.Content[0])
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "failed")
}

// TestForwardToServerMetaToolCallTool tests the special case of the call_tool
// meta-tool which wraps nested tool calls.
func TestForwardToServerMetaToolCallTool(t *testing.T) {
	// Mock response for a wrapped tool call
	wrappedResult, _ := json.Marshal(map[string]interface{}{
		"isError": false,
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "Tool executed successfully",
			},
		},
	})

	mockMCPClient := &MockMCPGoClient{
		callToolResponses: map[string]*mcp.CallToolResult{
			"call_tool": mcp.NewToolResultText(string(wrappedResult)),
		},
	}

	client := &Client{
		client:     mockMCPClient,
		mu:         sync.RWMutex{},
		formatters: NewFormatters(),
	}

	server := &MCPServer{
		client: client,
		logger: NewLogger(false, false, false),
	}

	// Create request to call a nested tool
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"name": "core_service_list",
				"arguments": map[string]interface{}{
					"namespace": "default",
				},
			},
		},
	}

	handler := server.forwardToServerMetaTool("call_tool")
	result, err := handler(context.Background(), req)
	require.NoError(t, err)

	// Verify the call_tool was forwarded to server
	assert.Equal(t, "call_tool", mockMCPClient.lastCallToolRequest.Params.Name)

	// Verify nested args were forwarded
	args, ok := mockMCPClient.lastCallToolRequest.Params.Arguments.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "core_service_list", args["name"])
	nestedArgs, ok := args["arguments"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "default", nestedArgs["namespace"])

	// Verify result
	assert.False(t, result.IsError)
}

// TestMCPServerRegistersTools tests that NewMCPServer correctly registers
// all meta-tools as transport bridge handlers.
func TestMCPServerRegistersTools(t *testing.T) {
	mockMCPClient := &MockMCPGoClient{
		callToolResponses: map[string]*mcp.CallToolResult{
			"list_tools": mcp.NewToolResultText(`[]`),
		},
	}

	client := &Client{
		client:     mockMCPClient,
		mu:         sync.RWMutex{},
		formatters: NewFormatters(),
	}

	server, err := NewMCPServer(client, NewLogger(false, false, false), false)
	require.NoError(t, err)
	require.NotNil(t, server)

	// The server should have registered tools
	// We verify by checking the mcpServer is not nil
	assert.NotNil(t, server.mcpServer)
}

// The aggregator's notifications arrive on the goroutine that reads the
// response stream of the call in flight; the hand-off to NotificationChan
// must never wait for a reader, or the stream stalls in front of the call's
// own result (giantswarm-platform-manager's `platformctl verify` ended in the
// bridge's deadline that way while the manager had answered in seconds).
func TestQueueNotificationNeverWaitsForAReader(t *testing.T) {
	c := &Client{NotificationChan: make(chan mcp.JSONRPCNotification, 2)}
	n := mcp.JSONRPCNotification{Notification: mcp.Notification{Method: mcp.MethodNotificationToolsListChanged}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			c.queueNotification(n)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("queueNotification waited for a reader of NotificationChan")
	}
	assert.Len(t, c.NotificationChan, 2)
}

// In --mcp-server mode the bridge reads the aggregator's notifications and
// relays the list_changed ones to the assistant; anything else is consumed
// without reaching it.
func TestRelayNotificationsForwardsListChangedToTheAssistant(t *testing.T) {
	c := &Client{NotificationChan: make(chan mcp.JSONRPCNotification, 10)}
	m := &MCPServer{client: c, notifyClients: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 10)
	go m.relayNotifications(ctx, func(method string) { got <- method })
	for _, method := range []string{"notifications/events/follow", mcp.MethodNotificationToolsListChanged, mcp.MethodNotificationResourcesListChanged} {
		c.NotificationChan <- mcp.JSONRPCNotification{Notification: mcp.Notification{Method: method}}
	}
	var relayed []string
	for len(relayed) < 2 {
		select {
		case method := <-got:
			relayed = append(relayed, method)
		case <-time.After(2 * time.Second):
			t.Fatalf("relayed %v, want the two list_changed notifications", relayed)
		}
	}
	assert.Equal(t, []string{mcp.MethodNotificationToolsListChanged, mcp.MethodNotificationResourcesListChanged}, relayed)
	select {
	case method := <-got:
		t.Fatalf("%s reached the assistant", method)
	case <-time.After(100 * time.Millisecond):
	}
}
