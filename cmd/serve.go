package cmd

import (
	"context"
	"fmt"
	"muster/internal/app"

	"github.com/spf13/cobra"
)

// noTUI controls whether to run in CLI mode (true) or TUI mode (false).
// CLI mode is useful for scripting and CI/CD environments where interactive UI is not desired.
var serveNoTUI bool

// debug enables verbose logging across the application.
// This helps troubleshoot connection issues and understand service behavior.
var serveDebug bool

// yolo disables the denylist for destructive tool calls.
// When enabled, all MCP tools can be executed without restrictions.
var serveYolo bool

// configPath specifies a custom configuration directory path.
// When set, disables layered configuration and loads all config from this single directory.
// The directory should contain config.yaml and subdirectories: mcpserver/, workflow/, capability/, serviceclass/, service/
var serveConfigPath string

// serveCmd defines the serve command structure.
// This is the main command of muster that starts the aggregator server
// and sets up the necessary MCP servers for development.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the muster aggregator server with an interactive TUI or CLI mode.",
	Long: `Starts the muster aggregator server and manages MCP servers for AI assistant access.
It can run in two modes:

1. Interactive TUI Mode (default):
   - Launches a terminal user interface to monitor service connections and manage MCP servers.
   - Automatically starts configured MCP servers for AI assistant access.
   - Provides real-time status and allows interactive control over services.

2. Non-TUI / CLI Mode (using --no-tui flag):
   - Starts configured MCP servers and services in the background.
   - Prints a summary of actions and connection details to the console, then exits.
   - Useful for scripting or when a TUI is not desired. Services continue to run
     until the 'muster' process initiated by 'serve --no-tui' is terminated (e.g., Ctrl+C).

The aggregator server provides a unified MCP interface that other muster commands can connect to.
Use 'muster service', 'muster workflow', etc. to interact with the running server.

Configuration:
  muster loads configuration from .muster/config.yaml in the current directory or user config directory.
  By default, it uses layered configuration loading (user config overridden by project config).
  
  Use --config-path to specify a single directory containing all configuration files:
  - config.yaml (main configuration)
  - mcpservers/ (MCP server definitions)
  - workflows/ (workflow definitions)  
  - capabilities/ (capability definitions)
  - serviceclasses/ (service class definitions)
  - services/ (service instance definitions)
  
  When --config-path is used, layered configuration is disabled.`,
	Args: cobra.NoArgs, // No arguments required
	RunE: runServe,
}

// runServe is the main entry point for the serve command
func runServe(cmd *cobra.Command, args []string) error {
	// Create application configuration without cluster arguments
	cfg := app.NewConfig(serveNoTUI, serveDebug, serveYolo, serveConfigPath)

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
	serveCmd.Flags().BoolVar(&serveNoTUI, "no-tui", false, "Disable TUI and run services in the background")
	serveCmd.Flags().BoolVar(&serveDebug, "debug", false, "Enable general debug logging")
	serveCmd.Flags().BoolVar(&serveYolo, "yolo", false, "Disable denylist for destructive tool calls (use with caution)")
	serveCmd.Flags().StringVar(&serveConfigPath, "config-path", "", "Custom configuration directory path (disables layered config)")
}
