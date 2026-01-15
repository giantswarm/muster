package cmd

import (
	"fmt"

	"muster/internal/config"

	"github.com/spf13/cobra"
)

var (
	authEndpoint   string
	authConfigPath string
	authServer     string
	authAll        bool
)

// authCmd represents the auth command group
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication for muster",
	Long: `Manage authentication for muster CLI commands.

The auth command group provides subcommands to login, logout, check status,
and refresh authentication tokens for remote muster aggregators that require
OAuth authentication.

Examples:
  muster auth login                      # Login to configured aggregator
  muster auth login --endpoint <url>     # Login to specific remote endpoint
  muster auth status                     # Show authentication status
  muster auth logout                     # Logout from all endpoints
  muster auth refresh                    # Force token refresh`,
}

// authLogoutCmd represents the auth logout command
var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear stored authentication tokens",
	Long: `Clear stored OAuth tokens.

This command removes cached authentication tokens, requiring you to
re-authenticate on the next connection to protected endpoints.

Examples:
  muster auth logout                     # Logout from configured aggregator
  muster auth logout --endpoint <url>    # Logout from specific endpoint
  muster auth logout --server <name>     # Logout from specific MCP server
  muster auth logout --all               # Clear all stored tokens`,
	RunE: runAuthLogout,
}

// authRefreshCmd represents the auth refresh command
var authRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Force token refresh",
	Long: `Force a refresh of the authentication token.

This command attempts to refresh the OAuth token for an endpoint,
which can be useful if you're experiencing authentication issues.

Examples:
  muster auth refresh                    # Refresh configured aggregator
  muster auth refresh --endpoint <url>   # Refresh specific endpoint`,
	RunE: runAuthRefresh,
}

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authRefreshCmd)

	// Common flags for auth commands
	authCmd.PersistentFlags().StringVar(&authEndpoint, "endpoint", "", "Specific endpoint URL to authenticate to")
	authCmd.PersistentFlags().StringVar(&authConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
	authCmd.PersistentFlags().StringVar(&authServer, "server", "", "Specific MCP server name to authenticate to")
	authCmd.PersistentFlags().BoolVar(&authAll, "all", false, "Authenticate to all pending endpoints")
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
	handler, err := ensureAuthHandler()
	if err != nil {
		return err
	}

	if authAll {
		if err := handler.LogoutAll(); err != nil {
			return fmt.Errorf("failed to clear all tokens: %w", err)
		}
		fmt.Println("All stored tokens have been cleared.")
		return nil
	}

	// Determine which endpoint to logout from
	var endpoint string
	if authEndpoint != "" {
		endpoint = authEndpoint
	} else if authServer != "" {
		// MCP server logout - note that MCP server auth is managed by the aggregator,
		// not stored locally. We can inform the user about this.
		fmt.Printf("Note: MCP server authentication is managed by the aggregator.\n")
		fmt.Printf("To disconnect a server, use the aggregator's management interface.\n")
		fmt.Printf("To clear all local tokens including aggregator auth, run: muster auth logout --all\n")
		return nil
	} else {
		// Use configured aggregator endpoint
		endpoint, err = getEndpointFromConfig()
		if err != nil {
			return err
		}
	}

	if err := handler.Logout(endpoint); err != nil {
		return fmt.Errorf("failed to logout: %w", err)
	}

	fmt.Printf("Logged out from %s\n", endpoint)
	return nil
}

func runAuthRefresh(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	handler, err := ensureAuthHandler()
	if err != nil {
		return err
	}

	// Determine which endpoint to refresh
	var endpoint string
	if authEndpoint != "" {
		endpoint = authEndpoint
	} else {
		// Use configured aggregator endpoint
		endpoint, err = getEndpointFromConfig()
		if err != nil {
			return err
		}
	}

	fmt.Printf("Refreshing token for %s...\n", endpoint)
	if err := handler.RefreshToken(ctx, endpoint); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	fmt.Println("Token refreshed successfully.")
	return nil
}
