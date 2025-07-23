package cmd

import (
	"context"
	"errors"

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
	errCh := make(chan error, 1)

	// Start the aggregator server
	go func() {
		errCh <- runServe(cmd, args)
	}()

	// Start the agent
	go func() {
		errCh <- runAgent(cmd, args)
	}()

	// Wait for either the aggregator server or agent to return an error
	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		return err
	}

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
