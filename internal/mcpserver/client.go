package mcpserver

import (
	"context"
	"fmt"
	"io"
	"muster/pkg/logging"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
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
		Params: struct {
			Name      string    `json:"name"`
			Arguments any       `json:"arguments,omitempty"`
			Meta      *mcp.Meta `json:"_meta,omitempty"`
		}{
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

// StdioClient implements the aggregator.MCPClient interface using stdio transport
type StdioClient struct {
	baseMCPClient
	command string
	args    []string
	env     map[string]string
}

// NewStdioClient creates a new stdio-based MCP client
func NewStdioClient(command string, args []string) *StdioClient {
	return &StdioClient{
		command: command,
		args:    args,
		env:     make(map[string]string),
	}
}

// NewStdioClientWithEnv creates a new stdio-based MCP client with environment variables
func NewStdioClientWithEnv(command string, args []string, env map[string]string) *StdioClient {
	return &StdioClient{
		command: command,
		args:    args,
		env:     env,
	}
}

// Initialize establishes the connection and performs protocol handshake
func (c *StdioClient) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	logging.Debug("StdioClient", "Creating stdio client for command: %s %v with env: %v", c.command, c.args, c.env)

	// Convert environment map to slice of strings
	var envStrings []string
	for k, v := range c.env {
		envStrings = append(envStrings, fmt.Sprintf("%s=%s", k, v))
	}

	// Create stdio client - it will start the process
	mcpClient, err := client.NewStdioMCPClient(c.command, envStrings, c.args...)
	if err != nil {
		return fmt.Errorf("failed to create stdio client: %w", err)
	}

	logging.Debug("StdioClient", "Stdio client created, initializing MCP protocol for %s", c.command)

	// Initialize the MCP protocol with timeout from context
	// If no timeout in context, add a reasonable default (reduced from 30s to 10s for faster failures)
	initCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		initCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	initResult, err := mcpClient.Initialize(initCtx, mcp.InitializeRequest{
		Params: struct {
			ProtocolVersion string                 `json:"protocolVersion"`
			Capabilities    mcp.ClientCapabilities `json:"capabilities"`
			ClientInfo      mcp.Implementation     `json:"clientInfo"`
		}{
			ProtocolVersion: "2024-11-05",
			ClientInfo: mcp.Implementation{
				Name:    "muster",
				Version: "1.0.0",
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	})
	if err != nil {
		logging.Error("StdioClient", err, "Failed to initialize MCP protocol for %s", c.command)
		closeErr := mcpClient.Close()
		if closeErr != nil {
			logging.Debug("StdioClient", "Error closing failed client for %s: %v", c.command, closeErr)
		}
		return fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}

	logging.Debug("StdioClient", "MCP protocol initialized successfully for %s", c.command)

	c.client = mcpClient
	c.connected = true

	// Log server capabilities
	if initResult.Capabilities.Tools != nil {
		logging.Debug("StdioClient", "Server %s supports tools", c.command)
	}
	if initResult.Capabilities.Resources != nil {
		logging.Debug("StdioClient", "Server %s supports resources", c.command)
	}
	if initResult.Capabilities.Prompts != nil {
		logging.Debug("StdioClient", "Server %s supports prompts", c.command)
	}

	return nil
}

// Close cleanly shuts down the client connection
func (c *StdioClient) Close() error {
	return c.closeClient()
}

// ListTools returns all available tools from the server
func (c *StdioClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	return c.listTools(ctx)
}

// CallTool executes a specific tool and returns the result
func (c *StdioClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	return c.callTool(ctx, name, args)
}

// ListResources returns all available resources from the server
func (c *StdioClient) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	return c.listResources(ctx)
}

// ReadResource retrieves a specific resource
func (c *StdioClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	return c.readResource(ctx, uri)
}

// ListPrompts returns all available prompts from the server
func (c *StdioClient) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	return c.listPrompts(ctx)
}

// GetPrompt retrieves a specific prompt
func (c *StdioClient) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	return c.getPrompt(ctx, name, args)
}

// Ping checks if the server is responsive
func (c *StdioClient) Ping(ctx context.Context) error {
	return c.ping(ctx)
}

// GetStderr returns a reader for the stderr output of the subprocess
func (c *StdioClient) GetStderr() (io.Reader, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, false
	}

	// Type assert to *client.Client as GetStderr expects the concrete type
	if concreteClient, ok := c.client.(*client.Client); ok {
		return client.GetStderr(concreteClient)
	}

	return nil, false
}

// SSEClient implements the aggregator.MCPClient interface using SSE transport
type SSEClient struct {
	baseMCPClient
	url     string
	headers map[string]string
}

// NewSSEClient creates a new SSE-based MCP client
func NewSSEClient(url string) *SSEClient {
	return &SSEClient{
		url:     url,
		headers: make(map[string]string),
	}
}

// NewSSEClientWithHeaders creates a new SSE-based MCP client with custom headers
func NewSSEClientWithHeaders(url string, headers map[string]string) *SSEClient {
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

// StreamableHTTPClient implements the aggregator.MCPClient interface using StreamableHTTP transport
type StreamableHTTPClient struct {
	baseMCPClient
	url     string
	headers map[string]string
}

// NewStreamableHTTPClient creates a new StreamableHTTP-based MCP client
func NewStreamableHTTPClient(url string, headers map[string]string) *StreamableHTTPClient {
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
