package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	mcp_client "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockMCPGoClient is a mock implementation of the mcp-go MCPClient for testing
type MockMCPGoClient struct {
	mcp_client.MCPClient
	tools []mcp.Tool
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
	return &mcp.CallToolResult{}, nil
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

// MockClient is a mock implementation of the agent Client for testing
type MockClient struct {
	toolCache     []mcp.Tool
	resourceCache []mcp.Resource
	promptCache   []mcp.Prompt
	mu            sync.RWMutex
}

func (m *MockClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	// Simple mock implementation
	if name == "test_tool" {
		return mcp.NewToolResultText("Test tool executed"), nil
	}
	return nil, fmt.Errorf("tool not found: %s", name)
}

func (m *MockClient) GetResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	// Simple mock implementation
	if uri == "test://resource" {
		return &mcp.ReadResourceResult{
			Contents: []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      uri,
					MIMEType: "text/plain",
					Text:     "Test resource content",
				},
			},
		}, nil
	}
	return nil, fmt.Errorf("resource not found: %s", uri)
}

func (m *MockClient) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	// Simple mock implementation
	if name == "test_prompt" {
		return &mcp.GetPromptResult{
			Messages: []mcp.PromptMessage{
				{
					Role:    mcp.RoleUser,
					Content: mcp.TextContent{Text: "Test prompt message"},
				},
			},
		}, nil
	}
	return nil, fmt.Errorf("prompt not found: %s", name)
}

func TestMCPServerListTools(t *testing.T) {
	// Create mock client with test data
	testTools := []mcp.Tool{
		{
			Name:        "test_tool_1",
			Description: "Test tool 1 description",
		},
		{
			Name:        "test_tool_2",
			Description: "Test tool 2 description",
		},
	}

	// Create MCP server with mock client
	server := &MCPServer{
		client: &Client{
			toolCache: testTools,
			mu:        sync.RWMutex{},
			client: &MockMCPGoClient{
				tools: testTools,
			},
			formatters: NewFormatters(),
		},
		logger: NewLogger(false, false, false),
	}

	// Test list tools handler
	result, err := server.handleListTools(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)

	// Verify result
	assert.False(t, result.IsError)
	assert.Len(t, result.Content, 1)

	// Parse JSON result
	textContent, ok := mcp.AsTextContent(result.Content[0])
	require.True(t, ok)

	var tools []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	err = json.Unmarshal([]byte(textContent.Text), &tools)
	require.NoError(t, err)

	assert.Len(t, tools, 2)
	assert.Equal(t, "test_tool_1", tools[0].Name)
	assert.Equal(t, "test_tool_2", tools[1].Name)
}

func TestMCPServerDescribeTool(t *testing.T) {
	// Create mock client with test data
	mockClient := &MockClient{
		toolCache: []mcp.Tool{
			{
				Name:        "test_tool",
				Description: "Test tool description",
				InputSchema: mcp.ToolInputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"param1": map[string]interface{}{
							"type":        "string",
							"description": "Arg 1",
						},
					},
				},
			},
		},
	}

	// Create MCP server with mock client
	server := &MCPServer{
		client: &Client{
			toolCache:  mockClient.toolCache,
			mu:         sync.RWMutex{},
			formatters: NewFormatters(),
		},
		logger: NewLogger(false, false, false),
	}

	// Test describe tool handler
	req := mcp.CallToolRequest{
		Params: struct {
			Name      string    `json:"name"`
			Arguments any       `json:"arguments,omitempty"`
			Meta      *mcp.Meta `json:"_meta,omitempty"`
		}{
			Arguments: map[string]interface{}{
				"name": "test_tool",
			},
		},
	}

	result, err := server.handleDescribeTool(context.Background(), req)
	require.NoError(t, err)

	// Verify result
	assert.False(t, result.IsError)
	assert.Len(t, result.Content, 1)

	// Parse JSON result
	textContent, ok := mcp.AsTextContent(result.Content[0])
	require.True(t, ok)

	var toolInfo map[string]interface{}
	err = json.Unmarshal([]byte(textContent.Text), &toolInfo)
	require.NoError(t, err)

	assert.Equal(t, "test_tool", toolInfo["name"])
	assert.Equal(t, "Test tool description", toolInfo["description"])
	assert.NotNil(t, toolInfo["inputSchema"])
}

// TestMCPServerHandlers tests the basic functionality of MCP server handlers
func TestMCPServerHandlers(t *testing.T) {
	// Create MCP server with minimal setup
	testTools := []mcp.Tool{
		{
			Name:        "test_tool",
			Description: "Test tool",
		},
	}
	server := &MCPServer{
		client: &Client{
			toolCache: testTools,
			resourceCache: []mcp.Resource{
				{
					URI:      "test://resource",
					Name:     "Test Resource",
					MIMEType: "text/plain",
				},
			},
			promptCache: []mcp.Prompt{
				{
					Name:        "test_prompt",
					Description: "Test prompt",
				},
			},
			mu: sync.RWMutex{},
			client: &MockMCPGoClient{
				tools: testTools,
			},
			formatters: NewFormatters(),
		},
		logger: NewLogger(false, false, false),
	}

	// Test empty caches
	t.Run("EmptyToolCache", func(t *testing.T) {
		emptyServer := &MCPServer{
			client: &Client{
				toolCache: []mcp.Tool{},
				mu:        sync.RWMutex{},
				client: &MockMCPGoClient{
					tools: []mcp.Tool{},
				},
				formatters: NewFormatters(),
			},
			logger: NewLogger(false, false, false),
		}

		result, err := emptyServer.handleListTools(context.Background(), mcp.CallToolRequest{})
		require.NoError(t, err)
		assert.False(t, result.IsError)

		textContent, ok := mcp.AsTextContent(result.Content[0])
		require.True(t, ok)
		assert.Equal(t, "No tools available", textContent.Text)
	})

	// Test tool not found
	t.Run("ToolNotFound", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
				Arguments: map[string]interface{}{
					"name": "nonexistent_tool",
				},
			},
		}

		result, err := server.handleDescribeTool(context.Background(), req)
		require.NoError(t, err)
		assert.True(t, result.IsError)
	})
}
