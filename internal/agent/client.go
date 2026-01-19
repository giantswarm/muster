package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// TransportType defines the transport type for MCP connections.
// It determines how the client communicates with the MCP server.
type TransportType string

const (
	// TransportSSE enables real-time bidirectional communication with notification support.
	// This transport maintains a persistent connection and provides immediate updates
	// when server capabilities change. Best for interactive use and monitoring.
	TransportSSE TransportType = "sse"

	// TransportStreamableHTTP uses request-response pattern for environments that don't support SSE.
	// This transport doesn't maintain persistent connections or provide real-time notifications.
	// Best for CLI scripts, automation, and restricted network environments.
	TransportStreamableHTTP TransportType = "streamable-http"
)

// ServerInfo contains information about the connected MCP server.
// This is populated during the MCP protocol handshake.
type ServerInfo struct {
	// Name is the server's name as reported during initialization
	Name string
	// Version is the server's version as reported during initialization
	Version string
}

// Client represents an MCP client that provides comprehensive Model Context Protocol support.
// It handles protocol communication, connection management, caching, and notification processing.
// The client supports multiple transport types (SSE and Streamable HTTP) and can operate
// in various modes: agent monitoring, interactive REPL, programmatic CLI, and MCP server backend.
//
// Key features:
//   - Protocol handshake and session management
//   - Tool, resource, and prompt operations with caching
//   - Real-time notification handling (SSE transport)
//   - Thread-safe concurrent operations
//   - Configurable timeouts and error handling
//   - Multiple output formats (text, JSON)
//   - OAuth 2.1 authentication support with Bearer token
type Client struct {
	endpoint         string
	transport        TransportType
	logger           *Logger
	client           client.MCPClient
	serverInfo       *ServerInfo // Stores server info from initialization
	toolCache        []mcp.Tool
	resourceCache    []mcp.Resource
	promptCache      []mcp.Prompt
	mu               sync.RWMutex
	timeout          time.Duration
	cacheEnabled     bool
	formatters       *Formatters
	NotificationChan chan mcp.JSONRPCNotification
	headers          map[string]string // Custom HTTP headers (e.g., Authorization)
}

// NewClient creates a new MCP client with the specified endpoint, logger, and transport type.
//
// Args:
//   - endpoint: The MCP server endpoint URL (e.g., "http://localhost:8090/sse")
//   - logger: Logger instance for structured logging, or nil to disable logging
//   - transport: Transport type (TransportSSE or TransportStreamableHTTP)
//
// The client is created with default settings:
//   - 30-second timeout for operations
//   - Caching enabled for tools, resources, and prompts
//   - 10-item notification channel buffer
//
// Example:
//
//	logger := agent.NewLogger(true, true, false)
//	client := agent.NewClient("http://localhost:8090/sse", logger, agent.TransportSSE)
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
		headers:          make(map[string]string),
	}
}

// SetAuthorizationHeader sets the Authorization header for authenticated requests.
// This is used for OAuth 2.1 Bearer token authentication.
//
// Example:
//
//	client.SetAuthorizationHeader("Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9...")
func (c *Client) SetAuthorizationHeader(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.headers["Authorization"] = token
}

// SetHeader sets a custom HTTP header for requests.
func (c *Client) SetHeader(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.headers == nil {
		c.headers = make(map[string]string)
	}
	c.headers[key] = value
}

// ClearAuthorizationHeader removes the Authorization header.
func (c *Client) ClearAuthorizationHeader() {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.headers, "Authorization")
}

// GetHeaders returns a copy of the current headers.
func (c *Client) GetHeaders() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	headers := make(map[string]string)
	for k, v := range c.headers {
		headers[k] = v
	}
	return headers
}

// Run executes the complete agent workflow for monitoring mode.
// This method establishes connection, performs initialization, loads initial data,
// and then enters a monitoring loop to handle real-time notifications.
//
// The workflow consists of:
//  1. Connect to the MCP aggregator using the configured transport
//  2. Perform MCP protocol handshake
//  3. Load initial tools, resources, and prompts into cache
//  4. Enter notification listening loop (SSE transport only)
//  5. Handle capability change notifications and update caches
//
// For SSE transport, the method will block until the context is cancelled,
// continuously monitoring for server notifications. For Streamable HTTP transport,
// the method returns immediately after initial data loading since notifications
// are not supported.
//
// Use Connect() instead for programmatic CLI usage without monitoring.
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

// createAndConnectClient creates and connects an MCP client based on transport type.
// It applies any configured headers (e.g., Authorization for OAuth).
func (c *Client) createAndConnectClient(ctx context.Context) (client.MCPClient, error) {
	if c.transport != TransportSSE && c.transport != TransportStreamableHTTP {
		return nil, fmt.Errorf("unsupported transport type: %s", c.transport)
	}

	// Get headers with lock held
	c.mu.RLock()
	headers := make(map[string]string)
	for k, v := range c.headers {
		headers[k] = v
	}
	c.mu.RUnlock()

	var mcpClient client.MCPClient
	switch c.transport {
	case TransportSSE:
		// Build SSE client options
		var sseOpts []transport.ClientOption
		if len(headers) > 0 {
			sseOpts = append(sseOpts, transport.WithHeaders(headers))
		}

		sseClient, err := client.NewSSEMCPClient(c.endpoint, sseOpts...)
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
		// Build StreamableHTTP client options
		var httpOpts []transport.StreamableHTTPCOption
		if len(headers) > 0 {
			httpOpts = append(httpOpts, transport.WithHTTPHeaders(headers))
		}

		httpClient, err := client.NewStreamableHttpClient(c.endpoint, httpOpts...)
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

// Connect establishes a connection to the MCP aggregator for programmatic CLI usage.
// Unlike Run(), this method performs only the connection and initialization steps
// without entering the monitoring loop, making it suitable for scripting and automation.
//
// The method performs:
//   - Transport-specific client creation and connection
//   - MCP protocol handshake and session initialization
//   - Connection validation and error handling
//
// After successful connection, the client is ready for tool execution via
// CallTool, CallToolSimple, CallToolJSON methods, as well as resource and prompt operations.
//
// Example:
//
//	client := agent.NewClient("http://localhost:8090/sse", nil, agent.TransportSSE)
//	defer client.Close()
//	if err := client.Connect(ctx); err != nil {
//	    return fmt.Errorf("connection failed: %w", err)
//	}
//	// Now ready for operations
//	result, err := client.CallToolSimple(ctx, "core_service_list", nil)
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
// for agent mode operation. This method combines protocol initialization with initial
// cache population for tools, resources, and prompts.
//
// The method performs these steps in sequence:
//  1. MCP protocol handshake and session initialization
//  2. Initial tool listing and cache population
//  3. Initial resource listing and cache population
//  4. Initial prompt listing and cache population
//
// This method is typically used by Run() for agent mode operation, but can also
// be called directly when you need both connection and initial cache loading.
//
// Use Connect() instead if you only need connection without cache pre-loading.
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

// initialize performs the MCP protocol handshake with the server.
// This is a critical step that establishes the communication protocol version,
// exchanges capability information, and sets up the session for subsequent operations.
//
// The handshake includes:
//   - Protocol version negotiation (currently "2024-11-05")
//   - Client capability advertisement
//   - Client identification (muster-agent or muster-cli)
//   - Server capability discovery
//
// This method is called automatically by Connect() and Run() methods.
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
					// Set client name based on usage context
					if c.logger != nil {
						return "muster-agent" // Interactive/monitoring mode
					}
					return "muster-cli" // Programmatic/headless mode
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

	// Create timeout context to prevent hanging on slow servers
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

	// Store server info from the handshake response
	c.mu.Lock()
	c.serverInfo = &ServerInfo{
		Name:    result.ServerInfo.Name,
		Version: result.ServerInfo.Version,
	}
	c.mu.Unlock()

	// Log response only if logger is available
	if c.logger != nil {
		c.logger.Response("initialize", result)
	}

	return nil
}

// listTools retrieves all available tools from the MCP server and updates the cache.
// This method handles both initial loading and refresh scenarios, with intelligent
// diff tracking for notification-driven updates.
//
// Args:
//   - ctx: Context for cancellation and timeout control
//   - initial: Whether this is the first time loading tools (affects diff display)
//
// The method performs the following operations:
//   - Sends tools/list request to the MCP server
//   - Updates the local tool cache (if caching is enabled)
//   - Displays differences from the previous cache state (if not initial)
//   - Handles errors with appropriate logging
//
// This method is called automatically during initialization and when tool
// change notifications are received.
func (c *Client) listTools(ctx context.Context, initial bool) error {
	req := mcp.ListToolsRequest{}

	// Log request only if logger is available
	if c.logger != nil {
		c.logger.Request("tools/list", req.Params)
	}

	// Create timeout context to prevent hanging operations
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
		// Compare with cache if not initial to show what changed
		if !initial {
			c.mu.RLock()
			oldTools := c.toolCache
			c.mu.RUnlock()

			// Update cache with new data
			c.mu.Lock()
			c.toolCache = result.Tools
			c.mu.Unlock()

			// Show differences only if logger is available
			if c.logger != nil {
				c.showToolDiff(oldTools, result.Tools)
			}
		} else {
			// Initial load - just populate the cache
			c.mu.Lock()
			c.toolCache = result.Tools
			c.mu.Unlock()
		}
	}

	return nil
}

// listResources retrieves all available resources from the MCP server and updates the cache.
// This method handles both initial loading and refresh scenarios, with intelligent
// diff tracking for notification-driven updates.
//
// Args:
//   - ctx: Context for cancellation and timeout control
//   - initial: Whether this is the first time loading resources (affects diff display)
//
// The method performs the following operations:
//   - Sends resources/list request to the MCP server
//   - Updates the local resource cache (if caching is enabled)
//   - Displays differences from the previous cache state (if not initial)
//   - Handles errors with appropriate logging
//
// This method is called automatically during initialization and when resource
// change notifications are received.
func (c *Client) listResources(ctx context.Context, initial bool) error {
	req := mcp.ListResourcesRequest{}

	// Log request only if logger is available
	if c.logger != nil {
		c.logger.Request("resources/list", req.Params)
	}

	// Create timeout context to prevent hanging operations
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
		// Compare with cache if not initial to show what changed
		if !initial {
			c.mu.RLock()
			oldResources := c.resourceCache
			c.mu.RUnlock()

			// Update cache with new data
			c.mu.Lock()
			c.resourceCache = result.Resources
			c.mu.Unlock()

			// Show differences only if logger is available
			if c.logger != nil {
				c.showResourceDiff(oldResources, result.Resources)
			}
		} else {
			// Initial load - just populate the cache
			c.mu.Lock()
			c.resourceCache = result.Resources
			c.mu.Unlock()
		}
	}

	return nil
}

// listPrompts retrieves all available prompts from the MCP server and updates the cache.
// This method handles both initial loading and refresh scenarios, with intelligent
// diff tracking for notification-driven updates.
//
// Args:
//   - ctx: Context for cancellation and timeout control
//   - initial: Whether this is the first time loading prompts (affects diff display)
//
// The method performs the following operations:
//   - Sends prompts/list request to the MCP server
//   - Updates the local prompt cache (if caching is enabled)
//   - Displays differences from the previous cache state (if not initial)
//   - Handles errors with appropriate logging
//
// This method is called automatically during initialization and when prompt
// change notifications are received.
func (c *Client) listPrompts(ctx context.Context, initial bool) error {
	req := mcp.ListPromptsRequest{}

	// Log request only if logger is available
	if c.logger != nil {
		c.logger.Request("prompts/list", req.Params)
	}

	// Create timeout context to prevent hanging operations
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
		// Compare with cache if not initial to show what changed
		if !initial {
			c.mu.RLock()
			oldPrompts := c.promptCache
			c.mu.RUnlock()

			// Update cache with new data
			c.mu.Lock()
			c.promptCache = result.Prompts
			c.mu.Unlock()

			// Show differences only if logger is available
			if c.logger != nil {
				c.showPromptDiff(oldPrompts, result.Prompts)
			}
		} else {
			// Initial load - just populate the cache
			c.mu.Lock()
			c.promptCache = result.Prompts
			c.mu.Unlock()
		}
	}

	return nil
}

// CallTool executes a tool on the MCP server with the provided arguments.
// This is the core method for tool execution, handling request construction,
// timeout management, and error propagation.
//
// Args:
//   - ctx: Context for cancellation and timeout control
//   - name: The exact name of the tool to execute
//   - args: Tool arguments as a map of arg names to values
//
// Returns:
//   - CallToolResult: Complete tool execution result including content and metadata
//   - error: Any execution or communication errors
//
// The method performs connection validation, constructs the proper MCP request,
// applies timeout controls, and returns the raw result for further processing.
// Use CallToolSimple() or CallToolJSON() for more convenient result handling.
//
// Example:
//
//	result, err := client.CallTool(ctx, "core_service_list", map[string]interface{}{
//	    "namespace": "default",
//	})
//	if err != nil {
//	    return fmt.Errorf("tool execution failed: %w", err)
//	}
func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	// Ensure client is connected before attempting tool execution
	if c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Construct the MCP tool call request
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

	// Create timeout context to prevent hanging tool executions
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Send request
	result, err := c.client.CallTool(timeoutCtx, req)
	if err != nil {
		return nil, fmt.Errorf("tool call failed: %w", err)
	}

	return result, nil
}

// CallToolSimple executes a tool and returns the first text content as a string.
// This is a convenience method that handles the most common use case of tool
// execution where you expect a simple text response.
//
// Args:
//   - ctx: Context for cancellation and timeout control
//   - name: The exact name of the tool to execute
//   - args: Tool arguments as a map of arg names to values
//
// Returns:
//   - string: The first text content from the tool result, or empty string if no text content
//   - error: Tool execution errors or tool-reported errors
//
// The method automatically handles:
//   - Tool execution errors (network, timeout, etc.)
//   - Tool-reported errors (IsError flag in result)
//   - Content extraction (returns first text content found)
//   - Empty result handling
//
// Use CallTool() for full result access or CallToolJSON() for structured data.
//
// Example:
//
//	result, err := client.CallToolSimple(ctx, "core_service_list", nil)
//	if err != nil {
//	    return fmt.Errorf("failed to list services: %w", err)
//	}
//	fmt.Println("Services:", result)
func (c *Client) CallToolSimple(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	result, err := c.CallTool(ctx, name, args)
	if err != nil {
		return "", err
	}

	// Handle tool-reported errors
	if result.IsError {
		var errorMsgs []string
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				errorMsgs = append(errorMsgs, textContent.Text)
			}
		}
		return "", fmt.Errorf("tool error: %s", fmt.Sprintf("%v", errorMsgs))
	}

	// Extract text content from result
	var output []string
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			output = append(output, textContent.Text)
		}
	}

	// Return first text content or empty string
	if len(output) == 0 {
		return "", nil
	}

	return output[0], nil
}

// CallToolJSON executes a tool and returns the result as parsed JSON.
// This is a convenience method for tools that return structured data.
// If the result is not valid JSON, it returns the text content as-is.
//
// Args:
//   - ctx: Context for cancellation and timeout control
//   - name: The exact name of the tool to execute
//   - args: Tool arguments as a map of arg names to values
//
// Returns:
//   - interface{}: Parsed JSON data structure, or string if not valid JSON
//   - error: Tool execution errors or parsing errors
//
// The method handles JSON parsing gracefully - if the tool result is not
// valid JSON, it returns the raw text instead of failing. This makes it
// suitable for tools that may return either structured or unstructured data.
//
// Example:
//
//	result, err := client.CallToolJSON(ctx, "core_service_get", map[string]interface{}{
//	    "name": "web-app",
//	})
//	if err != nil {
//	    return err
//	}
//	// result is now a parsed JSON structure
//	serviceData := result.(map[string]interface{})
func (c *Client) CallToolJSON(ctx context.Context, name string, args map[string]interface{}) (interface{}, error) {
	textResult, err := c.CallToolSimple(ctx, name, args)
	if err != nil {
		return nil, err
	}

	// Attempt to parse as JSON
	var jsonResult interface{}
	if err := json.Unmarshal([]byte(textResult), &jsonResult); err != nil {
		// If it's not JSON, return the text as-is
		return textResult, nil
	}

	return jsonResult, nil
}

// GetResource reads a resource from the MCP server and returns its content.
// Resources are identified by URI and can contain various types of content
// including text, binary data, or structured information.
//
// Args:
//   - ctx: Context for cancellation and timeout control
//   - uri: The resource URI to retrieve (e.g., "file://config.yaml", "memory://cache/data")
//
// Returns:
//   - ReadResourceResult: Complete resource data including content and metadata
//   - error: Any retrieval or communication errors
//
// The method handles:
//   - Connection validation
//   - Request logging (if logger available)
//   - Timeout management
//   - Error handling and logging
//
// Resource content can be accessed through the Contents field, which may
// contain multiple content items with different MIME types.
//
// Example:
//
//	resource, err := client.GetResource(ctx, "file://config.yaml")
//	if err != nil {
//	    return fmt.Errorf("failed to read config: %w", err)
//	}
//	for _, content := range resource.Contents {
//	    if textContent, ok := mcp.AsTextResourceContents(content); ok {
//	        fmt.Println("Config:", textContent.Text)
//	    }
//	}
func (c *Client) GetResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	// Ensure client is connected before attempting resource access
	if c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Construct the MCP resource read request
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

	// Create timeout context to prevent hanging resource operations
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

// GetPrompt retrieves a prompt template from the MCP server and executes it with the given arguments.
// Prompts are template-based text generation tools that can be argeterized for different contexts.
//
// Args:
//   - ctx: Context for cancellation and timeout control
//   - name: The exact name of the prompt to retrieve
//   - args: Template arguments as a map of arg names to string values
//
// Returns:
//   - GetPromptResult: Complete prompt result including generated messages and metadata
//   - error: Any retrieval, templating, or communication errors
//
// The method handles:
//   - Connection validation
//   - Template argument processing
//   - Request logging with prompt name context
//   - Timeout management
//   - Error handling and logging
//
// Prompt results contain a Messages field with generated content that can include
// text, images, or other media types depending on the prompt implementation.
//
// Example:
//
//	result, err := client.GetPrompt(ctx, "code_review", map[string]string{
//	    "language": "go",
//	    "style":    "google",
//	    "file":     "client.go",
//	})
//	if err != nil {
//	    return fmt.Errorf("failed to get prompt: %w", err)
//	}
//	for _, message := range result.Messages {
//	    fmt.Printf("Role: %s, Content: %v\n", message.Role, message.Content)
//	}
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	// Ensure client is connected before attempting prompt access
	if c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Construct the MCP prompt request
	req := mcp.GetPromptRequest{
		Params: struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments,omitempty"`
		}{
			Name:      name,
			Arguments: args,
		},
	}

	// Log request with prompt name context only if logger is available
	if c.logger != nil {
		c.logger.Request(fmt.Sprintf("prompts/get (%s)", name), req.Params)
	}

	// Create timeout context to prevent hanging prompt operations
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

	// Log response with prompt name context only if logger is available
	if c.logger != nil {
		c.logger.Response(fmt.Sprintf("prompts/get (%s)", name), result)
	}

	return result, nil
}

// Close closes the MCP client connection and cleans up resources.
// This method should be called when the client is no longer needed to
// ensure proper cleanup of network connections and background goroutines.
//
// It's safe to call Close multiple times; subsequent calls are no-ops.
//
// Example:
//
//	client := agent.NewClient(endpoint, logger, transport)
//	defer client.Close()
func (c *Client) Close() error {
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
	return nil
}

// NotificationHandler defines a function type for handling MCP notifications.
// It receives JSON-RPC notifications from the MCP server and can be used
// to implement custom notification processing logic.
//
// The handler is called asynchronously when notifications are received,
// typically for events like tool list changes, resource updates, or
// server status changes.
//
// Example:
//
//	handler := func(notification mcp.JSONRPCNotification) {
//	    fmt.Printf("Received notification: %s\n", notification.Method)
//	}
type NotificationHandler func(notification mcp.JSONRPCNotification)

// Command interface implementations for Client

// GetToolCache returns a copy of the currently cached tools.
// This method is thread-safe and returns the tools that were last retrieved
// from the MCP server. The cache is automatically updated when notifications
// are received (for SSE transport) or can be manually refreshed by calling
// tool listing operations.
//
// Returns an empty slice if no tools have been cached yet or if caching is disabled.
func (c *Client) GetToolCache() []mcp.Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.toolCache
}

// GetResourceCache returns a copy of the currently cached resources.
// This method is thread-safe and returns the resources that were last retrieved
// from the MCP server. The cache is automatically updated when notifications
// are received (for SSE transport) or can be manually refreshed by calling
// resource listing operations.
//
// Returns an empty slice if no resources have been cached yet or if caching is disabled.
func (c *Client) GetResourceCache() []mcp.Resource {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resourceCache
}

// GetPromptCache returns a copy of the currently cached prompts.
// This method is thread-safe and returns the prompts that were last retrieved
// from the MCP server. The cache is automatically updated when notifications
// are received (for SSE transport) or can be manually refreshed by calling
// prompt listing operations.
//
// Returns an empty slice if no prompts have been cached yet or if caching is disabled.
func (c *Client) GetPromptCache() []mcp.Prompt {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.promptCache
}

// GetServerInfo returns the server information obtained during MCP initialization.
// This includes the server's name and version as reported by the server.
//
// Returns nil if the client has not been initialized yet.
//
// Example:
//
//	client := agent.NewClient(endpoint, nil, agent.TransportStreamableHTTP)
//	if err := client.Connect(ctx); err != nil {
//	    return err
//	}
//	defer client.Close()
//	info := client.GetServerInfo()
//	fmt.Printf("Connected to %s version %s\n", info.Name, info.Version)
func (c *Client) GetServerInfo() *ServerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// GetFormatters returns the formatters instance used by this client.
// The formatters provide consistent formatting utilities for MCP data,
// supporting both human-readable console output and structured JSON responses.
//
// This method is primarily used by the command system to access formatting
// capabilities for tools, resources, and prompts.
func (c *Client) GetFormatters() interface{} {
	return c.formatters
}

// SupportsNotifications returns whether the current transport supports real-time notifications.
// SSE transport supports notifications for real-time capability updates, while
// Streamable HTTP transport does not support notifications and operates in
// request-response mode only.
//
// This method is used by the REPL and command system to determine whether
// to enable notification-dependent features like real-time updates and
// change monitoring.
func (c *Client) SupportsNotifications() bool {
	return c.transport == TransportSSE || c.transport == TransportStreamableHTTP
}

// SetCacheEnabled enables or disables client-side caching of tools, resources, and prompts.
// When caching is disabled, every operation will fetch fresh data from the server,
// which is useful for testing scenarios or when you need always-current data.
//
// Args:
//   - enabled: Whether to enable caching (true) or disable it (false)
//
// Disabling caching also disables diff tracking and change notifications since
// there's no previous state to compare against. This affects the behavior of
// notification handling in agent mode.
//
// Example:
//
//	client := agent.NewClient(endpoint, logger, transport)
//	client.SetCacheEnabled(false) // Disable caching for testing
//	defer client.Close()
func (c *Client) SetCacheEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cacheEnabled = enabled
}

// SetTimeout configures the timeout duration for MCP operations.
// This timeout applies to all network operations including tool calls,
// resource retrieval, prompt execution, and capability listing.
//
// Args:
//   - timeout: The timeout duration for operations (e.g., 30*time.Second)
//
// The default timeout is 30 seconds. Setting a shorter timeout can help
// with responsive UX but may cause failures with slow operations. Setting
// a longer timeout is useful for complex tools or slow networks.
//
// Example:
//
//	client := agent.NewClient(endpoint, logger, transport)
//	client.SetTimeout(60 * time.Second) // 1 minute timeout for slow operations
//	defer client.Close()
func (c *Client) SetTimeout(timeout time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeout = timeout
}

// SetTimeoutForComplexOperations sets a longer timeout specifically for complex operations
// like workflow execution that may take longer than the default timeout.
func (c *Client) SetTimeoutForComplexOperations() {
	c.SetTimeout(120 * time.Second) // 2 minutes for complex operations
}

// CallToolWithTimeout executes a tool with a custom timeout
func (c *Client) CallToolWithTimeout(ctx context.Context, name string, args map[string]interface{}, timeout time.Duration) (*mcp.CallToolResult, error) {
	// Ensure client is connected before attempting tool execution
	if c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Construct the MCP tool call request
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

	// Create timeout context with custom timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Send request
	result, err := c.client.CallTool(timeoutCtx, req)
	if err != nil {
		return nil, fmt.Errorf("tool call failed: %w", err)
	}

	return result, nil
}

// RefreshToolCache forces a refresh of the tool cache from the MCP server.
func (c *Client) RefreshToolCache(ctx context.Context) error {
	// Calling listTools with initial=false will trigger a cache refresh
	// and log the differences if a logger is configured.
	return c.listTools(ctx, false)
}

// RefreshResourceCache forces a refresh of the resource cache from the MCP server.
func (c *Client) RefreshResourceCache(ctx context.Context) error {
	return c.listResources(ctx, false)
}

// RefreshPromptCache forces a refresh of the prompt cache from the MCP server.
func (c *Client) RefreshPromptCache(ctx context.Context) error {
	return c.listPrompts(ctx, false)
}

// ListToolsFromServer retrieves all tools from the MCP server using the native MCP protocol.
// This method refreshes the cache and returns the complete list of tools.
//
// Args:
//   - ctx: Context for cancellation and timeout control
//
// Returns:
//   - []mcp.Tool: Slice of all available tools from the server
//   - error: Any connection or retrieval errors
func (c *Client) ListToolsFromServer(ctx context.Context) ([]mcp.Tool, error) {
	if err := c.listTools(ctx, false); err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	c.mu.RLock()
	tools := make([]mcp.Tool, len(c.toolCache))
	copy(tools, c.toolCache)
	c.mu.RUnlock()

	return tools, nil
}

// ListResourcesFromServer retrieves all resources from the MCP server using the native MCP protocol.
// This method refreshes the cache and returns the complete list of resources.
//
// Args:
//   - ctx: Context for cancellation and timeout control
//
// Returns:
//   - []mcp.Resource: Slice of all available resources from the server
//   - error: Any connection or retrieval errors
func (c *Client) ListResourcesFromServer(ctx context.Context) ([]mcp.Resource, error) {
	if err := c.listResources(ctx, false); err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	c.mu.RLock()
	resources := make([]mcp.Resource, len(c.resourceCache))
	copy(resources, c.resourceCache)
	c.mu.RUnlock()

	return resources, nil
}

// ListPromptsFromServer retrieves all prompts from the MCP server using the native MCP protocol.
// This method refreshes the cache and returns the complete list of prompts.
//
// Args:
//   - ctx: Context for cancellation and timeout control
//
// Returns:
//   - []mcp.Prompt: Slice of all available prompts from the server
//   - error: Any connection or retrieval errors
func (c *Client) ListPromptsFromServer(ctx context.Context) ([]mcp.Prompt, error) {
	if err := c.listPrompts(ctx, false); err != nil {
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}

	c.mu.RLock()
	prompts := make([]mcp.Prompt, len(c.promptCache))
	copy(prompts, c.promptCache)
	c.mu.RUnlock()

	return prompts, nil
}

// GetToolByName finds a specific tool by name from the cached tool list.
// This method does not refresh the cache; call ListToolsFromServer first if you need fresh data.
//
// Args:
//   - name: The exact name of the tool to find
//
// Returns:
//   - *mcp.Tool: Pointer to the found tool, or nil if not found
func (c *Client) GetToolByName(name string) *mcp.Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, tool := range c.toolCache {
		if tool.Name == name {
			return &tool
		}
	}
	return nil
}

// GetResourceByURI finds a specific resource by URI from the cached resource list.
// This method does not refresh the cache; call ListResourcesFromServer first if you need fresh data.
//
// Args:
//   - uri: The exact URI of the resource to find
//
// Returns:
//   - *mcp.Resource: Pointer to the found resource, or nil if not found
func (c *Client) GetResourceByURI(uri string) *mcp.Resource {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, resource := range c.resourceCache {
		if resource.URI == uri {
			return &resource
		}
	}
	return nil
}

// GetPromptByName finds a specific prompt by name from the cached prompt list.
// This method does not refresh the cache; call ListPromptsFromServer first if you need fresh data.
//
// Args:
//   - name: The exact name of the prompt to find
//
// Returns:
//   - *mcp.Prompt: Pointer to the found prompt, or nil if not found
func (c *Client) GetPromptByName(name string) *mcp.Prompt {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, prompt := range c.promptCache {
		if prompt.Name == name {
			return &prompt
		}
	}
	return nil
}

// authStatusResponse mirrors the auth://status resource structure for parsing.
type authStatusResponse struct {
	Servers []authServerStatus `json:"servers"`
}

type authServerStatus struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Issuer   string `json:"issuer,omitempty"`
	Scope    string `json:"scope,omitempty"`
	AuthTool string `json:"auth_tool,omitempty"`
}

// AuthRequiredInfo contains information about a server requiring authentication.
type AuthRequiredInfo struct {
	Server   string
	Issuer   string
	Scope    string
	AuthTool string
}

// GetAuthRequired fetches the auth://status resource and returns a list of servers
// requiring authentication. This method is used by the agent to detect which
// remote MCP servers need OAuth authentication.
//
// Returns an empty slice if no servers require authentication or if the resource
// cannot be fetched.
func (c *Client) GetAuthRequired() []AuthRequiredInfo {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	resource, err := c.GetResource(ctx, "auth://status")
	if err != nil {
		// Silently return empty list - auth status is best-effort
		return []AuthRequiredInfo{}
	}

	// Parse the response content
	if len(resource.Contents) == 0 {
		return []AuthRequiredInfo{}
	}

	var responseText string
	for _, content := range resource.Contents {
		if textContent, ok := mcp.AsTextResourceContents(content); ok {
			responseText = textContent.Text
			break
		}
	}

	if responseText == "" {
		return []AuthRequiredInfo{}
	}

	var status authStatusResponse
	if err := json.Unmarshal([]byte(responseText), &status); err != nil {
		return []AuthRequiredInfo{}
	}

	// Build the auth required list
	var authRequired []AuthRequiredInfo
	for _, srv := range status.Servers {
		if srv.Status == "auth_required" {
			authRequired = append(authRequired, AuthRequiredInfo{
				Server:   srv.Name,
				Issuer:   srv.Issuer,
				Scope:    srv.Scope,
				AuthTool: srv.AuthTool,
			})
		}
	}

	return authRequired
}

func (c *Client) showToolDiff(oldTools, newTools []mcp.Tool) {
	oldMap := make(map[string]mcp.Tool)
	for _, tool := range oldTools {
		oldMap[tool.Name] = tool
	}

	newMap := make(map[string]mcp.Tool)
	for _, tool := range newTools {
		newMap[tool.Name] = tool
	}

	var added, removed []string

	for name := range newMap {
		if _, exists := oldMap[name]; !exists {
			added = append(added, name)
		}
	}

	for name := range oldMap {
		if _, exists := newMap[name]; !exists {
			removed = append(removed, name)
		}
	}

	if len(added) == 0 && len(removed) == 0 {
		return // Silently return if no changes
	}

	c.logger.Info("Tool changes detected:")
	for _, name := range added {
		c.logger.Success("+ Added: %s", name)
	}
	for _, name := range removed {
		c.logger.Error("- Removed: %s", name)
	}
}

func (c *Client) showResourceDiff(oldResources, newResources []mcp.Resource) {
	oldMap := make(map[string]mcp.Resource)
	for _, res := range oldResources {
		oldMap[res.URI] = res
	}

	newMap := make(map[string]mcp.Resource)
	for _, res := range newResources {
		newMap[res.URI] = res
	}

	var added, removed []string

	for uri := range newMap {
		if _, exists := oldMap[uri]; !exists {
			added = append(added, uri)
		}
	}

	for uri := range oldMap {
		if _, exists := newMap[uri]; !exists {
			removed = append(removed, uri)
		}
	}

	if len(added) == 0 && len(removed) == 0 {
		return // Silently return if no changes
	}

	c.logger.Info("Resource changes detected:")
	for _, uri := range added {
		c.logger.Success("+ Added: %s", uri)
	}
	for _, uri := range removed {
		c.logger.Error("- Removed: %s", uri)
	}
}

func (c *Client) showPromptDiff(oldPrompts, newPrompts []mcp.Prompt) {
	oldMap := make(map[string]mcp.Prompt)
	for _, p := range oldPrompts {
		oldMap[p.Name] = p
	}

	newMap := make(map[string]mcp.Prompt)
	for _, p := range newPrompts {
		newMap[p.Name] = p
	}

	var added, removed []string

	for name := range newMap {
		if _, exists := oldMap[name]; !exists {
			added = append(added, name)
		}
	}

	for name := range oldMap {
		if _, exists := newMap[name]; !exists {
			removed = append(removed, name)
		}
	}

	if len(added) == 0 && len(removed) == 0 {
		return // Silently return if no changes
	}

	c.logger.Info("Prompt changes detected:")
	for _, name := range added {
		c.logger.Success("+ Added: %s", name)
	}
	for _, name := range removed {
		c.logger.Error("- Removed: %s", name)
	}
}
