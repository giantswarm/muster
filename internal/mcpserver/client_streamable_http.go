package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"muster/internal/oauth"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// StreamableHTTPClient implements the MCPClient interface using StreamableHTTP transport.
// It connects to remote MCP servers using HTTP with streaming support.
type StreamableHTTPClient struct {
	baseMCPClient
	url     string
	headers map[string]string
}

// NewStreamableHTTPClient creates a new StreamableHTTP-based MCP client without custom headers
func NewStreamableHTTPClient(url string) *StreamableHTTPClient {
	return &StreamableHTTPClient{
		url:     url,
		headers: make(map[string]string),
	}
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
		if authErr := c.checkForAuthRequiredError(err); authErr != nil {
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

// checkForAuthRequiredError examines an error to determine if it's a 401 authentication
// required error. If so, it returns an AuthRequiredError with parsed OAuth parameters.
func (c *StreamableHTTPClient) checkForAuthRequiredError(err error) *AuthRequiredError {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Check for 401 status code in the error message
	// The mcp-go library returns errors like "request failed with status 401: ..."
	if !strings.Contains(errStr, "401") &&
		!strings.Contains(errStr, http.StatusText(http.StatusUnauthorized)) {
		return nil
	}

	// Extract WWW-Authenticate header information if available
	// The error message may contain JSON with OAuth information
	authInfo := AuthInfo{}

	// Try to parse any WWW-Authenticate-style information from the error
	// Look for common OAuth indicators
	if strings.Contains(errStr, "Bearer") {
		// Try to extract realm/issuer from error message
		authInfo = c.parseAuthInfoFromError(errStr)
	}

	return &AuthRequiredError{
		URL:      c.url,
		AuthInfo: authInfo,
		Err:      fmt.Errorf("server returned 401 Unauthorized"),
	}
}

// parseAuthInfoFromError attempts to extract OAuth information from an error message.
// This is a best-effort parse since we can't directly access HTTP response headers.
func (c *StreamableHTTPClient) parseAuthInfoFromError(errStr string) AuthInfo {
	info := AuthInfo{}

	// Try to parse as WWW-Authenticate header format if present
	// The error might contain the raw header value
	if idx := strings.Index(errStr, "Bearer"); idx >= 0 {
		headerPart := errStr[idx:]
		// Find the end of the Bearer challenge
		if endIdx := strings.Index(headerPart, "\n"); endIdx > 0 {
			headerPart = headerPart[:endIdx]
		}
		params := oauth.ParseWWWAuthenticate(headerPart)
		if params != nil {
			info.Issuer = params.Realm
			info.Scope = params.Scope
			info.ResourceMetadataURL = params.ResourceMetadataURL
		}
	}

	return info
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
