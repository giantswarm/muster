package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// MCPClientTimeouts holds timeout configuration for MCP operations
type MCPClientTimeouts struct {
	Connect     time.Duration // Connection establishment timeout
	CallTool    time.Duration // Individual tool call timeout
	ListTools   time.Duration // Tool listing timeout
	HTTPTimeout time.Duration // HTTP transport timeout
	IdleTimeout time.Duration // HTTP idle connection timeout
}

// DefaultMCPClientTimeouts returns reasonable default timeout values
func DefaultMCPClientTimeouts() MCPClientTimeouts {
	return MCPClientTimeouts{
		Connect:     30 * time.Second,
		CallTool:    30 * time.Second,
		ListTools:   15 * time.Second,
		HTTPTimeout: 10 * time.Second, // Transport-level timeout
		IdleTimeout: 90 * time.Second, // How long to keep idle connections
	}
}

// AggressiveMCPClientTimeouts returns shorter timeouts for testing scenarios
func AggressiveMCPClientTimeouts() MCPClientTimeouts {
	return MCPClientTimeouts{
		Connect:     10 * time.Second,
		CallTool:    15 * time.Second,
		ListTools:   8 * time.Second,
		HTTPTimeout: 5 * time.Second, // Shorter transport timeout
		IdleTimeout: 30 * time.Second,
	}
}

// mcpTestClient implements the MCPTestClient interface
type mcpTestClient struct {
	client   client.MCPClient
	endpoint string
	debug    bool
	logger   TestLogger
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
	c.endpoint = endpoint

	if c.debug {
		c.logger.Debug("🔗 Connecting to MCP aggregator at %s\n", endpoint)
	}

	// Create streamable HTTP client for muster aggregator
	httpClient, err := client.NewStreamableHttpClient(endpoint)
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

// CallTool invokes an MCP tool with the given args
func (c *mcpTestClient) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	if c.client == nil {
		return nil, fmt.Errorf("MCP client not connected")
	}

	// Convert args to the format expected by the MCP client
	var toolArgs interface{}
	if args != nil {
		toolArgs = args
	}

	if c.debug {
		argsJSON, _ := json.MarshalIndent(toolArgs, "", "  ")
		c.logger.Debug("🔧 Calling tool: %s\n", toolName)
		c.logger.Debug("📋 Args: %s\n", string(argsJSON))
	}

	// Create timeout context for the tool call
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Create the request using the pattern from the existing codebase
	request := mcp.CallToolRequest{
		Params: struct {
			Name      string    `json:"name"`
			Arguments any       `json:"arguments,omitempty"`
			Meta      *mcp.Meta `json:"_meta,omitempty"`
		}{
			Name:      toolName,
			Arguments: toolArgs,
		},
	}

	// Make the tool call
	result, err := c.client.CallTool(callCtx, request)
	if err != nil {
		if c.debug {
			c.logger.Debug("❌ Tool call failed: %v\n", err)
		}
		return nil, fmt.Errorf("tool call %s failed: %w", toolName, err)
	}

	if c.debug {
		resultJSON, _ := json.MarshalIndent(result, "", "  ")
		c.logger.Debug("✅ Tool call result: %s\n", string(resultJSON))
	}

	return result, nil
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
