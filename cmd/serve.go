package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	mcptoolkitmetrics "github.com/giantswarm/mcp-toolkit/metrics"
	"github.com/giantswarm/mcp-toolkit/tracing"

	"github.com/giantswarm/muster/internal/app"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/spf13/cobra"
)

// otelShutdownTimeout bounds how long a deferred OTel Shutdown may
// block. 5s leaves slack inside kubelet's terminationGracePeriodSeconds
// default of 30s for in-flight logs, spans and metric batches to
// drain before SIGKILL.
const otelShutdownTimeout = 5 * time.Second

// debug enables verbose logging across the application.
// This helps troubleshoot connection issues and understand service behavior.
var serveDebug bool

// serveSilent disables console log output (writer → io.Discard). OTLP, if
// configured, is unaffected — that's controlled via OTEL_* env vars.
var serveSilent bool

// yolo disables the denylist for destructive tool calls.
// When enabled, all MCP tools can be executed without restrictions.
var serveYolo bool

// configPath specifies the configuration directory.
// The directory should contain config.yaml and subdirectories: mcpservers/, workflows/
var serveConfigPath string

// OAuth MCP Client/Proxy configuration flags (for authenticating TO remote MCP servers - ADR 004)
var (
	// serveOAuthMCPClientEnabled enables the OAuth MCP client/proxy functionality for remote MCP servers
	serveOAuthMCPClientEnabled bool
	// serveOAuthMCPClientPublicURL is the publicly accessible URL of the Muster Server
	serveOAuthMCPClientPublicURL string
	// serveOAuthMCPClientID is the OAuth client identifier (CIMD URL)
	serveOAuthMCPClientID string
)

// OAuth Server configuration flags (for protecting the Muster Server ITSELF - ADR 005)
var (
	// serveOAuthServerEnabled enables OAuth server protection for the Muster Server
	serveOAuthServerEnabled bool
	// serveOAuthServerBaseURL is the base URL of the Muster Server (for OAuth issuer)
	serveOAuthServerBaseURL string
)

// serveEnableEvents enables Kubernetes event emission (alpha, disabled by default)
var serveEnableEvents bool

// serveExtraCAFile is a PEM file appended to the system trust pool at startup.
var serveExtraCAFile string

// serveCmd defines the serve command structure.
// This is the main command of muster that starts the aggregator server
// and sets up the necessary MCP servers for development.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the muster aggregator server.",
	Long: `Starts the muster aggregator server and manages MCP servers for AI assistant access.

   - Starts configured MCP servers and services in the background.
   - Prints a summary of actions and connection details to the console.

The aggregator server provides a unified MCP interface that other muster commands can connect to.
Use 'muster service', 'muster workflow', etc. to interact with the running server.

To connect to muster in your IDE, you can use the following command:
muster agent --mcp-server

Configuration:
  muster loads configuration from ~/.config/muster by default.

  Use --config-path to specify a custom directory containing all configuration files:
  - config.yaml (main configuration)
  - mcpservers/ (MCP server definitions)
  - workflows/ (workflow definitions)`,
	Args: cobra.NoArgs, // No arguments required
	RunE: runServe,
}

// runServe is the main entry point for the serve command
func runServe(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	level := logging.LevelInfo
	if serveDebug {
		level = logging.LevelDebug
	}
	var output io.Writer = os.Stderr
	if serveSilent {
		output = io.Discard
	}
	shutdownLogging, err := logging.Init(ctx, level, output, "muster", GetVersion())
	if err != nil {
		return fmt.Errorf("init logging: %w", err)
	}
	defer otelShutdown("logging", shutdownLogging)

	shutdownTracing, err := tracing.Init(ctx,
		tracing.WithServiceName("muster"),
		tracing.WithServiceVersion(GetVersion()),
	)
	if err != nil {
		return fmt.Errorf("init tracing: %w", err)
	}
	defer otelShutdown("tracing", shutdownTracing)

	shutdownMeter, err := mcptoolkitmetrics.Init(ctx,
		mcptoolkitmetrics.WithServiceName("muster"),
		mcptoolkitmetrics.WithServiceVersion(GetVersion()),
	)
	if err != nil {
		return fmt.Errorf("init meter: %w", err)
	}
	defer otelShutdown("meter", shutdownMeter)

	// Create application configuration without cluster arguments
	cfg := app.NewConfig(serveDebug, serveYolo, serveConfigPath).
		WithVersion(GetVersion()).
		WithOAuthMCPClient(serveOAuthMCPClientEnabled, serveOAuthMCPClientPublicURL, serveOAuthMCPClientID).
		WithOAuthServer(serveOAuthServerEnabled, serveOAuthServerBaseURL).
		WithEvents(serveEnableEvents).
		WithExtraCAFile(serveExtraCAFile)

	// Create and initialize the application
	application, err := app.NewApplication(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize application: %w", err)
	}

	return application.Run(ctx)
}

// otelShutdown runs an OTel Shutdown function with a bounded fresh
// context so SIGTERM-canceled parent contexts don't prevent in-flight
// logs, spans or metric batches from draining.
func otelShutdown(name string, shutdown func(context.Context) error) {
	sctx, cancel := context.WithTimeout(context.Background(), otelShutdownTimeout)
	defer cancel()
	if err := shutdown(sctx); err != nil {
		slog.Warn("otel shutdown failed", "component", name, "error", err)
	}
}

// init registers the serve command and its flags with the root command.
// This is called automatically when the package is imported.
func init() {
	rootCmd.AddCommand(serveCmd)

	// Register command flags
	serveCmd.Flags().BoolVar(&serveDebug, "debug", false, "Enable general debug logging")
	serveCmd.Flags().BoolVar(&serveSilent, "silent", false, "Disable console log output. Does not silence OTLP — unset OTEL_EXPORTER_OTLP_* or set OTEL_SDK_DISABLED=true for that.")
	serveCmd.Flags().BoolVar(&serveYolo, "yolo", false, "Disable denylist for destructive tool calls (use with caution)")
	serveCmd.Flags().StringVar(&serveConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")

	// OAuth MCP Client/Proxy flags (for authenticating TO remote MCP servers - ADR 004)
	// These configure muster as an OAuth client when connecting to remote MCP servers
	serveCmd.Flags().BoolVar(&serveOAuthMCPClientEnabled, "oauth-mcp-client", false, "Enable OAuth MCP client/proxy for remote MCP server authentication")
	serveCmd.Flags().StringVar(&serveOAuthMCPClientPublicURL, "oauth-mcp-client-public-url", "", "Publicly accessible URL of the Muster Server for OAuth callbacks")
	// Note: When --oauth-mcp-client-id is empty (default), the client ID is auto-derived from publicUrl
	// as {publicUrl}/.well-known/oauth-client.json and muster serves its own CIMD
	serveCmd.Flags().StringVar(&serveOAuthMCPClientID, "oauth-mcp-client-id", "", "OAuth client identifier (CIMD URL). If empty, auto-derived from public URL")

	// OAuth Server protection flags (for protecting the Muster Server ITSELF - ADR 005)
	// These configure muster as an OAuth resource server to protect its endpoints
	// Note: Full OAuth server configuration should be done via config file (config.yaml)
	serveCmd.Flags().BoolVar(&serveOAuthServerEnabled, "oauth-server", false, "Enable OAuth 2.1 protection for Muster Server (requires config file for full setup)")
	serveCmd.Flags().StringVar(&serveOAuthServerBaseURL, "oauth-server-base-url", "", "Base URL of the Muster Server for OAuth (e.g., https://muster.example.com)")

	// Events flags (alpha feature, disabled by default)
	serveCmd.Flags().BoolVar(&serveEnableEvents, "enable-events", false, "Enable Kubernetes event emission (alpha)")

	// PEM file appended to the system trust pool at startup. Use for internal
	// CAs (e.g. tunnelport SPIFFE bundle) without a per-MCPServer caFile knob.
	serveCmd.Flags().StringVar(&serveExtraCAFile, "extra-ca-file", "", "PEM file whose certificates are appended to the system trust pool at startup")
}
