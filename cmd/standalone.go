package cmd

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// standaloneCmd defines the standalone command structure.
// This command starts both the muster aggregator server
// and muster agent.
var standaloneCmd = &cobra.Command{
	Use:   "standalone",
	Short: "Start the muster in standalone mode",
	Long: `Standalone mode starts the muster aggregator server and the agent in a single process.
It enforces the MCP server mode for the agent and disables serve logging.`,
	RunE: runStandalone,
}

// runStandalone is the main entry point for the standalone command
func runStandalone(cmd *cobra.Command, args []string) error {
	// Enable agent MCP server mode
	agentMCPServer = true
	// Disable serve logging
	serveSilent = true

	// Start the aggregator server
	go runServe(cmd, args)

	// Start the agent
	go runAgent(cmd, args)

	// Wait for termination signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	return nil
}

// init registers the standalone command and its flags with the root command.
// This is called automatically when the package is imported.
func init() {
	rootCmd.AddCommand(standaloneCmd)

	// Inherit flags from agent and serve commands
	standaloneCmd.Flags().AddFlagSet(agentCmd.Flags())
	standaloneCmd.Flags().AddFlagSet(serveCmd.Flags())
}
