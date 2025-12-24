package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"muster/internal/agent"
	"muster/internal/agent/oauth"
	"muster/internal/cli"
	"muster/internal/config"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"
)

var (
	agentEndpoint   string
	agentTimeout    time.Duration
	agentVerbose    bool
	agentNoColor    bool
	agentJSONRPC    bool
	agentREPL       bool
	agentMCPServer  bool
	agentTransport  string
	agentConfigPath string
	agentAutoSSO    bool
)

// agentCmd represents the agent command
var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "MCP Client for the muster aggregator server",
	Long: `The agent command connects to the MCP aggregator as a client agent,
logs all JSON-RPC communication, and demonstrates dynamic tool updates.

This is useful for connecting the aggregator's behavior, filtering
tools, and ensuring that the agent can execute tools.

The agent command can run in three modes:
1. Normal mode (default): Connects, lists tools, and waits for notifications
2. REPL mode (--repl): Provides an interactive interface to explore and execute tools
3. MCP Server mode (--mcp-server): Runs an MCP server that exposes REPL functionality via stdio

Transport options:
- streamable-http (default): Fast HTTP-based transport with notification support, compatible with muster serve
- sse: Server-Sent Events transport with real-time notification support

In REPL mode, you can:
- List available tools, resources, and prompts
- Get detailed information about specific items
- Execute tools interactively with JSON arguments
- View resources and retrieve their contents
- Execute prompts with arguments
- Toggle notification display

In MCP Server mode:
- The agent command acts as an MCP server using stdio transport
- It exposes all REPL functionality as MCP tools
- It's designed for integration with AI assistants like Claude or Cursor
- Configure it in your AI assistant's MCP settings

By default, it connects to the aggregator endpoint configured in your
muster configuration file. You can override this with the --endpoint flag.

Note: The aggregator server must be running (use 'muster serve') before using this command.`,
	RunE: runAgent,
}

func init() {
	rootCmd.AddCommand(agentCmd)

	// Add flags
	agentCmd.Flags().StringVar(&agentEndpoint, "endpoint", "", "Aggregator MCP endpoint URL (default: from config)")
	agentCmd.Flags().DurationVar(&agentTimeout, "timeout", 5*time.Minute, "Timeout for waiting for notifications")
	agentCmd.Flags().BoolVar(&agentVerbose, "verbose", false, "Enable verbose logging (show keepalive messages)")
	agentCmd.Flags().BoolVar(&agentNoColor, "no-color", false, "Disable colored output")
	agentCmd.Flags().BoolVar(&agentJSONRPC, "json-rpc", false, "Enable full JSON-RPC message logging")
	agentCmd.Flags().BoolVar(&agentREPL, "repl", false, "Start interactive REPL mode")
	agentCmd.Flags().BoolVar(&agentMCPServer, "mcp-server", false, "Run as MCP server (stdio transport)")
	agentCmd.Flags().StringVar(&agentTransport, "transport", string(agent.TransportStreamableHTTP), "Transport to use (streamable-http, sse)")
	agentCmd.Flags().StringVar(&agentConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
	agentCmd.Flags().BoolVar(&agentAutoSSO, "auto-sso", false, "Automatically authenticate with remote MCP servers after Muster auth (opens browser tabs)")

	// Mark flags as mutually exclusive
	agentCmd.MarkFlagsMutuallyExclusive("repl", "mcp-server")
}

func runAgent(cmd *cobra.Command, args []string) error {
	// Create context with signal handling
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	var logger *agent.Logger
	if agentMCPServer {
		// no logging for mpc servers in stdio
		logger = agent.NewDevNullLogger()
	} else {
		logger = agent.NewLogger(agentVerbose, !agentNoColor, agentJSONRPC)
	}

	// Handle interrupts gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		if !agentMCPServer {
			logger.Info("\nReceived interrupt signal, shutting down gracefully...")
		}
		cancel()
	}()

	// Determine endpoint using the same logic as CLI commands
	endpoint := agentEndpoint
	if endpoint == "" {
		// Use the same endpoint detection logic as CLI commands
		config, err := config.LoadConfig(agentConfigPath)
		endpoint = cli.GetAggregatorEndpoint(&config)
		if err != nil {
			// Use fallback default that matches system defaults
			if !agentMCPServer && agentVerbose {
				logger.Info("Warning: Could not detect endpoint (%v), using default: %s\n", err, endpoint)
			}
		}
	}

	// Parse transport type
	var transport agent.TransportType
	switch agentTransport {
	case "sse":
		transport = agent.TransportSSE
	case "streamable-http":
		transport = agent.TransportStreamableHTTP
	default:
		return fmt.Errorf("unsupported transport: %s (supported: streamable-http, sse)", agentTransport)
	}

	// Create agent client
	client := agent.NewClient(endpoint, logger, transport)

	// For MCP Server mode, check if authentication is required first
	if agentMCPServer {
		return runMCPServerWithOAuth(ctx, client, logger, endpoint, transport)
	}

	// Connect to aggregator and load tools/resources/prompts with retry logic
	err := connectWithRetry(ctx, client, logger, endpoint, transport)
	if err != nil {
		return err
	}
	defer client.Close()

	// Run in different modes
	if agentREPL {
		// REPL mode - let REPL handle its own connection and logging
		repl := agent.NewREPL(client, logger)
		if err := repl.Run(ctx); err != nil {
			return fmt.Errorf("REPL error: %w", err)
		}
		return nil
	}

	// Normal agent mode - wait for context cancellation
	<-ctx.Done()
	return nil
}

// runMCPServerWithOAuth runs the MCP server with OAuth authentication support.
// If the server requires authentication, it starts with a pending auth server
// exposing only the authenticate_muster tool, then upgrades to the full server
// after authentication completes.
func runMCPServerWithOAuth(ctx context.Context, client *agent.Client, logger *agent.Logger, endpoint string, transport agent.TransportType) error {
	// First, check if the server requires authentication
	authManager, err := oauth.NewAuthManager(oauth.AuthManagerConfig{
		CallbackPort: 3000,
		FileMode:     true, // Persist tokens to filesystem
	})
	if err != nil {
		return fmt.Errorf("failed to create auth manager: %w", err)
	}
	defer authManager.Close()

	// Check connection and detect 401
	authState, err := authManager.CheckConnection(ctx, endpoint)
	if err != nil && authState != oauth.AuthStatePendingAuth {
		// Error that's not auth-related, try regular connection
		// Still pass auth manager for potential re-auth if token expires later
		logger.Info("Auth check failed: %v, attempting direct connection with re-auth support", err)
		return runMCPServerDirectWithAuth(ctx, client, logger, endpoint, transport, authManager)
	}

	switch authState {
	case oauth.AuthStateAuthenticated:
		// Already have a valid token, use it
		bearerToken, err := authManager.GetBearerToken()
		if err != nil {
			// Token might have expired between check and now
			// Clear the invalid token and fall through to pending auth
			logger.Info("Token expired or invalid, clearing and re-authenticating")
			_ = authManager.ClearToken()

			// Re-check to get the auth challenge
			authState, err = authManager.CheckConnection(ctx, endpoint)
			if err != nil || authState != oauth.AuthStatePendingAuth {
				// Can't determine auth requirements, try direct connection with re-auth support
				return runMCPServerDirectWithAuth(ctx, client, logger, endpoint, transport, authManager)
			}
			return runMCPServerPendingAuth(ctx, client, logger, endpoint, transport, authManager)
		}
		client.SetAuthorizationHeader(bearerToken)
		// Pass auth manager for re-authentication support when token expires mid-session
		return runMCPServerDirectWithAuth(ctx, client, logger, endpoint, transport, authManager)

	case oauth.AuthStatePendingAuth:
		// Need to authenticate - start pending auth MCP server
		return runMCPServerPendingAuth(ctx, client, logger, endpoint, transport, authManager)

	default:
		// No auth required or unknown state, try direct connection
		// Still pass auth manager for potential re-auth if server starts requiring auth later
		return runMCPServerDirectWithAuth(ctx, client, logger, endpoint, transport, authManager)
	}
}

// runMCPServerDirect runs the MCP server with a direct connection (no auth required).
func runMCPServerDirect(ctx context.Context, client *agent.Client, logger *agent.Logger, endpoint string, transport agent.TransportType) error {
	return runMCPServerDirectWithAuth(ctx, client, logger, endpoint, transport, nil)
}

// runMCPServerDirectWithAuth runs the MCP server with optional auth manager for re-auth support.
func runMCPServerDirectWithAuth(ctx context.Context, client *agent.Client, logger *agent.Logger, endpoint string, transport agent.TransportType, authManager *oauth.AuthManager) error {
	// Connect with retry
	if err := connectWithRetry(ctx, client, logger, endpoint, transport); err != nil {
		// Check if this is a 401 error - if so, the cached token is invalid
		// and we need to fall back to the pending auth flow
		if authManager != nil && oauth.IsTokenExpiredError(err) {
			logger.Info("Connection failed with 401 - cached token is invalid, clearing and re-authenticating")
			_ = authManager.ClearToken()

			// Re-check connection to get the auth challenge
			authState, checkErr := authManager.CheckConnection(ctx, endpoint)
			if checkErr == nil && authState == oauth.AuthStatePendingAuth {
				// Fall back to pending auth flow
				return runMCPServerPendingAuth(ctx, client, logger, endpoint, transport, authManager)
			}
			// If we can't get auth challenge, return original error
			logger.Info("Could not start re-authentication flow: %v", checkErr)
		}
		return err
	}
	defer client.Close()

	// Create and start MCP server
	server, err := agent.NewMCPServer(client, logger, true) // Enable notifications
	if err != nil {
		return fmt.Errorf("failed to create MCP server: %w", err)
	}

	// Set auth manager for re-authentication support if available
	if authManager != nil {
		server.SetAuthManager(authManager, endpoint)
	}

	logger.Info("Starting muster agent MCP server (stdio transport)...")
	return server.Start(ctx)
}

// runMCPServerPendingAuth runs an MCP server that handles OAuth authentication.
// It starts with a synthetic authenticate_muster tool and upgrades to the full
// tool set after authentication completes.
func runMCPServerPendingAuth(ctx context.Context, client *agent.Client, logger *agent.Logger, endpoint string, transport agent.TransportType, authManager *oauth.AuthManager) error {
	// Create the pending auth MCP server with synthetic authenticate_muster tool
	pendingServer, err := agent.NewPendingAuthMCPServer(logger, authManager, endpoint)
	if err != nil {
		return fmt.Errorf("failed to create pending auth server: %w", err)
	}

	// Create a channel to signal when authentication completes
	authCompleteChan := make(chan struct{})

	// Start a goroutine to monitor auth state and upgrade when complete
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if pendingServer.IsAuthComplete() {
					close(authCompleteChan)
					return
				}
			}
		}
	}()

	// Start another goroutine to upgrade the server when auth completes
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-authCompleteChan:
			// Authentication completed - upgrade to full server
			upgradeToConnectedServer(ctx, client, logger, endpoint, transport, authManager, pendingServer)
		}
	}()

	// Start the pending auth server (blocks until context is cancelled)
	logger.Info("Starting muster agent MCP server in pending auth mode...")
	return pendingServer.Start(ctx)
}

// upgradeToConnectedServer upgrades from pending auth to a fully connected server.
// It connects to the aggregator with the auth token and updates the MCP server's tools.
func upgradeToConnectedServer(ctx context.Context, client *agent.Client, logger *agent.Logger, endpoint string, transport agent.TransportType, authManager *oauth.AuthManager, pendingServer *agent.PendingAuthMCPServer) {
	// Get the bearer token
	bearerToken, err := authManager.GetBearerToken()
	if err != nil {
		logger.Error("Failed to get bearer token after auth: %v", err)
		return
	}

	// Set the authorization header on the client
	client.SetAuthorizationHeader(bearerToken)

	// Connect to the aggregator
	if err := client.Connect(ctx); err != nil {
		logger.Error("Failed to connect after auth: %v", err)
		return
	}

	// Initialize and load data
	if err := client.InitializeAndLoadData(ctx); err != nil {
		logger.Error("Failed to load data after auth: %v", err)
		return
	}

	logger.Success("Connected to Muster Server after authentication")

	// Now upgrade the MCP server by adding real tools and sending notification
	mcpServer := pendingServer.GetMCPServer()
	if mcpServer == nil {
		logger.Error("MCP server is nil, cannot upgrade to full tool set")
		return
	}

	// Remove the synthetic authenticate_muster tool
	mcpServer.DeleteTools("authenticate_muster")

	// Add all the real tools from the connected client
	agent.RegisterClientToolsOnServer(mcpServer, client)

	// Send tools/list_changed notification to inform clients
	mcpServer.SendNotificationToAllClients("notifications/tools/list_changed", nil)

	logger.Info("Upgraded to full tool set - %d tools available", len(client.GetToolCache()))

	// Auto-trigger authentication for any remote MCP servers that require auth
	// This provides a seamless SSO experience when the same IdP is used
	// Only runs if --auto-sso flag is set to avoid unexpected browser tabs
	if agentAutoSSO {
		go triggerPendingRemoteAuth(ctx, client, logger)
	}
}

// failedServer represents a server that failed SSO authentication.
type failedServer struct {
	name string
	url  string
}

// triggerPendingRemoteAuth detects remote MCP servers that require authentication
// and automatically triggers the OAuth flow for each one. Since they typically share
// the same IdP (Dex), the browser session from Muster auth will provide SSO.
func triggerPendingRemoteAuth(ctx context.Context, client *agent.Client, logger *agent.Logger) {
	// Get all tools from the cache (already populated by upgradeToConnectedServer)
	tools := client.GetToolCache()
	if len(tools) == 0 {
		logger.Info("SSO chain skipped: no tools available in cache")
		return
	}

	// Find all authenticate_* tools (excluding authenticate_muster)
	pendingAuthTools := findPendingAuthTools(tools)

	if len(pendingAuthTools) == 0 {
		logger.Info("SSO chain skipped: no remote servers require authentication")
		return
	}

	logger.Info("Found %d remote server(s) requiring authentication, starting SSO chain...", len(pendingAuthTools))

	// Track results for summary
	var successCount, failureCount int
	var failedServers []failedServer

	// Trigger auth for each pending server sequentially
	// Sequential is better for SSO as the browser session builds up
	for i, toolName := range pendingAuthTools {
		select {
		case <-ctx.Done():
			logger.Info("SSO chain cancelled")
			return
		default:
		}

		serverName := strings.TrimPrefix(toolName, "authenticate_")
		logger.Info("[%d/%d] Authenticating with %s...", i+1, len(pendingAuthTools), serverName)

		// Call the authenticate tool to get the auth URL
		result, err := client.CallTool(ctx, toolName, nil)
		if err != nil {
			logger.Error("Failed to call %s: %v", toolName, err)
			failureCount++
			failedServers = append(failedServers, failedServer{name: serverName, url: ""})
			continue
		}

		// Extract the auth URL from the result
		authURL := extractAuthURLFromResult(result)
		if authURL == "" {
			logger.Error("Could not extract auth URL from %s response", toolName)
			failureCount++
			failedServers = append(failedServers, failedServer{name: serverName, url: ""})
			continue
		}

		// Open the browser for this auth flow
		if err := oauth.OpenBrowser(authURL); err != nil {
			logger.Error("Failed to open browser for %s: %v", serverName, err)
			failureCount++
			failedServers = append(failedServers, failedServer{name: serverName, url: authURL})
		} else {
			successCount++
		}

		// Delay between auth flows to allow SSO redirects to complete.
		// With SSO using the same IdP (Dex), authentication typically completes
		// in <1s via automatic redirects. The 2-second delay provides margin
		// for slower networks and prevents browser tab overload.
		if i < len(pendingAuthTools)-1 {
			time.Sleep(2 * time.Second)
		}
	}

	// Log summary with accurate counts
	total := len(pendingAuthTools)
	if failureCount == 0 {
		logger.Success("SSO chain complete - %d/%d servers authenticated", successCount, total)
	} else if successCount > 0 {
		logger.Info("SSO chain finished - %d/%d servers authenticated (%d failed)", successCount, total, failureCount)
	} else {
		logger.Error("SSO chain failed - 0/%d servers authenticated", total)
	}

	// Display failed URLs together for easy manual authentication
	if len(failedServers) > 0 {
		var hasURLs bool
		for _, fs := range failedServers {
			if fs.url != "" {
				if !hasURLs {
					logger.Info("Manual authentication required for the following servers:")
					hasURLs = true
				}
				logger.Info("  %s: %s", fs.name, fs.url)
			}
		}
	}
}

// extractAuthURLFromResult parses the auth URL from a tool call result.
// The result typically contains JSON with an "auth_url" field.
func extractAuthURLFromResult(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}

	// The content is typically a TextContent with JSON
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			// Try to parse as JSON first
			var authResp struct {
				AuthURL string `json:"auth_url"`
			}
			if err := json.Unmarshal([]byte(textContent.Text), &authResp); err == nil && authResp.AuthURL != "" {
				return authResp.AuthURL
			}

			// Fallback: look for URL pattern in the text
			// The response often contains "Please sign in to connect..." with a URL
			lines := strings.Split(textContent.Text, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
					return line
				}
			}
		}
	}

	return ""
}

// findPendingAuthTools finds all authenticate_* tools from the tool list,
// excluding authenticate_muster which is the main muster auth tool.
func findPendingAuthTools(tools []mcp.Tool) []string {
	var pendingAuthTools []string
	for _, tool := range tools {
		if strings.HasPrefix(tool.Name, "authenticate_") && tool.Name != "authenticate_muster" {
			pendingAuthTools = append(pendingAuthTools, tool.Name)
		}
	}
	return pendingAuthTools
}

// connectWithRetry attempts to connect to the aggregator with retry logic
func connectWithRetry(ctx context.Context, client *agent.Client, logger *agent.Logger, endpoint string, transport agent.TransportType) error {
	const maxRetries = 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Don't wait on the first attempt
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
				// Simple 1 second delay between retries
			}
		}

		// Attempt to connect
		logger.Info("Connecting to aggregator at: %s using %s transport (attempt %d/%d)", endpoint, transport, attempt+1, maxRetries)

		err := client.Connect(ctx)
		if err == nil {
			// Connection successful, now try to initialize
			if err := client.InitializeAndLoadData(ctx); err != nil {
				if attempt < maxRetries-1 {
					logger.Info("Initialization failed, retrying: %v", err)
					continue
				}
				return fmt.Errorf("failed to load initial data: %w", err)
			}
			return nil
		}

		// Retry on any error if we haven't exhausted our retries
		if attempt < maxRetries-1 {
			logger.Info("Connection attempt %d failed, retrying: %v", attempt+1, err)
			continue
		}

		// If we've exhausted retries, return the error
		return fmt.Errorf("failed to connect to aggregator: %w", err)
	}

	return fmt.Errorf("failed to connect to aggregator after %d attempts", maxRetries)
}
