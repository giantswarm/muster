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
// with mcp-go's built-in OAuth handler for automatic bearer token injection and
// typed 401 error handling.
//
// Instead of manually injecting Authorization headers via WithHTTPHeaderFunc,
// this client delegates token management to mcp-go's WithHTTPOAuth transport option.
// The transport.TokenStore adapter bridges muster's session-scoped token management
// to mcp-go's OAuth handler, enabling:
//   - Automatic bearer token injection on every request
//   - Typed OAuthAuthorizationRequiredError on 401 (preserving error details)
//   - Transparent token refresh via the TokenStore
type DynamicAuthClient struct {
	baseMCPClient
	url        string
	tokenStore transport.TokenStore
	scope      string
}

// NewDynamicAuthClient creates a new StreamableHTTP-based MCP client with mcp-go's
// built-in OAuth handler. The TokenStore is queried on each HTTP request to get
// the current access token for bearer injection.
//
// Args:
//   - url: The MCP server URL
//   - tokenStore: Adapter providing OAuth tokens (implements transport.TokenStore)
//   - scope: The OAuth scope for this connection
//
// Returns a new DynamicAuthClient ready for initialization.
func NewDynamicAuthClient(url string, tokenStore transport.TokenStore, scope string) *DynamicAuthClient {
	return &DynamicAuthClient{
		url:        url,
		tokenStore: tokenStore,
		scope:      scope,
	}
}

// Initialize establishes the connection and performs protocol handshake.
// Uses mcp-go's WithHTTPOAuth for automatic token injection and typed 401 handling.
func (c *DynamicAuthClient) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	logging.Debug("DynamicAuthClient", "Creating StreamableHTTP client for URL: %s with OAuth handler", c.url)

	opts := []transport.StreamableHTTPCOption{
		transport.WithHTTPOAuth(transport.OAuthConfig{
			TokenStore: c.tokenStore,
			Scopes:     []string{c.scope},
		}),
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

		if authErr := CheckForAuthRequiredError(ctx, err, c.url); authErr != nil {
			logging.Debug("DynamicAuthClient", "Authentication required for URL: %s", c.url)
			return authErr
		}

		return fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}

	c.client = mcpClient
	c.connected = true

	logging.Debug("DynamicAuthClient", "StreamableHTTP client initialized with OAuth handler. Server: %s, Version: %s",
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
