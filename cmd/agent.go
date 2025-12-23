package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"muster/internal/agent"
	"muster/internal/agent/oauth"
	"muster/internal/cli"
	"muster/internal/config"

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
		logger.Info("Auth check failed: %v, attempting direct connection", err)
		return runMCPServerDirect(ctx, client, logger, endpoint, transport)
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
				// Can't determine auth requirements, try direct connection
				return runMCPServerDirect(ctx, client, logger, endpoint, transport)
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
		return runMCPServerDirect(ctx, client, logger, endpoint, transport)
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
