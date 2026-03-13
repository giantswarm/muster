package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// testTokenStore implements transport.TokenStore for the test client.
// It holds a pre-set access token and returns it on GetToken(), allowing
// mcp-go's WithHTTPOAuth to handle bearer token injection and typed 401 errors.
type testTokenStore struct {
	token *transport.Token
}

func (s *testTokenStore) GetToken(_ context.Context) (*transport.Token, error) {
	if s.token == nil {
		return nil, transport.ErrNoToken
	}
	return s.token, nil
}

func (s *testTokenStore) SaveToken(_ context.Context, token *transport.Token) error {
	s.token = token
	return nil
}

var _ transport.TokenStore = (*testTokenStore)(nil)

// mcpTestClient implements the MCPTestClient interface
type mcpTestClient struct {
	client      client.MCPClient
	endpoint    string
	debug       bool
	logger      TestLogger
	accessToken string // Current access token used for authentication
}

// NewMCPTestClient creates a new MCP test client
func NewMCPTestClient(debug bool) MCPTestClient {
	return &mcpTestClient{
		debug:  debug,
		logger: NewStdoutLogger(false, debug), // Default to stdout logger
	}
}

// NewMCPTestClientWithLogger creates a new MCP test client with custom logger
func NewMCPTestClientWithLogger(debug bool, logger TestLogger) MCPTestClient {
	return &mcpTestClient{
		debug:  debug,
		logger: logger,
	}
}

// Connect establishes connection to the MCP aggregator
func (c *mcpTestClient) Connect(ctx context.Context, endpoint string) error {
	return c.connectWithOptions(ctx, endpoint, "")
}

// ConnectWithAuth establishes connection to the MCP aggregator with an access token.
// This is used when muster's OAuth server is enabled and requires authentication.
func (c *mcpTestClient) ConnectWithAuth(ctx context.Context, endpoint, accessToken string) error {
	return c.connectWithOptions(ctx, endpoint, accessToken)
}

// connectWithOptions establishes connection with optional authentication.
func (c *mcpTestClient) connectWithOptions(ctx context.Context, endpoint, accessToken string) error {
	c.endpoint = endpoint
	c.accessToken = accessToken

	if c.debug {
		if accessToken != "" {
			c.logger.Debug("🔗 Connecting to MCP aggregator at %s (with auth)\n", endpoint)
		} else {
			c.logger.Debug("🔗 Connecting to MCP aggregator at %s\n", endpoint)
		}
	}

	var opts []transport.StreamableHTTPCOption

	if accessToken != "" {
		opts = append(opts, transport.WithHTTPOAuth(transport.OAuthConfig{
			TokenStore: &testTokenStore{
				token: &transport.Token{
					AccessToken: accessToken,
					TokenType:   "Bearer",
				},
			},
		}))
	}

	// Create streamable HTTP client for muster aggregator
	httpClient, err := client.NewStreamableHttpClient(endpoint, opts...)
	if err != nil {
		return fmt.Errorf("failed to create streamable HTTP client: %w", err)
	}

	// Start the streamable HTTP transport
	if err := httpClient.Start(ctx); err != nil {
		httpClient.Close() // Clean up failed client
		return fmt.Errorf("failed to start streamable HTTP client: %w", err)
	}

	// Initialize the MCP protocol
	initRequest := mcp.InitializeRequest{
		Params: struct {
			ProtocolVersion string                 `json:"protocolVersion"`
			Capabilities    mcp.ClientCapabilities `json:"capabilities"`
			ClientInfo      mcp.Implementation     `json:"clientInfo"`
		}{
			ProtocolVersion: "2024-11-05",
			ClientInfo: mcp.Implementation{
				Name:    "muster-test-client",
				Version: "1.0.0",
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	}

	// Initialize with timeout
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// CRITICAL: Only store the client AFTER successful initialization
	_, err = httpClient.Initialize(initCtx, initRequest)
	if err != nil {
		httpClient.Close() // Clean up failed client
		return fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}

	// SUCCESS: Store the client only after full initialization
	c.client = httpClient

	if c.debug {
		c.logger.Debug("✅ Successfully connected to MCP aggregator at %s\n", endpoint)
	}

	return nil
}

// CallTool executes a tool via MCP using the call_tool meta-tool.
// This implements the server-side meta-tools pattern (Issue #343) where all tool
// calls are routed through the call_tool meta-tool for centralized execution.
func (c *mcpTestClient) CallTool(ctx context.Context, toolName string, toolArgs map[string]interface{}) (interface{}, error) {
	if c.client == nil {
		return nil, fmt.Errorf("MCP client not connected")
	}

	if c.debug {
		argsJSON, _ := json.MarshalIndent(toolArgs, "", "  ")
		c.logger.Debug("🔧 Calling tool: %s\n", toolName)
		c.logger.Debug("📋 Args: %s\n", string(argsJSON))
	}

	timeout := 120 * time.Second

	// Create timeout context for the tool call
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wrap the tool call through the call_tool meta-tool
	// This is the server-side meta-tools pattern (Issue #343)
	metaToolArgs := map[string]interface{}{
		"name":      toolName,
		"arguments": toolArgs,
	}

	// Create the request for the call_tool meta-tool
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "call_tool",
			Arguments: metaToolArgs,
		},
	}

	// Make the tool call through call_tool meta-tool
	result, err := c.client.CallTool(callCtx, request)
	if err != nil {
		if c.debug {
			c.logger.Debug("❌ Tool call failed: %v\n", err)
		}
		return nil, fmt.Errorf("tool call %s failed: %w", toolName, err)
	}

	// Unwrap the nested response from call_tool
	// The call_tool meta-tool returns a JSON string containing the wrapped result
	unwrappedResult, err := c.unwrapMetaToolResponse(result, toolName)
	if err != nil {
		if c.debug {
			c.logger.Debug("❌ Failed to unwrap meta-tool response: %v\n", err)
		}
		return nil, fmt.Errorf("tool call %s failed: failed to unwrap response: %w", toolName, err)
	}

	if c.debug {
		resultJSON, _ := json.MarshalIndent(unwrappedResult, "", "  ")
		c.logger.Debug("✅ Tool call result: %s\n", string(resultJSON))
	}

	// Return the unwrapped result
	return unwrappedResult, nil
}

// CallToolDirect invokes an MCP tool directly on the server without wrapping through call_tool.
// This is needed for calling meta-tools (list_tools, describe_tool, etc.) that are registered
// on the MCP server but cannot be dispatched through CallToolInternal.
func (c *mcpTestClient) CallToolDirect(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if c.client == nil {
		return nil, fmt.Errorf("MCP client not connected")
	}

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}

	result, err := c.client.CallTool(callCtx, request)
	if err != nil {
		return nil, fmt.Errorf("direct tool call %s failed: %w", toolName, err)
	}

	return result, nil
}

// unwrapMetaToolResponse extracts the actual tool result from a call_tool meta-tool response.
// The call_tool meta-tool wraps tool results in a JSON structure for proper serialization.
func (c *mcpTestClient) unwrapMetaToolResponse(result *mcp.CallToolResult, toolName string) (*mcp.CallToolResult, error) {
	if result == nil {
		return nil, fmt.Errorf("nil result from call_tool")
	}

	// Check if the meta-tool call itself failed
	if result.IsError {
		// Extract error message from content
		var errorMsgs []string
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				errorMsgs = append(errorMsgs, textContent.Text)
			}
		}
		return nil, fmt.Errorf("meta-tool error: %s", fmt.Sprintf("%v", errorMsgs))
	}

	// The call_tool meta-tool returns a single text content containing the wrapped result as JSON
	if len(result.Content) == 0 {
		return nil, fmt.Errorf("empty content from call_tool")
	}

	// Get the JSON string from the first text content
	textContent, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		return nil, fmt.Errorf("unexpected content type from call_tool")
	}

	// Parse the wrapped result
	var wrappedResult struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
	}

	if err := json.Unmarshal([]byte(textContent.Text), &wrappedResult); err != nil {
		return nil, fmt.Errorf("failed to parse wrapped result: %w", err)
	}

	// Reconstruct the CallToolResult from the wrapped structure
	unwrapped := &mcp.CallToolResult{
		IsError: wrappedResult.IsError,
	}

	for _, item := range wrappedResult.Content {
		if item.Type == "text" {
			unwrapped.Content = append(unwrapped.Content, mcp.TextContent{
				Type: "text",
				Text: item.Text,
			})
		}
	}

	return unwrapped, nil
}

// ListTools returns available MCP tools
func (c *mcpTestClient) ListTools(ctx context.Context) ([]string, error) {
	if c.client == nil {
		return nil, fmt.Errorf("MCP client not connected")
	}

	// Create timeout context for the tools list request
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Get the list of available tools
	result, err := c.client.ListTools(listCtx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Extract tool names
	var toolNames []string
	for _, tool := range result.Tools {
		toolNames = append(toolNames, tool.Name)
	}

	if c.debug {
		c.logger.Debug("🛠️  Available tools (%d): %v\n", len(toolNames), toolNames)
	}

	return toolNames, nil
}

// ListToolsWithSchemas returns available MCP tools with their full schemas
func (c *mcpTestClient) ListToolsWithSchemas(ctx context.Context) ([]mcp.Tool, error) {
	if c.client == nil {
		return nil, fmt.Errorf("MCP client not connected")
	}

	// Create timeout context for the tools list request
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Get the list of available tools
	result, err := c.client.ListTools(listCtx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	if c.debug {
		c.logger.Debug("🛠️  Available tools with schemas (%d): %v\n", len(result.Tools), result.Tools)
	}

	return result.Tools, nil
}

// ReadResource reads an MCP resource by URI
func (c *mcpTestClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	if c.client == nil {
		return nil, fmt.Errorf("MCP client not connected")
	}

	if c.debug {
		c.logger.Debug("📖 Reading resource: %s\n", uri)
	}

	// Create timeout context for the resource read
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Create the request
	request := mcp.ReadResourceRequest{
		Params: struct {
			URI       string         `json:"uri"`
			Arguments map[string]any `json:"arguments,omitempty"`
		}{
			URI: uri,
		},
	}

	// Read the resource
	result, err := c.client.ReadResource(readCtx, request)
	if err != nil {
		if c.debug {
			c.logger.Debug("❌ Resource read failed: %v\n", err)
		}
		return nil, fmt.Errorf("resource read %s failed: %w", uri, err)
	}

	if c.debug {
		c.logger.Debug("✅ Resource read successful\n")
	}

	return result, nil
}

// Close closes the MCP connection
func (c *mcpTestClient) Close() error {
	if c.client == nil {
		return nil
	}

	if c.debug {
		c.logger.Debug("🔌 Closing MCP client connection to %s\n", c.endpoint)
	}

	err := c.client.Close()
	c.client = nil
	return err
}

// IsConnected returns whether the client is connected
func (c *mcpTestClient) IsConnected() bool {
	return c.client != nil
}

// GetEndpoint returns the current endpoint
func (c *mcpTestClient) GetEndpoint() string {
	return c.endpoint
}
