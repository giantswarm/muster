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

	"github.com/giantswarm/muster/v5/internal/app"
	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
	"github.com/giantswarm/muster/v5/pkg/observability"
	"github.com/giantswarm/muster/v5/pkg/project"

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

// configPath specifies the configuration directory.
// The directory should contain config.yaml and subdirectories: mcpservers/, workflows/
var serveConfigPath string

// OAuth MCP Client/Proxy configuration flags (for authenticating TO remote MCP servers - ADR 004)
var (
	// serveOAuthMCPClientEnabled enables the OAuth MCP client/proxy functionality for remote MCP servers
	serveOAuthMCPClientEnabled bool
	// serveOAuthMCPClientPublicURL is the publicly accessible URL of the muster server
	serveOAuthMCPClientPublicURL string
	// serveOAuthMCPClientID is the OAuth client identifier (CIMD URL)
	serveOAuthMCPClientID string
)

// OAuth Server configuration flags (for protecting the muster server ITSELF - ADR 005)
var (
	// serveOAuthServerEnabled enables OAuth server protection for the muster server
	serveOAuthServerEnabled bool
	// serveOAuthServerBaseURL is the base URL of the muster server (for OAuth issuer)
	serveOAuthServerBaseURL string
)

// serveExtraCAFile is a PEM file appended to the system trust pool at startup.
var serveExtraCAFile string

// serveEnableEvents is retained only to keep `muster serve --enable-events`
// invocations from existing scripts/units working after events became
// always-on. The flag is hidden, deprecated, and has no effect.
var serveEnableEvents bool

// serveCmd defines the serve command structure.
// This is the main command of muster that starts the aggregator server
// and sets up the necessary MCP servers for development.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the muster aggregator server.",
	Long: `Start the muster aggregator: the process that connects to the registered
MCP servers, aggregates their tools and serves them over one MCP endpoint
(http://localhost:8090/mcp by default).

  - Registered MCP servers are connected, or started when they are stdio
    servers with autoStart, and reconnected when they fail.
  - Every other muster command (list, get, create, call, start, stop, check,
    events) talks to this endpoint.
  - An MCP client connects to the endpoint directly, or through
    'muster agent --mcp-server' when it needs a stdio transport.

Configuration is read from ~/.config/muster unless --config-path names another
directory. The directory holds config.yaml, mcpservers/ with one MCPServer
definition per file and workflows/ with one Workflow per file. With
'kubernetes: true' in config.yaml the definitions are read from the MCPServer
and Workflow custom resources of the configured namespace instead.

OAuth protection of the endpoint and OAuth towards remote MCP servers are
configured in config.yaml; the --oauth-* flags switch them on for a quick
local trial. See https://giantswarm.github.io/muster/ for the reference.`,
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
	shutdownLogging, err := logging.Init(ctx, level, output, "muster", project.Version())
	if err != nil {
		return fmt.Errorf("init logging: %w", err)
	}
	defer otelShutdown("logging", shutdownLogging)

	shutdownTracing, err := tracing.Init(ctx,
		tracing.WithServiceName("muster"),
		tracing.WithServiceVersion(project.Version()),
	)
	if err != nil {
		return fmt.Errorf("init tracing: %w", err)
	}
	defer otelShutdown("tracing", shutdownTracing)

	shutdownMeter, err := mcptoolkitmetrics.Init(ctx,
		mcptoolkitmetrics.WithServiceName("muster"),
		mcptoolkitmetrics.WithServiceVersion(project.Version()),
		mcptoolkitmetrics.WithViews(observability.SecondsHistogramView()),
	)
	if err != nil {
		return fmt.Errorf("init meter: %w", err)
	}
	defer otelShutdown("meter", shutdownMeter)

	// The controllable clock's control endpoint, served only when the
	// integration test harness selected one through MUSTER_TEST_CLOCK.
	stopClock, err := clock.StartControl()
	if err != nil {
		return err
	}
	defer stopClock()

	// Create application configuration without cluster arguments
	cfg := app.NewConfig(serveDebug, serveConfigPath).
		WithVersion(project.Version()).
		WithOAuthMCPClient(serveOAuthMCPClientEnabled, serveOAuthMCPClientPublicURL, serveOAuthMCPClientID).
		WithOAuthServer(serveOAuthServerEnabled, serveOAuthServerBaseURL).
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
	serveCmd.Flags().StringVar(&serveConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")

	// OAuth MCP Client/Proxy flags (for authenticating TO remote MCP servers - ADR 004)
	// These configure muster as an OAuth client when connecting to remote MCP servers
	serveCmd.Flags().BoolVar(&serveOAuthMCPClientEnabled, "oauth-mcp-client", false, "Enable OAuth MCP client/proxy for remote MCP server authentication")
	serveCmd.Flags().StringVar(&serveOAuthMCPClientPublicURL, "oauth-mcp-client-public-url", "", "Publicly accessible URL of the muster server for OAuth callbacks")
	// Note: When --oauth-mcp-client-id is empty (default), the client ID is auto-derived from publicUrl
	// as {publicUrl}/.well-known/oauth-client.json and muster serves its own CIMD
	serveCmd.Flags().StringVar(&serveOAuthMCPClientID, "oauth-mcp-client-id", "", "OAuth client identifier (CIMD URL). If empty, auto-derived from public URL")

	// OAuth Server protection flags (for protecting the muster server ITSELF - ADR 005)
	// These configure muster as an OAuth resource server to protect its endpoints
	// Note: Full OAuth server configuration should be done via config file (config.yaml)
	serveCmd.Flags().BoolVar(&serveOAuthServerEnabled, "oauth-server", false, "Enable OAuth 2.1 protection for muster server (requires config file for full setup)")
	serveCmd.Flags().StringVar(&serveOAuthServerBaseURL, "oauth-server-base-url", "", "Base URL of the muster server for OAuth (e.g., https://muster.example.com)")

	// PEM file appended to the system trust pool at startup. Use for internal
	// CAs (e.g. tunnelport SPIFFE bundle) without a per-MCPServer caFile knob.
	serveCmd.Flags().StringVar(&serveExtraCAFile, "extra-ca-file", "", "PEM file whose certificates are appended to the system trust pool at startup")

	// Deprecated no-op: events are always on. Kept hidden so existing
	// `--enable-events` invocations don't fail with "unknown flag" after upgrade.
	serveCmd.Flags().BoolVar(&serveEnableEvents, "enable-events", false, "Deprecated: events are always enabled; this flag has no effect")
	_ = serveCmd.Flags().MarkHidden("enable-events")
	_ = serveCmd.Flags().MarkDeprecated("enable-events", "events are always enabled; remove this flag")
}
