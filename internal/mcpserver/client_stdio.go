package mcpserver

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// DefaultStdioInitTimeout is the default timeout for stdio client initialization.
// This covers the time needed to start the subprocess and complete the MCP handshake.
const DefaultStdioInitTimeout = 10 * time.Second

// StdioClient implements the MCPClient interface using stdio transport.
// It manages a local subprocess that communicates via stdin/stdout.
type StdioClient struct {
	baseMCPClient
	command string
	args    []string
	env     map[string]string
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
	// If no timeout in context, add a reasonable default
	initCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		initCtx, cancel = context.WithTimeout(ctx, DefaultStdioInitTimeout)
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
