package agent

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/agent/oauth"
	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/metatools"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"

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

	go m.relayNotifications(ctx, m.notifyAssistant)

	// Start the stdio server
	return server.ServeStdio(m.mcpServer)
}

// notifyAssistant sends a notification to the assistant on the other side of
// stdio.
func (m *MCPServer) notifyAssistant(method string) {
	m.mcpServer.SendNotificationToAllClients(method, nil)
}

// relayNotifications reads the aggregator's notifications for as long as ctx
// lasts and passes on what matters: the client's caches follow a
// list_changed, and with notifyClients set the assistant receives the same
// notifications/tools/list_changed (resources and prompts alike) through
// notify, so a server the person signed in to or a backend that connected
// shows up in its next list_tools. Nothing else reads the client's
// NotificationChan in this mode.
func (m *MCPServer) relayNotifications(ctx context.Context, notify func(method string)) {
	for {
		select {
		case <-ctx.Done():
			return
		case notification := <-m.client.NotificationChan:
			if err := m.client.handleNotification(ctx, notification); err != nil && m.logger != nil {
				m.logger.Debug("Notification %s: %v", notification.Method, err)
			}
			if m.notifyClients && listChanged[notification.Method] {
				notify(notification.Method)
			}
		}
	}
}

// listChanged names the aggregator's notifications the bridge relays to the
// assistant: its lists of tools, resources and prompts changed.
var listChanged = map[string]bool{
	mcp.MethodNotificationToolsListChanged:     true,
	mcp.MethodNotificationResourcesListChanged: true,
	mcp.MethodNotificationPromptsListChanged:   true,
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

	// Probe the server for its auth challenge without removing the stored
	// token: the new flow's token exchange replaces it, an abandoned flow
	// leaves it in place for the other muster processes sharing the store.
	if err := m.authManager.RequireAuth(ctx, endpoint); err != nil {
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
				"  - The OAuth callback port is already in use\n"+
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
	go m.waitForReauthCompletion() //nolint:gosec

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

	// After re-auth, the token is stored in the file-based token store.
	// The mcp-go OAuth transport reads from the AgentTokenStore on each request,
	// so subsequent requests will automatically use the new token.
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

	if pkgoauth.IsOAuthUnauthorizedError(err) {
		return m.handleTokenExpiredError(ctx, err)
	}

	return nil
}

// registerTools registers all MCP meta-tools.
//
// IMPORTANT: This is a transport bridge implementation (Issue #344).
// All meta-tool handlers forward directly to the server's meta-tools.
// The server (aggregator) is the source of truth for all tool logic.
//
// Handler flow:
//  1. Extract arguments from MCP request
//  2. Forward to server via client.CallTool(ctx, "<meta-tool-name>", args)
//  3. Handle OAuth errors for re-authentication
//  4. Apply auth status wrapper (ADR-008)
//  5. Return result to AI client
func (m *MCPServer) registerTools() {
	// Delegate to shared implementation to avoid duplication with server_upgrade.go
	registerAgentTools(m)
}

// forwardToServerMetaTool creates a handler that forwards the call to a server meta-tool.
// This implements the transport bridge pattern (Issue #344) where the agent acts as a
// thin proxy between the AI client (stdio) and the server (HTTP).
//
// The handler:
//  1. Extracts arguments from the MCP request
//  2. Forwards to the server by calling the corresponding meta-tool
//  3. Handles OAuth token expiration with re-authentication flow
//  4. Wraps the result with auth status (ADR-008)
func (m *MCPServer) forwardToServerMetaTool(metaToolName string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := maps.Clone(request.GetArguments())
		if args == nil {
			args = map[string]any{}
		}

		// Forward to server's meta-tool. A call_tool request may carry the
		// bridge's own timeout argument, which bounds this one call instead of
		// the client's call timeout and never reaches the aggregator.
		call := m.client.CallTool
		if metaToolName == metatools.ToolCallTool {
			timeout, err := takeCallTimeout(args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if timeout > 0 {
				call = func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
					return m.client.CallToolWithTimeout(ctx, name, args, timeout)
				}
			}
		}
		result, err := call(ctx, metaToolName, args)
		if err != nil {
			// Handle OAuth token expiration
			if tokenResult := m.checkAndHandleTokenExpiration(ctx, err); tokenResult != nil {
				return tokenResult, nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("Meta-tool execution failed: %v", err)), nil
		}

		// Wrap result with auth status (ADR-008)
		return m.wrapToolResultWithAuth(result), nil
	}
}

// callTimeoutArg is the one argument of call_tool the bridge owns: seconds to
// wait for this call before giving up, in place of the agent's --timeout. It
// is removed from the arguments before they are forwarded to the aggregator.
var callTimeoutArg = api.ArgMetadata{
	Name:        "timeout",
	Type:        api.ArgTypeNumber,
	Description: "Seconds to wait for this call before giving up; overrides the agent's --timeout for this call only",
}

// takeCallTimeout removes the bridge's timeout argument from a call_tool
// request's arguments and returns it as a duration; zero when it is absent.
func takeCallTimeout(args map[string]any) (time.Duration, error) {
	raw, ok := args[callTimeoutArg.Name]
	if !ok {
		return 0, nil
	}
	delete(args, callTimeoutArg.Name)

	seconds, ok := raw.(float64)
	if !ok || seconds <= 0 {
		return 0, fmt.Errorf("%s must be a positive number of seconds, got %v", callTimeoutArg.Name, raw)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}
