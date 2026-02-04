package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/giantswarm/muster/internal/agent"
	"github.com/giantswarm/muster/internal/agent/oauth"
	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/cli"
	"github.com/giantswarm/muster/internal/config"

	"github.com/spf13/cobra"
)

const (
	// DefaultOAuthCallbackPort is the port used for OAuth callback during authentication.
	DefaultOAuthCallbackPort = 3000
)

var (
	agentEndpoint       string
	agentContext        string
	agentTimeout        time.Duration
	agentVerbose        bool
	agentNoColor        bool
	agentJSONRPC        bool
	agentREPL           bool
	agentMCPServer      bool
	agentTransport      string
	agentConfigPath     string
	agentDisableAutoSSO bool
	agentAuthMode       string
	agentSilentAuth     bool
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
	agentCmd.Flags().StringVar(&agentContext, "context", "", "Use a specific context (env: MUSTER_CONTEXT)")
	agentCmd.Flags().DurationVar(&agentTimeout, "timeout", 5*time.Minute, "Timeout for waiting for notifications")
	agentCmd.Flags().BoolVar(&agentVerbose, "verbose", false, "Enable verbose logging (show keepalive messages)")
	agentCmd.Flags().BoolVar(&agentNoColor, "no-color", false, "Disable colored output")
	agentCmd.Flags().BoolVar(&agentJSONRPC, "json-rpc", false, "Enable full JSON-RPC message logging")
	agentCmd.Flags().BoolVar(&agentREPL, "repl", false, "Start interactive REPL mode")
	agentCmd.Flags().BoolVar(&agentMCPServer, "mcp-server", false, "Run as MCP server (stdio transport)")
	agentCmd.Flags().StringVar(&agentTransport, "transport", string(agent.TransportStreamableHTTP), "Transport to use (streamable-http, sse)")
	agentCmd.Flags().StringVar(&agentConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
	agentCmd.Flags().BoolVar(&agentDisableAutoSSO, "disable-auto-sso", false, "Disable automatic authentication with remote MCP servers after Muster auth")
	agentCmd.Flags().StringVar(&agentAuthMode, "auth", "", "Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)")
	agentCmd.Flags().BoolVar(&agentSilentAuth, "silent", false, "Attempt silent re-auth using OIDC prompt=none (requires IdP support, not supported by Dex)")

	// Mark flags as mutually exclusive
	agentCmd.MarkFlagsMutuallyExclusive("repl", "mcp-server")
}

func runAgent(cmd *cobra.Command, args []string) error {
	// Create context with signal handling
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	var logger *agent.Logger
	if agentMCPServer {
		// No logging for MCP servers in stdio mode
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

	// Determine endpoint using context resolution with precedence:
	// 1. --endpoint flag
	// 2. --context flag
	// 3. MUSTER_CONTEXT env var
	// 4. current-context from contexts.yaml
	// 5. config-based fallback
	endpoint, err := cli.ResolveEndpoint(agentEndpoint, agentContext)
	if err != nil {
		return err
	}
	if endpoint == "" {
		// Fall back to config-based resolution
		cfg, err := config.LoadConfig(agentConfigPath)
		endpoint = cli.GetAggregatorEndpoint(&cfg)
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

	// Parse auth mode (uses environment variable as default if not specified)
	authMode, err := cli.GetAuthModeWithOverride(agentAuthMode)
	if err != nil {
		return err
	}

	// For REPL and normal modes, use the AuthHandler for authentication
	if err := setupAgentAuthentication(ctx, client, logger, endpoint, authMode); err != nil {
		return err
	}

	// Connect to aggregator and load tools/resources/prompts with retry logic
	err = connectWithRetry(ctx, client, logger, endpoint, transport)
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

// setupAgentAuthentication sets up authentication for the agent client using the AuthHandler.
// This provides a unified authentication experience for all agent modes (REPL, normal).
func setupAgentAuthentication(ctx context.Context, client *agent.Client, logger *agent.Logger, endpoint string, authMode cli.AuthMode) error {
	// Check if this is a remote endpoint
	if !cli.IsRemoteEndpoint(endpoint) {
		// Local endpoint - no auth needed
		return nil
	}

	// Try to get a token from the AuthHandler
	handler := api.GetAuthHandler()
	if handler == nil {
		// No auth handler - create and register one
		// Silent refresh is disabled by default (Dex doesn't support prompt=none)
		// Use --silent flag to opt-in if your IdP supports it
		adapter, err := cli.NewAuthAdapterWithConfig(cli.AuthAdapterConfig{
			NoSilentRefresh: !agentSilentAuth,
		})
		if err != nil {
			logger.Info("Warning: Could not initialize auth adapter: %v", err)
			return nil
		}
		adapter.Register()
		handler = api.GetAuthHandler()
	}

	if handler == nil {
		return nil
	}

	// Set persistent session ID for MCP server token persistence.
	// This must be set early so the aggregator can associate all requests
	// with the same session, enabling MCP server tools to be visible after
	// authentication via `muster auth login --server <server>`.
	if sessionID := handler.GetSessionID(); sessionID != "" {
		client.SetHeader(api.ClientSessionIDHeader, sessionID)
	}

	// Check if we have a valid token
	if handler.HasValidToken(endpoint) {
		token, err := handler.GetBearerToken(endpoint)
		if err == nil {
			client.SetAuthorizationHeader(token)
			logger.Info("Using existing authentication token")
			return nil
		}
	}

	// Check if auth is required
	authRequired, err := handler.CheckAuthRequired(ctx, endpoint)
	if err != nil {
		// Can't check auth - continue without token
		logger.Info("Could not check auth requirements: %v", err)
		return nil
	}

	if !authRequired {
		// No auth required
		return nil
	}

	// Handle auth based on mode
	switch authMode {
	case cli.AuthModeNone:
		return &cli.AuthRequiredError{Endpoint: endpoint}
	case cli.AuthModePrompt:
		logger.Info("Authentication required for %s", endpoint)
		logger.Info("Press Enter to open browser for authentication, or Ctrl+C to cancel...")
		// Wait for user input
		var input string
		if _, err := fmt.Scanln(&input); err != nil {
			// User pressed Enter (with or without newline)
		}
	default:
		// AuthModeAuto - proceed directly
		logger.Info("Authentication required for %s", endpoint)
	}

	logger.Info("Starting OAuth login flow...")

	if err := handler.Login(ctx, endpoint); err != nil {
		return fmt.Errorf("authentication failed: %w. Run 'muster auth login --endpoint %s' to authenticate", err, endpoint)
	}

	// Get the token and set it on the client
	token, err := handler.GetBearerToken(endpoint)
	if err != nil {
		return fmt.Errorf("failed to get authentication token: %w", err)
	}
	client.SetAuthorizationHeader(token)
	logger.Success("Authentication successful")

	return nil
}

// runMCPServerWithOAuth runs the MCP server with OAuth authentication support.
// If the server requires authentication, it starts with a pending auth server
// exposing only the authenticate_muster tool, then upgrades to the full server
// after authentication completes.
func runMCPServerWithOAuth(ctx context.Context, client *agent.Client, logger *agent.Logger, endpoint string, transport agent.TransportType) error {
	// First, check if the server requires authentication
	authManager, err := oauth.NewAuthManager(oauth.AuthManagerConfig{
		CallbackPort: DefaultOAuthCallbackPort,
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
	// This checks both:
	// 1. OAuth callback completion (via IsAuthComplete)
	// 2. Filesystem tokens from CLI authentication (via HasValidTokenForEndpoint)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Check if auth completed via OAuth callback
				if pendingServer.IsAuthComplete() {
					close(authCompleteChan)
					return
				}
				// Check if a valid token appeared in filesystem (CLI auth)
				if authManager.HasValidTokenForEndpoint(endpoint) {
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
	// Enabled by default, can be disabled with --disable-auto-sso
	if !agentDisableAutoSSO {
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
	// Get servers requiring auth from the auth://status resource
	pendingServers := client.GetAuthRequired()

	if len(pendingServers) == 0 {
		logger.Info("SSO chain skipped: no remote servers require authentication")
		return
	}

	logger.Info("Found %d remote server(s) requiring authentication, starting SSO chain...", len(pendingServers))

	// Track results for summary
	var successCount, failureCount int
	var failedServers []failedServer

	// Trigger auth for each pending server sequentially, waiting for completion
	// This ensures SSO cookies are available for subsequent flows
	for i, srv := range pendingServers {
		select {
		case <-ctx.Done():
			logger.Info("SSO chain cancelled")
			return
		default:
		}

		serverName := srv.Server
		logger.Info("[%d/%d] Authenticating with %s...", i+1, len(pendingServers), serverName)

		// Call core_auth_login with the server name as argument
		result, err := client.CallTool(ctx, "core_auth_login", map[string]interface{}{
			"server": serverName,
		})
		if err != nil {
			logger.Error("Failed to call core_auth_login for %s: %v", serverName, err)
			failureCount++
			failedServers = append(failedServers, failedServer{name: serverName, url: ""})
			continue
		}

		// Extract the auth URL from the result
		authURL := extractAuthURL(result)
		if authURL == "" {
			logger.Error("Could not extract auth URL from core_auth_login response for %s", serverName)
			failureCount++
			failedServers = append(failedServers, failedServer{name: serverName, url: ""})
			continue
		}

		// Open the browser for this auth flow
		if err := oauth.OpenBrowser(authURL); err != nil {
			logger.Error("Failed to open browser for %s: %v", serverName, err)
			failureCount++
			failedServers = append(failedServers, failedServer{name: serverName, url: authURL})
			continue
		}

		// Wait for the server to become connected by polling the auth://status resource
		if err := waitForServerConnection(ctx, client, serverName, logger); err != nil {
			logger.Error("Timeout waiting for %s: %v", serverName, err)
			failureCount++
			failedServers = append(failedServers, failedServer{name: serverName, url: authURL})
		} else {
			logger.Success("%s authenticated", serverName)
			successCount++
		}
	}

	// Log summary with accurate counts
	total := len(pendingServers)
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

// waitForServerConnection polls until the server transitions to "connected" status.
// It uses the agent client to read the auth://status resource and check server state.
func waitForServerConnection(ctx context.Context, client *agent.Client, serverName string, logger *agent.Logger) error {
	const timeout = 2 * time.Minute
	const pollInterval = 500 * time.Millisecond

	logger.Debug("Waiting for %s to connect (timeout: %s)", serverName, timeout)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for %s to authenticate\n\nPlease complete authentication in your browser, then run:\n  muster auth login --server %s", serverName, serverName)
		case <-ticker.C:
			// Check if the server is still in auth_required state using the auth://status resource
			pendingServers := client.GetAuthRequired()
			stillPending := false
			for _, srv := range pendingServers {
				if srv.Server == serverName {
					stillPending = true
					break
				}
			}
			if !stillPending {
				// Server is no longer in auth_required state - it has connected
				logger.Debug("%s connection confirmed", serverName)
				return nil
			}
			// Still waiting - server still requires auth
		}
	}
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
