package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// TransportType defines the transport type for MCP connections
type TransportType string

const (
	TransportSSE            TransportType = "sse"
	TransportStreamableHTTP TransportType = "streamable-http"
)

// Client represents an MCP client that can be used for both agent and CLI operations
type Client struct {
	endpoint         string
	transport        TransportType
	logger           *Logger
	client           client.MCPClient
	toolCache        []mcp.Tool
	resourceCache    []mcp.Resource
	promptCache      []mcp.Prompt
	mu               sync.RWMutex
	timeout          time.Duration
	cacheEnabled     bool
	formatters       *Formatters
	NotificationChan chan mcp.JSONRPCNotification
}

// NewClient creates a new agent client with specified transport
func NewClient(endpoint string, logger *Logger, transport TransportType) *Client {
	return &Client{
		endpoint:         endpoint,
		transport:        transport,
		logger:           logger,
		toolCache:        []mcp.Tool{},
		resourceCache:    []mcp.Resource{},
		promptCache:      []mcp.Prompt{},
		timeout:          30 * time.Second,
		cacheEnabled:     true,
		formatters:       NewFormatters(),
		NotificationChan: make(chan mcp.JSONRPCNotification, 10),
	}
}

// Run executes the agent workflow
func (c *Client) Run(ctx context.Context) error {
	c.logger.Info("Connecting to MCP aggregator at %s using %s transport...", c.endpoint, c.transport)

	// Create and connect MCP client
	mcpClient, err := c.createAndConnectClient(ctx)
	if err != nil {
		return err
	}
	defer mcpClient.Close()

	c.client = mcpClient

	// Initialize session and load initial data
	if err := c.InitializeAndLoadData(ctx); err != nil {
		return err
	}

	// For streamable-http, we just connect and list items, then exit
	if c.transport == TransportStreamableHTTP {
		c.logger.Info("Successfully connected and listed available items. Streamable-HTTP transport doesn't support notifications.")
		return nil
	}

	// Wait for notifications (SSE only)
	c.logger.Info("Waiting for notifications (press Ctrl+C to exit)...")

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Shutting down...")
			return nil

		case notification := <-c.NotificationChan:
			if err := c.handleNotification(ctx, notification); err != nil {
				c.logger.Error("Failed to handle notification: %v", err)
			}
		}
	}
}

// handleNotification processes incoming notifications
func (c *Client) handleNotification(ctx context.Context, notification mcp.JSONRPCNotification) error {
	// Log the notification only if logger is available
	if c.logger != nil {
		c.logger.Notification(notification.Method, notification.Params)
	}

	// Handle specific notifications only if caching is enabled
	if c.cacheEnabled {
		switch notification.Method {
		case "notifications/tools/list_changed":
			return c.listTools(ctx, false)

		case "notifications/resources/list_changed":
			return c.listResources(ctx, false)

		case "notifications/prompts/list_changed":
			return c.listPrompts(ctx, false)

		default:
			// Unknown notification type
		}
	}

	return nil
}

// createAndConnectClient creates and connects an MCP client based on transport type
func (c *Client) createAndConnectClient(ctx context.Context) (client.MCPClient, error) {
	if c.transport != TransportSSE && c.transport != TransportStreamableHTTP {
		return nil, fmt.Errorf("unsupported transport type: %s", c.transport)
	}

	var mcpClient client.MCPClient
	switch c.transport {
	case TransportSSE:
		sseClient, err := client.NewSSEMCPClient(c.endpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to create SSE client: %w", err)
		}

		// Start the transport
		if err := sseClient.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start SSE client: %w", err)
		}

		// Set up notification handler for SSE
		sseClient.OnNotification(func(notification mcp.JSONRPCNotification) {
			select {
			case c.NotificationChan <- notification:
			case <-ctx.Done():
			}
		})

		mcpClient = sseClient

	case TransportStreamableHTTP:
		httpClient, err := client.NewStreamableHttpClient(c.endpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to create streamable-http client: %w", err)
		}

		// Start the transport
		if err := httpClient.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start streamable-http client: %w", err)
		}

		// Set up notification handler for streamable HTTP
		httpClient.OnNotification(func(notification mcp.JSONRPCNotification) {
			select {
			case c.NotificationChan <- notification:
			case <-ctx.Done():
			}
		})

		mcpClient = httpClient
	}

	return mcpClient, nil
}

// Connect establishes connection to the MCP aggregator (CLI-style)
func (c *Client) Connect(ctx context.Context) error {
	// Create and connect MCP client (without notifications for CLI usage)
	mcpClient, err := c.createAndConnectClient(ctx)
	if err != nil {
		return err
	}

	c.client = mcpClient

	// Initialize the session
	if err := c.initialize(ctx); err != nil {
		c.client.Close()
		return fmt.Errorf("initialization failed: %w", err)
	}

	return nil
}

// InitializeAndLoadData performs the standard initialization and data loading sequence
func (c *Client) InitializeAndLoadData(ctx context.Context) error {
	// Initialize the session
	if err := c.initialize(ctx); err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}

	// List tools initially
	if err := c.listTools(ctx, true); err != nil {
		return fmt.Errorf("initial tool listing failed: %w", err)
	}

	// List resources initially
	if err := c.listResources(ctx, true); err != nil {
		return fmt.Errorf("initial resource listing failed: %w", err)
	}

	// List prompts initially
	if err := c.listPrompts(ctx, true); err != nil {
		return fmt.Errorf("initial prompt listing failed: %w", err)
	}

	return nil
}

// initialize performs the MCP protocol handshake
func (c *Client) initialize(ctx context.Context) error {
	req := mcp.InitializeRequest{
		Params: struct {
			ProtocolVersion string                 `json:"protocolVersion"`
			Capabilities    mcp.ClientCapabilities `json:"capabilities"`
			ClientInfo      mcp.Implementation     `json:"clientInfo"`
		}{
			ProtocolVersion: "2024-11-05",
			ClientInfo: mcp.Implementation{
				Name: func() string {
					if c.logger != nil {
						return "muster-agent"
					}
					return "muster-cli"
				}(),
				Version: "1.0.0",
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	}

	// Log request only if logger is available
	if c.logger != nil {
		c.logger.Request("initialize", req.Params)
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Send request
	result, err := c.client.Initialize(timeoutCtx, req)
	if err != nil {
		if c.logger != nil {
			c.logger.Error("Initialize failed: %v", err)
		}
		return err
	}

	// Log response only if logger is available
	if c.logger != nil {
		c.logger.Response("initialize", result)
	}

	return nil
}

// listTools lists all available tools
func (c *Client) listTools(ctx context.Context, initial bool) error {
	req := mcp.ListToolsRequest{}

	// Log request only if logger is available
	if c.logger != nil {
		c.logger.Request("tools/list", req.Params)
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Send request
	result, err := c.client.ListTools(timeoutCtx, req)
	if err != nil {
		if c.logger != nil {
			c.logger.Error("ListTools failed: %v", err)
		}
		return err
	}

	// Log response only if logger is available
	if c.logger != nil {
		c.logger.Response("tools/list", result)
	}

	// Only do caching and diff comparison if caching is enabled
	if c.cacheEnabled {
		// Compare with cache if not initial
		if !initial {
			c.mu.RLock()
			oldTools := c.toolCache
			c.mu.RUnlock()

			c.mu.Lock()
			c.toolCache = result.Tools
			c.mu.Unlock()

			// Show differences only if logger is available
			if c.logger != nil {
				c.showToolDiff(oldTools, result.Tools)
			}
		} else {
			c.mu.Lock()
			c.toolCache = result.Tools
			c.mu.Unlock()
		}
	}

	return nil
}

// listResources lists all available resources
func (c *Client) listResources(ctx context.Context, initial bool) error {
	req := mcp.ListResourcesRequest{}

	// Log request only if logger is available
	if c.logger != nil {
		c.logger.Request("resources/list", req.Params)
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Send request
	result, err := c.client.ListResources(timeoutCtx, req)
	if err != nil {
		if c.logger != nil {
			c.logger.Error("ListResources failed: %v", err)
		}
		return err
	}

	// Log response only if logger is available
	if c.logger != nil {
		c.logger.Response("resources/list", result)
	}

	// Only do caching and diff comparison if caching is enabled
	if c.cacheEnabled {
		// Compare with cache if not initial
		if !initial {
			c.mu.RLock()
			oldResources := c.resourceCache
			c.mu.RUnlock()

			c.mu.Lock()
			c.resourceCache = result.Resources
			c.mu.Unlock()

			// Show differences only if logger is available
			if c.logger != nil {
				c.showResourceDiff(oldResources, result.Resources)
			}
		} else {
			c.mu.Lock()
			c.resourceCache = result.Resources
			c.mu.Unlock()
		}
	}

	return nil
}

// listPrompts lists all available prompts
func (c *Client) listPrompts(ctx context.Context, initial bool) error {
	req := mcp.ListPromptsRequest{}

	// Log request only if logger is available
	if c.logger != nil {
		c.logger.Request("prompts/list", req.Params)
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Send request
	result, err := c.client.ListPrompts(timeoutCtx, req)
	if err != nil {
		if c.logger != nil {
			c.logger.Error("ListPrompts failed: %v", err)
		}
		return err
	}

	// Log response only if logger is available
	if c.logger != nil {
		c.logger.Response("prompts/list", result)
	}

	// Only do caching and diff comparison if caching is enabled
	if c.cacheEnabled {
		// Compare with cache if not initial
		if !initial {
			c.mu.RLock()
			oldPrompts := c.promptCache
			c.mu.RUnlock()

			c.mu.Lock()
			c.promptCache = result.Prompts
			c.mu.Unlock()

			// Show differences only if logger is available
			if c.logger != nil {
				c.showPromptDiff(oldPrompts, result.Prompts)
			}
		} else {
			c.mu.Lock()
			c.promptCache = result.Prompts
			c.mu.Unlock()
		}
	}

	return nil
}

// CallTool executes a tool and returns the result
func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	req := mcp.CallToolRequest{
		Params: struct {
			Name      string    `json:"name"`
			Arguments any       `json:"arguments,omitempty"`
			Meta      *mcp.Meta `json:"_meta,omitempty"`
		}{
			Name:      name,
			Arguments: args,
		},
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Send request
	result, err := c.client.CallTool(timeoutCtx, req)
	if err != nil {
		return nil, fmt.Errorf("tool call failed: %w", err)
	}

	return result, nil
}

// CallToolSimple executes a tool and returns the text content as a string
func (c *Client) CallToolSimple(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	result, err := c.CallTool(ctx, name, args)
	if err != nil {
		return "", err
	}

	if result.IsError {
		var errorMsgs []string
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				errorMsgs = append(errorMsgs, textContent.Text)
			}
		}
		return "", fmt.Errorf("tool error: %s", fmt.Sprintf("%v", errorMsgs))
	}

	var output []string
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			output = append(output, textContent.Text)
		}
	}

	if len(output) == 0 {
		return "", nil
	}

	return output[0], nil
}

// CallToolJSON executes a tool and returns the result as parsed JSON
func (c *Client) CallToolJSON(ctx context.Context, name string, args map[string]interface{}) (interface{}, error) {
	textResult, err := c.CallToolSimple(ctx, name, args)
	if err != nil {
		return nil, err
	}

	var jsonResult interface{}
	if err := json.Unmarshal([]byte(textResult), &jsonResult); err != nil {
		// If it's not JSON, return the text as-is
		return textResult, nil
	}

	return jsonResult, nil
}

// GetResource reads a resource and returns its content
func (c *Client) GetResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	req := mcp.ReadResourceRequest{
		Params: struct {
			URI       string         `json:"uri"`
			Arguments map[string]any `json:"arguments,omitempty"`
		}{
			URI: uri,
		},
	}

	// Log request only if logger is available
	if c.logger != nil {
		c.logger.Request("resources/read", req.Params)
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Send request
	result, err := c.client.ReadResource(timeoutCtx, req)
	if err != nil {
		if c.logger != nil {
			c.logger.Error("ReadResource failed: %v", err)
		}
		return nil, err
	}

	// Log response only if logger is available
	if c.logger != nil {
		c.logger.Response("resources/read", result)
	}

	return result, nil
}

// GetPrompt retrieves a prompt with the given arguments
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	req := mcp.GetPromptRequest{
		Params: struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments,omitempty"`
		}{
			Name:      name,
			Arguments: args,
		},
	}

	// Log request only if logger is available
	if c.logger != nil {
		c.logger.Request(fmt.Sprintf("prompts/get (%s)", name), req.Params)
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Send request
	result, err := c.client.GetPrompt(timeoutCtx, req)
	if err != nil {
		if c.logger != nil {
			c.logger.Error("GetPrompt failed: %v", err)
		}
		return nil, err
	}

	// Log response only if logger is available
	if c.logger != nil {
		c.logger.Response(fmt.Sprintf("prompts/get (%s)", name), result)
	}

	return result, nil
}

// Close closes the connection
func (c *Client) Close() error {
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
	return nil
}

// PrettyJSON pretty-prints JSON for logging
func PrettyJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func prettyJSON(v interface{}) string {
	return PrettyJSON(v)
}

type NotificationHandler func(notification mcp.JSONRPCNotification)

// Command interface implementations for Client

// GetToolCache returns the tool cache for commands
func (c *Client) GetToolCache() []mcp.Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.toolCache
}

// GetResourceCache returns the resource cache for commands
func (c *Client) GetResourceCache() []mcp.Resource {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resourceCache
}

// GetPromptCache returns the prompt cache for commands
func (c *Client) GetPromptCache() []mcp.Prompt {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.promptCache
}

// GetFormatters returns the formatters for commands
func (c *Client) GetFormatters() interface{} {
	return c.formatters
}

// SupportsNotifications returns whether the transport supports notifications
func (c *Client) SupportsNotifications() bool {
	return c.transport == TransportSSE || c.transport == TransportStreamableHTTP
}
