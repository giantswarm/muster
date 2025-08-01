package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"muster/internal/agent"
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

	// Connect to aggregator and load tools/resources/prompts with retry logic
	err := connectWithRetry(ctx, client, logger, endpoint, transport)
	if err != nil {
		return err
	}
	defer client.Close()

	// Run in different modes
	if agentMCPServer {
		// MCP Server mode
		server, err := agent.NewMCPServer(client, logger, false)
		if err != nil {
			return fmt.Errorf("failed to create MCP server: %w", err)
		}

		logger.Info("Starting muster agent MCP server (stdio transport)...")

		if err := server.Start(ctx); err != nil {
			return fmt.Errorf("MCP server error: %w", err)
		}
		return nil
	} else if agentREPL {
		// REPL mode - let REPL handle its own connection and logging
		repl := agent.NewREPL(client, logger)
		if err := repl.Run(ctx); err != nil {
			return fmt.Errorf("REPL error: %w", err)
		}
		return nil
	} else {
		// Normal agent mode
		for {
			select {
			case <-ctx.Done():
				return nil
			}
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
