package mcpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// StreamableHTTPClient implements the MCPClient interface using StreamableHTTP transport.
// It connects to remote MCP servers using HTTP with streaming support.
type StreamableHTTPClient struct {
	baseMCPClient
	url        string
	headers    map[string]string
	httpClient *http.Client // Custom HTTP client (e.g., for Teleport TLS)
}

// NewStreamableHTTPClientWithHeaders creates a new StreamableHTTP-based MCP client with custom headers
func NewStreamableHTTPClientWithHeaders(url string, headers map[string]string) *StreamableHTTPClient {
	if headers == nil {
		headers = make(map[string]string)
	}
	return &StreamableHTTPClient{
		url:     url,
		headers: headers,
	}
}

// NewStreamableHTTPClientWithHTTPClient creates a new StreamableHTTP-based MCP client with a custom HTTP client.
// This is useful for Teleport authentication where the HTTP client needs custom TLS certificates.
func NewStreamableHTTPClientWithHTTPClient(url string, headers map[string]string, httpClient *http.Client) *StreamableHTTPClient {
	if headers == nil {
		headers = make(map[string]string)
	}
	return &StreamableHTTPClient{
		url:        url,
		headers:    headers,
		httpClient: httpClient,
	}
}

// Initialize establishes the connection and performs protocol handshake
func (c *StreamableHTTPClient) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	logging.Debug("StreamableHTTPClient", "Creating StreamableHTTP client for URL: %s", c.url)

	// Build client options including headers if provided
	var opts []transport.StreamableHTTPCOption
	if len(c.headers) > 0 {
		opts = append(opts, transport.WithHTTPHeaders(c.headers))
		logging.Debug("StreamableHTTPClient", "Configured %d custom headers", len(c.headers))
	}

	// If a custom HTTP client is provided (e.g., for Teleport TLS), use it
	if c.httpClient != nil {
		opts = append(opts, transport.WithHTTPBasicClient(c.httpClient))
		logging.Debug("StreamableHTTPClient", "Using custom HTTP client (e.g., Teleport TLS)")
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
		if authErr := CheckForAuthRequiredError(ctx, err, c.url); authErr != nil {
			logging.Debug("StreamableHTTPClient", "Authentication required for URL: %s", c.url)
			return authErr
		}

		return fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}

	c.client = mcpClient
	c.connected = true

	logging.Debug("StreamableHTTPClient", "StreamableHTTP client initialized. Server: %s, Version: %s",
		initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	return nil
}

// Close cleanly shuts down the client connection
func (c *StreamableHTTPClient) Close() error {
	return c.closeClient()
}

// ListTools returns all available tools from the server
func (c *StreamableHTTPClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	return c.listTools(ctx)
}

// CallTool executes a specific tool and returns the result
func (c *StreamableHTTPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	return c.callTool(ctx, name, args)
}

// ListResources returns all available resources from the server
func (c *StreamableHTTPClient) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	return c.listResources(ctx)
}

// ReadResource retrieves a specific resource
func (c *StreamableHTTPClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	return c.readResource(ctx, uri)
}

// ListPrompts returns all available prompts from the server
func (c *StreamableHTTPClient) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	return c.listPrompts(ctx)
}

// GetPrompt retrieves a specific prompt
func (c *StreamableHTTPClient) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	return c.getPrompt(ctx, name, args)
}

// Ping checks if the server is responsive
func (c *StreamableHTTPClient) Ping(ctx context.Context) error {
	return c.ping(ctx)
}
