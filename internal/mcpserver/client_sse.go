package mcpserver

import (
	"context"
	"fmt"

	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// SSEClient implements the MCPClient interface using SSE transport.
// It connects to remote MCP servers using Server-Sent Events for communication.
type SSEClient struct {
	baseMCPClient
	url     string
	headers map[string]string
}

// NewSSEClient creates a new SSE-based MCP client without custom headers
func NewSSEClient(url string) *SSEClient {
	return &SSEClient{
		url:     url,
		headers: make(map[string]string),
	}
}

// NewSSEClientWithHeaders creates a new SSE-based MCP client with custom headers
func NewSSEClientWithHeaders(url string, headers map[string]string) *SSEClient {
	if headers == nil {
		headers = make(map[string]string)
	}
	return &SSEClient{
		url:     url,
		headers: headers,
	}
}

// Initialize establishes the connection and performs protocol handshake
func (c *SSEClient) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	logging.Debug("SSEClient", "Creating SSE client for URL: %s", c.url)

	// Build client options including headers if provided
	var opts []transport.ClientOption
	if len(c.headers) > 0 {
		opts = append(opts, transport.WithHeaders(c.headers))
		logging.Debug("SSEClient", "Configured %d custom headers", len(c.headers))
	}

	mcpClient, err := client.NewSSEMCPClient(c.url, opts...)
	if err != nil {
		return fmt.Errorf("failed to create SSE client: %w", err)
	}

	if err := mcpClient.Start(ctx); err != nil {
		// Check if this is a 401 authentication error
		if authErr := CheckForAuthRequiredError(err, c.url); authErr != nil {
			logging.Debug("SSEClient", "Authentication required for URL: %s", c.url)
			return authErr
		}
		return fmt.Errorf("failed to start SSE transport: %w", err)
	}

	initResult, err := mcpClient.Initialize(ctx, mcp.InitializeRequest{
		Params: struct {
			ProtocolVersion string                 `json:"protocolVersion"`
			Capabilities    mcp.ClientCapabilities `json:"capabilities"`
			ClientInfo      mcp.Implementation     `json:"clientInfo"`
		}{
			ProtocolVersion: "2024-11-05",
			ClientInfo: mcp.Implementation{
				Name:    "muster-aggregator",
				Version: "1.0.0",
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	})
	if err != nil {
		mcpClient.Close()

		// Check if this is a 401 authentication error
		if authErr := CheckForAuthRequiredError(err, c.url); authErr != nil {
			logging.Debug("SSEClient", "Authentication required for URL: %s", c.url)
			return authErr
		}

		return fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}

	c.client = mcpClient
	c.connected = true

	logging.Debug("SSEClient", "SSE client initialized. Server: %s, Version: %s",
		initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	return nil
}

// Close cleanly shuts down the client connection
func (c *SSEClient) Close() error {
	return c.closeClient()
}

// ListTools returns all available tools from the server
func (c *SSEClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	return c.listTools(ctx)
}

// CallTool executes a specific tool and returns the result
func (c *SSEClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	return c.callTool(ctx, name, args)
}

// ListResources returns all available resources from the server
func (c *SSEClient) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	return c.listResources(ctx)
}

// ReadResource retrieves a specific resource
func (c *SSEClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	return c.readResource(ctx, uri)
}

// ListPrompts returns all available prompts from the server
func (c *SSEClient) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	return c.listPrompts(ctx)
}

// GetPrompt retrieves a specific prompt
func (c *SSEClient) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	return c.getPrompt(ctx, name, args)
}

// Ping checks if the server is responsive
func (c *SSEClient) Ping(ctx context.Context) error {
	return c.ping(ctx)
}
