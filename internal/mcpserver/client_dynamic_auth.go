package mcpserver

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// DynamicAuthClient implements the MCPClient interface using StreamableHTTP transport
// with dynamic token injection. Instead of static headers, it uses a TokenProvider
// that's called on each HTTP request to get the current access token.
//
// This enables automatic token refresh for long-running sessions:
// - The TokenProvider can check if the token is expiring and refresh it
// - All HTTP requests will use the latest token automatically
// - No client recreation needed when tokens are refreshed
//
// This implements Issue #214: Automatic token refresh for MCP server session connections.
type DynamicAuthClient struct {
	baseMCPClient
	url           string
	tokenProvider TokenProvider
}

// NewDynamicAuthClient creates a new StreamableHTTP-based MCP client with dynamic token injection.
// The TokenProvider is called on each HTTP request to get the current access token.
//
// Args:
//   - url: The MCP server URL
//   - tokenProvider: Provider for OAuth access tokens (called on each request)
//
// Returns a new DynamicAuthClient ready for initialization.
func NewDynamicAuthClient(url string, tokenProvider TokenProvider) *DynamicAuthClient {
	if tokenProvider == nil {
		// Use a no-op provider if none provided
		tokenProvider = TokenProviderFunc(func(_ context.Context) string { return "" })
	}
	return &DynamicAuthClient{
		url:           url,
		tokenProvider: tokenProvider,
	}
}

// Initialize establishes the connection and performs protocol handshake.
// The TokenProvider is used to dynamically inject the Authorization header.
func (c *DynamicAuthClient) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	logging.Debug("DynamicAuthClient", "Creating StreamableHTTP client for URL: %s with dynamic auth", c.url)

	// Use the dynamic header function instead of static headers
	opts := []transport.StreamableHTTPCOption{
		transport.WithHTTPHeaderFunc(tokenProviderToHeaderFunc(c.tokenProvider)),
	}

	mcpClient, err := client.NewStreamableHttpClient(c.url, opts...)
	if err != nil {
		return fmt.Errorf("failed to create StreamableHTTP client: %w", err)
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
			logging.Debug("DynamicAuthClient", "Authentication required for URL: %s", c.url)
			return authErr
		}

		return fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}

	c.client = mcpClient
	c.connected = true

	logging.Debug("DynamicAuthClient", "StreamableHTTP client initialized with dynamic auth. Server: %s, Version: %s",
		initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	return nil
}

// Close cleanly shuts down the client connection
func (c *DynamicAuthClient) Close() error {
	return c.closeClient()
}

// ListTools returns all available tools from the server
func (c *DynamicAuthClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	return c.listTools(ctx)
}

// CallTool executes a specific tool and returns the result
func (c *DynamicAuthClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	return c.callTool(ctx, name, args)
}

// ListResources returns all available resources from the server
func (c *DynamicAuthClient) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	return c.listResources(ctx)
}

// ReadResource retrieves a specific resource
func (c *DynamicAuthClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	return c.readResource(ctx, uri)
}

// ListPrompts returns all available prompts from the server
func (c *DynamicAuthClient) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	return c.listPrompts(ctx)
}

// GetPrompt retrieves a specific prompt
func (c *DynamicAuthClient) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	return c.getPrompt(ctx, name, args)
}

// Ping checks if the server is responsive
func (c *DynamicAuthClient) Ping(ctx context.Context) error {
	return c.ping(ctx)
}
