package mcpserver

import (
	"context"
	"fmt"
	"io"
	"muster/pkg/logging"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// StdioClient implements the aggregator.MCPClient interface using stdio transport
type StdioClient struct {
	command   string
	args      []string
	env       map[string]string
	client    client.MCPClient
	mu        sync.RWMutex
	connected bool
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
			Capabilities: mcp.ClientCapabilities{
				// Empty capabilities for client
			},
		},
	})
	if err != nil {
		logging.Error("StdioClient", err, "Failed to initialize MCP protocol for %s", c.command)
		// Ensure we close the client to clean up any processes
		closeErr := mcpClient.Close()
		if closeErr != nil {
			logging.Debug("StdioClient", "Error closing failed client for %s: %v", c.command, closeErr)
		}
		return fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}

	logging.Debug("StdioClient", "MCP protocol initialized successfully for %s", c.command)

	// Store the initialized client
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
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil
	}

	err := c.client.Close()
	c.connected = false
	c.client = nil

	return err
}

// ListTools returns all available tools from the server
func (c *StdioClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	return result.Tools, nil
}

// CallTool executes a specific tool and returns the result
func (c *StdioClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.CallTool(ctx, mcp.CallToolRequest{
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

// ListResources returns all available resources from the server
func (c *StdioClient) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	return result.Resources, nil
}

// ReadResource retrieves a specific resource
func (c *StdioClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ReadResource(ctx, mcp.ReadResourceRequest{
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

// ListPrompts returns all available prompts from the server
func (c *StdioClient) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}

	return result.Prompts, nil
}

// GetPrompt retrieves a specific prompt
func (c *StdioClient) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
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

	result, err := c.client.GetPrompt(ctx, mcp.GetPromptRequest{
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

// Ping checks if the server is responsive
func (c *StdioClient) Ping(ctx context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return fmt.Errorf("client not connected")
	}

	// Use the Ping method from the mcp-go client
	return c.client.Ping(ctx)
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
// This is kept for backward compatibility but the StdioClient is preferred
type SSEClient struct {
	url       string
	client    client.MCPClient
	mu        sync.RWMutex
	connected bool
}

// NewSSEClient creates a new SSE-based MCP client
func NewSSEClient(url string) *SSEClient {
	return &SSEClient{
		url: url,
	}
}

// Initialize establishes the connection and performs protocol handshake
func (c *SSEClient) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	// Create SSE client
	mcpClient, err := client.NewSSEMCPClient(c.url)
	if err != nil {
		return fmt.Errorf("failed to create SSE client: %w", err)
	}

	// Start the SSE transport
	if err := mcpClient.Start(ctx); err != nil {
		return fmt.Errorf("failed to start SSE transport: %w", err)
	}

	// Initialize the MCP protocol
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
			Capabilities: mcp.ClientCapabilities{
				// Empty capabilities for client
			},
		},
	})
	if err != nil {
		mcpClient.Close()
		return fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}

	// Store the initialized client
	c.client = mcpClient
	c.connected = true

	// Log server capabilities
	if initResult.Capabilities.Tools != nil {
		// Server supports tools
	}
	if initResult.Capabilities.Resources != nil {
		// Server supports resources
	}
	if initResult.Capabilities.Prompts != nil {
		// Server supports prompts
	}

	return nil
}

// Close cleanly shuts down the client connection
func (c *SSEClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil
	}

	err := c.client.Close()
	c.connected = false
	c.client = nil

	return err
}

// ListTools returns all available tools from the server
func (c *SSEClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	return result.Tools, nil
}

// CallTool executes a specific tool and returns the result
func (c *SSEClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.CallTool(ctx, mcp.CallToolRequest{
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

// ListResources returns all available resources from the server
func (c *SSEClient) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	return result.Resources, nil
}

// ReadResource retrieves a specific resource
func (c *SSEClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ReadResource(ctx, mcp.ReadResourceRequest{
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

// ListPrompts returns all available prompts from the server
func (c *SSEClient) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}

	return result.Prompts, nil
}

// GetPrompt retrieves a specific prompt
func (c *SSEClient) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
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

	result, err := c.client.GetPrompt(ctx, mcp.GetPromptRequest{
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

// Ping checks if the server is responsive
func (c *SSEClient) Ping(ctx context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return fmt.Errorf("client not connected")
	}

	// Use the Ping method from the mcp-go client
	return c.client.Ping(ctx)
}

// StreamableHTTPClient implements the aggregator.MCPClient interface using StreamableHTTP transport
type StreamableHTTPClient struct {
	url       string
	headers   map[string]string
	client    client.MCPClient
	mu        sync.RWMutex
	connected bool
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

	// Create StreamableHTTP client using the correct constructor from mcp-go
	// Based on: https://mcp-go.dev/clients/transports#streamablehttp-client

	// Add headers if provided
	if len(c.headers) > 0 {
		// Note: Will need to import transport package for WithHTTPHeaders option
		logging.Debug("StreamableHTTPClient", "Headers provided but not yet implemented: %v", c.headers)
	}

	// Create the client - handle both return values (client, error)
	mcpClient, err := client.NewStreamableHttpClient(c.url) // Note: lowercase 'h' in Http
	if err != nil {
		return fmt.Errorf("failed to create StreamableHTTP client: %w", err)
	}

	c.client = mcpClient

	// Initialize the MCP protocol
	initResult, err := c.client.Initialize(ctx, mcp.InitializeRequest{
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
			Capabilities: mcp.ClientCapabilities{
				// Empty capabilities for client
			},
		},
	})
	if err != nil {
		c.client.Close()
		return fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}

	logging.Debug("StreamableHTTPClient", "StreamableHTTP client initialized. Server: %s, Version: %s",
		initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	c.connected = true
	return nil
}

// Close cleanly shuts down the client connection
func (c *StreamableHTTPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil
	}

	err := c.client.Close()
	c.connected = false
	c.client = nil

	return err
}

// ListTools returns all available tools from the server
func (c *StreamableHTTPClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	return result.Tools, nil
}

// CallTool executes a specific tool and returns the result
func (c *StreamableHTTPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.CallTool(ctx, mcp.CallToolRequest{
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

// ListResources returns all available resources from the server
func (c *StreamableHTTPClient) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	return result.Resources, nil
}

// ReadResource retrieves a specific resource
func (c *StreamableHTTPClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ReadResource(ctx, mcp.ReadResourceRequest{
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

// ListPrompts returns all available prompts from the server
func (c *StreamableHTTPClient) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}

	return result.Prompts, nil
}

// GetPrompt retrieves a specific prompt
func (c *StreamableHTTPClient) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("client not connected")
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

	result, err := c.client.GetPrompt(ctx, mcp.GetPromptRequest{
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

// Ping checks if the server is responsive
func (c *StreamableHTTPClient) Ping(ctx context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return fmt.Errorf("client not connected")
	}

	// Use the Ping method from the mcp-go client
	return c.client.Ping(ctx)
}
