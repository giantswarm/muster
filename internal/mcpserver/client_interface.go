package mcpserver

import (
	"context"
	"fmt"
	"sync"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// MCPClient defines the interface for MCP client implementations.
// All transport types (stdio, SSE, streamable-http) implement this interface,
// enabling polymorphic usage and easier testing with mocks.
type MCPClient interface {
	// Initialize establishes the connection and performs protocol handshake
	Initialize(ctx context.Context) error
	// Close cleanly shuts down the client connection
	Close() error
	// ListTools returns all available tools from the server
	ListTools(ctx context.Context) ([]mcp.Tool, error)
	// CallTool executes a specific tool and returns the result
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error)
	// ListResources returns all available resources from the server
	ListResources(ctx context.Context) ([]mcp.Resource, error)
	// ReadResource retrieves a specific resource
	ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error)
	// ListPrompts returns all available prompts from the server
	ListPrompts(ctx context.Context) ([]mcp.Prompt, error)
	// GetPrompt retrieves a specific prompt
	GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error)
	// Ping checks if the server is responsive
	Ping(ctx context.Context) error
}

// Compile-time interface compliance checks
var (
	_ MCPClient = (*StdioClient)(nil)
	_ MCPClient = (*SSEClient)(nil)
	_ MCPClient = (*StreamableHTTPClient)(nil)
)

// baseMCPClient provides common functionality for all MCP client implementations.
// It implements the shared MCP protocol operations that are identical across
// different transport types (stdio, SSE, streamable-http).
type baseMCPClient struct {
	client    client.MCPClient
	mu        sync.RWMutex
	connected bool
}

// checkConnected verifies the client is connected and returns an error if not.
// This is a helper for consistent error handling across all MCP operations.
// Note: Caller must hold at least a read lock on mu.
func (b *baseMCPClient) checkConnected() error {
	if !b.connected || b.client == nil {
		return fmt.Errorf("client not connected")
	}
	return nil
}

// closeClient performs the common close logic
func (b *baseMCPClient) closeClient() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.connected || b.client == nil {
		return nil
	}

	err := b.client.Close()
	b.connected = false
	b.client = nil

	return err
}

// listTools returns all available tools from the server
func (b *baseMCPClient) listTools(ctx context.Context) ([]mcp.Tool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	result, err := b.client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	return result.Tools, nil
}

// callTool executes a specific tool and returns the result
func (b *baseMCPClient) callTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	result, err := b.client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call tool: %w", err)
	}

	return result, nil
}

// listResources returns all available resources from the server
func (b *baseMCPClient) listResources(ctx context.Context) ([]mcp.Resource, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	result, err := b.client.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	return result.Resources, nil
}

// readResource retrieves a specific resource
func (b *baseMCPClient) readResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	result, err := b.client.ReadResource(ctx, mcp.ReadResourceRequest{
		Params: struct {
			URI       string         `json:"uri"`
			Arguments map[string]any `json:"arguments,omitempty"`
		}{
			URI: uri,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read resource: %w", err)
	}

	return result, nil
}

// listPrompts returns all available prompts from the server
func (b *baseMCPClient) listPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	result, err := b.client.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}

	return result.Prompts, nil
}

// getPrompt retrieves a specific prompt
func (b *baseMCPClient) getPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return nil, err
	}

	// Convert args to map[string]string as required by the API
	stringArgs := make(map[string]string)
	for k, v := range args {
		if str, ok := v.(string); ok {
			stringArgs[k] = str
		} else {
			stringArgs[k] = fmt.Sprintf("%v", v)
		}
	}

	result, err := b.client.GetPrompt(ctx, mcp.GetPromptRequest{
		Params: struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments,omitempty"`
		}{
			Name:      name,
			Arguments: stringArgs,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt: %w", err)
	}

	return result, nil
}

// ping checks if the server is responsive
func (b *baseMCPClient) ping(ctx context.Context) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if err := b.checkConnected(); err != nil {
		return err
	}

	return b.client.Ping(ctx)
}
