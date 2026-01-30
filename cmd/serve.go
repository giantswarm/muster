package cmd

import (
	"context"
	"fmt"

	"muster/internal/app"
	"muster/internal/config"

	"github.com/spf13/cobra"
)

// debug enables verbose logging across the application.
// This helps troubleshoot connection issues and understand service behavior.
var serveDebug bool

// serveSilent disables all output to the console.
var serveSilent bool

// yolo disables the denylist for destructive tool calls.
// When enabled, all MCP tools can be executed without restrictions.
var serveYolo bool

// configPath specifies the configuration directory.
// The directory should contain config.yaml and subdirectories: mcpservers/, workflows/, serviceclasses/, services/
var serveConfigPath string

// OAuth Client/Proxy configuration flags (for authenticating TO remote MCP servers - ADR 004)
var (
	// serveOAuthClientEnabled enables the OAuth client/proxy functionality for remote MCP servers
	serveOAuthClientEnabled bool
	// serveOAuthClientPublicURL is the publicly accessible URL of the Muster Server
	serveOAuthClientPublicURL string
	// serveOAuthClientID is the OAuth client identifier (CIMD URL)
	serveOAuthClientID string
)

// OAuth Server configuration flags (for protecting the Muster Server ITSELF - ADR 005)
var (
	// serveOAuthServerEnabled enables OAuth server protection for the Muster Server
	serveOAuthServerEnabled bool
	// serveOAuthServerBaseURL is the base URL of the Muster Server (for OAuth issuer)
	serveOAuthServerBaseURL string
)

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
  - workflows/ (workflow definitions)

  - serviceclasses/ (service class definitions)
  - services/ (service instance definitions)`,
	Args: cobra.NoArgs, // No arguments required
	RunE: runServe,
}

// runServe is the main entry point for the serve command
func runServe(cmd *cobra.Command, args []string) error {
	// Create application configuration without cluster arguments
	cfg := app.NewConfig(serveDebug, serveSilent, serveYolo, serveConfigPath).
		WithVersion(GetVersion()).
		WithOAuthClient(serveOAuthClientEnabled, serveOAuthClientPublicURL, serveOAuthClientID).
		WithOAuthServer(serveOAuthServerEnabled, serveOAuthServerBaseURL)

	// Create and initialize the application
	application, err := app.NewApplication(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize application: %w", err)
	}

	// Run the application
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	return application.Run(ctx)
}

// init registers the serve command and its flags with the root command.
// This is called automatically when the package is imported.
func init() {
	rootCmd.AddCommand(serveCmd)

	// Register command flags
	serveCmd.Flags().BoolVar(&serveDebug, "debug", false, "Enable general debug logging")
	serveCmd.Flags().BoolVar(&serveSilent, "silent", false, "Disable all output to the console")
	serveCmd.Flags().BoolVar(&serveYolo, "yolo", false, "Disable denylist for destructive tool calls (use with caution)")
	serveCmd.Flags().StringVar(&serveConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")

	// OAuth Client/Proxy flags (for authenticating TO remote MCP servers - ADR 004)
	// These configure muster as an OAuth client when connecting to remote MCP servers
	serveCmd.Flags().BoolVar(&serveOAuthClientEnabled, "oauth-client", false, "Enable OAuth client/proxy for remote MCP server authentication")
	serveCmd.Flags().StringVar(&serveOAuthClientPublicURL, "oauth-client-public-url", "", "Publicly accessible URL of the Muster Server for OAuth callbacks")
	// Note: When --oauth-client-id is empty (default), the client ID is auto-derived from publicUrl
	// as {publicUrl}/.well-known/oauth-client.json and muster serves its own CIMD
	serveCmd.Flags().StringVar(&serveOAuthClientID, "oauth-client-id", "", "OAuth client identifier (CIMD URL). If empty, auto-derived from public URL")

	// OAuth Server protection flags (for protecting the Muster Server ITSELF - ADR 005)
	// These configure muster as an OAuth resource server to protect its endpoints
	// Note: Full OAuth server configuration should be done via config file (config.yaml)
	serveCmd.Flags().BoolVar(&serveOAuthServerEnabled, "oauth-server", false, "Enable OAuth 2.1 protection for Muster Server (requires config file for full setup)")
	serveCmd.Flags().StringVar(&serveOAuthServerBaseURL, "oauth-server-base-url", "", "Base URL of the Muster Server for OAuth (e.g., https://muster.example.com)")
}
