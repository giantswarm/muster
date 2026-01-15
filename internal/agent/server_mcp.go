package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"muster/internal/agent/oauth"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// MCPServer wraps the agent functionality and exposes it as MCP tools for AI assistants.
// It acts as a bridge between AI assistants and the muster aggregator, enabling
// programmatic access to all MCP capabilities through the standard MCP protocol.
//
// The server exposes comprehensive tool operations including:
//   - Listing and describing tools, resources, and prompts
//   - Executing tools with argument validation
//   - Retrieving resource contents and prompt templates
//   - Advanced filtering and search capabilities
//   - Core tool identification and categorization
//
// Key features:
//   - Stdio transport for AI assistant integration
//   - JSON-formatted responses for structured data consumption
//   - Error handling with detailed error messages
//   - Optional client notification support
//   - Tool availability caching and refresh
//   - Automatic re-authentication when tokens expire
//   - Proactive auth status notification in tool responses (ADR-008)
type MCPServer struct {
	client        *Client
	logger        *Logger
	mcpServer     *server.MCPServer
	notifyClients bool

	// Auth support for re-authentication
	authManager  *oauth.AuthManager
	authMu       sync.Mutex
	endpoint     string
	reauthInProg bool

	// Auth status polling for proactive auth notifications (ADR-008)
	authPoller *authPoller
}

// NewMCPServer creates a new MCP server that exposes agent functionality as MCP tools.
// This enables AI assistants to interact with muster through the standard MCP protocol
// using stdio transport.
//
// Args:
//   - client: MCP client for aggregator communication
//   - logger: Logger instance for structured logging
//   - notifyClients: Whether to enable client notifications for tool changes
//
// The server is initialized with:
//   - Complete tool registry for agent operations
//   - Stdio transport for AI assistant integration
//   - Tool, resource, and prompt capabilities
//   - Optional notification support for dynamic updates
//
// Exposed tools include: list_tools, describe_tool, call_tool, get_resource,
// get_prompt, filter_tools, list_core_tools, and more.
//
// Example:
//
//	client := agent.NewClient("http://localhost:8090/sse", logger, agent.TransportSSE)
//	server, err := agent.NewMCPServer(client, logger, false)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if err := server.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
func NewMCPServer(client *Client, logger *Logger, notifyClients bool) (*MCPServer, error) {
	// Create MCP server
	mcpServer := server.NewMCPServer(
		"muster-agent",
		"1.0.0",
		server.WithToolCapabilities(notifyClients),
		server.WithResourceCapabilities(false, false),
		server.WithPromptCapabilities(false),
	)

	ms := &MCPServer{
		client:        client,
		logger:        logger,
		mcpServer:     mcpServer,
		notifyClients: notifyClients,
		authPoller:    newAuthPoller(client, logger),
	}

	// Register all tools
	ms.registerTools()

	return ms, nil
}

// Start starts the MCP server using stdio transport for AI assistant integration.
// This method blocks until the server is terminated, handling MCP protocol
// communication over stdin/stdout. It's designed to be used as the main
// entry point when running as an MCP server for AI assistants.
//
// The server will continue running until the context is cancelled or
// the stdio connection is closed by the client.
func (m *MCPServer) Start(ctx context.Context) error {
	// Start the auth status poller in background (ADR-008)
	go m.authPoller.Start(ctx)

	// Start the stdio server
	return server.ServeStdio(m.mcpServer)
}

// SetAuthManager sets the auth manager for re-authentication support.
// When set, the server can automatically trigger browser-based re-authentication
// when tokens expire during operations.
func (m *MCPServer) SetAuthManager(authManager *oauth.AuthManager, endpoint string) {
	m.authMu.Lock()
	defer m.authMu.Unlock()
	m.authManager = authManager
	m.endpoint = endpoint
}

// reauthTimeout is the maximum time to wait for re-authentication to complete.
const reauthTimeout = 5 * time.Minute

// handleTokenExpiredError handles a token expiration error by triggering re-authentication.
// It clears the expired token, starts a new OAuth flow, and opens the browser.
// Returns a user-friendly error message with the auth URL.
func (m *MCPServer) handleTokenExpiredError(ctx context.Context, originalErr error) *mcp.CallToolResult {
	m.authMu.Lock()

	// If no auth manager is configured, we can't do browser-based re-auth.
	// This shouldn't happen if the agent was started correctly with OAuth support.
	if m.authManager == nil {
		m.authMu.Unlock()
		return mcp.NewToolResultError(fmt.Sprintf(
			"Authentication token expired: %v\n\n"+
				"Re-authentication is not available (auth manager not configured).\n"+
				"This may happen if the muster server didn't require authentication at startup.\n"+
				"To fix: restart the muster agent in Cursor (Cmd/Ctrl+Shift+P -> 'Reload Window').",
			originalErr,
		))
	}

	endpoint := m.endpoint

	// Prevent concurrent re-auth attempts
	if m.reauthInProg {
		m.authMu.Unlock()
		return mcp.NewToolResultError(
			"Re-authentication is already in progress.\n" +
				"Please complete the sign-in in your browser, then retry your request.",
		)
	}
	m.reauthInProg = true
	// Note: reauthInProg is reset by waitForReauthCompletion when auth completes or times out

	// Clear the expired token
	if err := m.authManager.ClearToken(); err != nil {
		if m.logger != nil {
			m.logger.Error("Failed to clear expired token: %v", err)
		}
	}

	// Re-check connection to get the auth challenge
	authState, err := m.authManager.CheckConnection(ctx, endpoint)
	if err != nil || authState != oauth.AuthStatePendingAuth {
		m.reauthInProg = false
		m.authMu.Unlock()
		return mcp.NewToolResultError(fmt.Sprintf(
			"Authentication token expired but could not contact the server to start re-authentication.\n\n"+
				"Error: %v\n\n"+
				"Please check:\n"+
				"  - Is the muster server running at %s?\n"+
				"  - Is your network connection working?\n\n"+
				"If the problem persists, restart the muster agent in Cursor.",
			err, endpoint,
		))
	}

	// Start the OAuth flow
	authURL, err := m.authManager.StartAuthFlow(ctx)
	if err != nil {
		m.reauthInProg = false
		m.authMu.Unlock()
		return mcp.NewToolResultError(fmt.Sprintf(
			"Authentication token expired but could not start the OAuth flow.\n\n"+
				"Error: %v\n\n"+
				"This might happen if:\n"+
				"  - Port 3000 is already in use (OAuth callback port)\n"+
				"  - The authorization server is not reachable\n\n"+
				"Try: restart the muster agent in Cursor (Cmd/Ctrl+Shift+P -> 'Reload Window').",
			err,
		))
	}

	// Try to open the browser automatically
	browserOpened := false
	if err := oauth.OpenBrowser(authURL); err == nil {
		browserOpened = true
		if m.logger != nil {
			m.logger.Info("Opened browser for re-authentication")
		}
	} else {
		if m.logger != nil {
			m.logger.Error("Failed to open browser: %v", err)
		}
	}

	m.authMu.Unlock()

	// Start waiting for auth completion in background with its own context and timeout.
	// We use a background context because the request context may be cancelled when
	// the handler returns, but we need the re-auth flow to complete independently.
	go m.waitForReauthCompletion()

	// Return a user-friendly message
	if browserOpened {
		return mcp.NewToolResultError(fmt.Sprintf(
			"Authentication token expired. Your browser has been opened for re-authentication.\n\n"+
				"If the browser did not open, please visit:\n%s\n\n"+
				"After signing in, retry your request.",
			authURL,
		))
	}

	return mcp.NewToolResultError(fmt.Sprintf(
		"Authentication token expired. Please authenticate by visiting:\n%s\n\n"+
			"After signing in, retry your request.",
		authURL,
	))
}

// waitForReauthCompletion waits for re-authentication to complete and updates the client.
// It uses its own context with a timeout to ensure the re-auth flow can complete
// independently of the original request context.
func (m *MCPServer) waitForReauthCompletion() {
	// Always reset reauthInProg when done, regardless of success or failure
	defer func() {
		m.authMu.Lock()
		m.reauthInProg = false
		m.authMu.Unlock()
	}()

	if m.authManager == nil {
		return
	}

	// Create a new context with timeout for the re-auth wait
	ctx, cancel := context.WithTimeout(context.Background(), reauthTimeout)
	defer cancel()

	err := m.authManager.WaitForAuth(ctx)
	if err != nil {
		if m.logger != nil {
			m.logger.Error("Re-authentication failed: %v", err)
		}
		return
	}

	// Get the new bearer token and update the client
	bearerToken, err := m.authManager.GetBearerToken()
	if err != nil {
		if m.logger != nil {
			m.logger.Error("Failed to get bearer token after re-auth: %v", err)
		}
		return
	}

	m.client.SetAuthorizationHeader(bearerToken)

	if m.logger != nil {
		m.logger.Success("Re-authentication successful! Token updated.")
	}
}

// checkAndHandleTokenExpiration checks if an error is a token expiration error
// and handles it appropriately. Returns the error result if it was a token error,
// or nil if it wasn't.
func (m *MCPServer) checkAndHandleTokenExpiration(ctx context.Context, err error) *mcp.CallToolResult {
	if err == nil {
		return nil
	}

	if oauth.IsTokenExpiredError(err) {
		return m.handleTokenExpiredError(ctx, err)
	}

	return nil
}

// registerTools registers all MCP tools
func (m *MCPServer) registerTools() {
	// List tools
	listToolsTool := mcp.NewTool("list_tools",
		mcp.WithDescription("List all available tools from connected MCP servers"),
	)
	m.mcpServer.AddTool(listToolsTool, m.handleListTools)

	// List resources
	listResourcesTool := mcp.NewTool("list_resources",
		mcp.WithDescription("List all available resources from connected MCP servers"),
	)
	m.mcpServer.AddTool(listResourcesTool, m.handleListResources)

	// List prompts
	listPromptsTool := mcp.NewTool("list_prompts",
		mcp.WithDescription("List all available prompts from connected MCP servers"),
	)
	m.mcpServer.AddTool(listPromptsTool, m.handleListPrompts)

	// Describe tool
	describeToolTool := mcp.NewTool("describe_tool",
		mcp.WithDescription("Get detailed information about a specific tool"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Name of the tool to describe"),
		),
	)
	m.mcpServer.AddTool(describeToolTool, m.handleDescribeTool)

	// Describe resource
	describeResourceTool := mcp.NewTool("describe_resource",
		mcp.WithDescription("Get detailed information about a specific resource"),
		mcp.WithString("uri",
			mcp.Required(),
			mcp.Description("URI of the resource to describe"),
		),
	)
	m.mcpServer.AddTool(describeResourceTool, m.handleDescribeResource)

	// Describe prompt
	describePromptTool := mcp.NewTool("describe_prompt",
		mcp.WithDescription("Get detailed information about a specific prompt"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Name of the prompt to describe"),
		),
	)
	m.mcpServer.AddTool(describePromptTool, m.handleDescribePrompt)

	// Call tool
	callToolTool := mcp.NewTool("call_tool",
		mcp.WithDescription("Execute a tool with the given arguments"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Name of the tool to call"),
		),
		mcp.WithObject("arguments",
			mcp.Description("Arguments to pass to the tool (as JSON object)"),
		),
	)
	m.mcpServer.AddTool(callToolTool, m.handleCallTool)

	// Get resource
	getResourceTool := mcp.NewTool("get_resource",
		mcp.WithDescription("Retrieve the contents of a resource"),
		mcp.WithString("uri",
			mcp.Required(),
			mcp.Description("URI of the resource to retrieve"),
		),
	)
	m.mcpServer.AddTool(getResourceTool, m.handleGetResource)

	// Get prompt
	getPromptTool := mcp.NewTool("get_prompt",
		mcp.WithDescription("Get a prompt with the given arguments"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Name of the prompt to get"),
		),
		mcp.WithObject("arguments",
			mcp.Description("Arguments to pass to the prompt (as JSON object with string values)"),
		),
	)
	m.mcpServer.AddTool(getPromptTool, m.handleGetPrompt)

	// List core tools
	listCoreToolsTool := mcp.NewTool("list_core_tools",
		mcp.WithDescription("List core muster tools (built-in functionality separate from external MCP servers)"),
		mcp.WithBoolean("include_schema",
			mcp.Description("Whether to include full tool specifications with input schemas (default: true)"),
		),
	)
	m.mcpServer.AddTool(listCoreToolsTool, m.handleListCoreTools)

	// Filter tools
	filterToolsTool := mcp.NewTool("filter_tools",
		mcp.WithDescription("Filter available tools based on name patterns or descriptions with full specifications"),
		mcp.WithString("pattern",
			mcp.Description("Pattern to match against tool names (supports wildcards like *)"),
		),
		mcp.WithString("description_filter",
			mcp.Description("Filter by description content (case-insensitive substring match)"),
		),
		mcp.WithBoolean("case_sensitive",
			mcp.Description("Whether pattern matching should be case-sensitive (default: false)"),
		),
		mcp.WithBoolean("include_schema",
			mcp.Description("Whether to include full tool specifications with input schemas (default: true)"),
		),
	)
	m.mcpServer.AddTool(filterToolsTool, m.handleFilterTools)
}
