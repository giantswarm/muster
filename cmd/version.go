package cmd

import (
	"context"
	"fmt"
	"time"

	"muster/internal/agent"
	"muster/internal/cli"

	"github.com/spf13/cobra"
)

// versionCheckTimeout is the timeout for connecting to the server to retrieve version info.
const versionCheckTimeout = 5 * time.Second

// newVersionCmd creates the Cobra command for displaying the application version.
// The command displays both the CLI version (from build-time injection) and the
// server version (if the muster aggregator is running).
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version number of muster CLI and server",
		Long: `Displays the muster CLI version and, if the aggregator server is running,
also displays the server version obtained from the MCP protocol handshake.`,
		Run: func(cmd *cobra.Command, args []string) {
			// Print CLI version
			fmt.Fprintf(cmd.OutOrStdout(), "muster version %s\n", rootCmd.Version)

			// Try to get server version
			serverVersion, serverName, err := getServerVersion()
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "\nServer: (not running)\n")
				return
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\nServer: %s (%s)\n", serverVersion, serverName)
		},
	}
}

// getServerVersion attempts to connect to the muster aggregator and retrieve
// the server version from the MCP initialization handshake.
func getServerVersion() (version, name string, err error) {
	endpoint := cli.GetAggregatorEndpoint(nil)

	// Quick check if server is running before creating client
	if err := cli.CheckServerRunning(endpoint); err != nil {
		return "", "", fmt.Errorf("server not running: %w", err)
	}

	// Create context with timeout for the entire operation
	ctx, cancel := context.WithTimeout(context.Background(), versionCheckTimeout)
	defer cancel()

	// Create a client without logging for version check
	mcpClient := agent.NewClient(endpoint, nil, agent.TransportStreamableHTTP)
	defer mcpClient.Close()

	// Connect to the server (this performs the MCP handshake)
	if err := mcpClient.Connect(ctx); err != nil {
		return "", "", fmt.Errorf("failed to connect: %w", err)
	}

	// Get server info from the initialization response
	serverInfo := mcpClient.GetServerInfo()
	if serverInfo == nil {
		return "", "", fmt.Errorf("no server info available")
	}

	return serverInfo.Version, serverInfo.Name, nil
}
