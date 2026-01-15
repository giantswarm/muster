package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"muster/internal/config"

	"github.com/spf13/cobra"
)

var (
	authEndpoint   string
	authConfigPath string
	authServer     string
	authQuiet      bool
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
  muster auth login                    # Login to configured aggregator
  muster auth login --endpoint <url>   # Login to specific remote endpoint
  muster auth status                   # Show authentication status
  muster auth logout                   # Logout from configured aggregator
  muster auth logout --all             # Clear all stored tokens
  muster auth refresh                  # Force token refresh
  muster auth whoami                   # Show current identity`,
}

// authLogoutCmd represents the auth logout command
var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear stored authentication tokens",
	Long: `Clear stored OAuth tokens.

This command removes cached authentication tokens, requiring you to
re-authenticate on the next connection to protected endpoints.

Examples:
  muster auth logout                   # Logout from configured aggregator
  muster auth logout --endpoint <url>  # Logout from specific endpoint
  muster auth logout --all             # Clear all stored tokens
  muster auth logout --all --yes       # Clear all without confirmation`,
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
  muster auth refresh                  # Refresh configured aggregator
  muster auth refresh --endpoint <url> # Refresh specific endpoint`,
	RunE: runAuthRefresh,
}

// authWhoamiCmd represents the auth whoami command
var authWhoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show current authenticated identity",
	Long: `Show the currently authenticated identity and token information.

This command displays details about your current authentication state,
including the issuer, token expiration, and endpoint information.

Examples:
  muster auth whoami                   # Show identity for configured aggregator
  muster auth whoami --endpoint <url>  # Show identity for specific endpoint`,
	RunE: runAuthWhoami,
}

// Logout-specific flags
var (
	logoutAll bool
	logoutYes bool
)

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authRefreshCmd)
	authCmd.AddCommand(authWhoamiCmd)

	// Common flags for auth commands (shared across subcommands)
	authCmd.PersistentFlags().StringVar(&authEndpoint, "endpoint", "", "Specific endpoint URL to authenticate to")
	authCmd.PersistentFlags().StringVar(&authConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
	authCmd.PersistentFlags().BoolVarP(&authQuiet, "quiet", "q", false, "Suppress non-essential output")

	// Logout-specific flags (only on logout subcommand)
	authLogoutCmd.Flags().BoolVar(&logoutAll, "all", false, "Clear all stored tokens")
	authLogoutCmd.Flags().BoolVarP(&logoutYes, "yes", "y", false, "Skip confirmation prompt for --all")
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
	handler, err := ensureAuthHandler()
	if err != nil {
		return err
	}

	if logoutAll {
		// Get list of tokens that will be cleared
		statuses := handler.GetStatus()

		if len(statuses) == 0 {
			if !authQuiet {
				fmt.Println("No stored tokens to clear.")
			}
			return nil
		}

		// Show what will be cleared and ask for confirmation
		if !logoutYes {
			fmt.Println("The following tokens will be cleared:")
			for _, status := range statuses {
				if status.Authenticated {
					fmt.Printf("  - %s\n", status.Endpoint)
				}
			}
			fmt.Print("\nAre you sure you want to clear all tokens? [y/N]: ")

			reader := bufio.NewReader(os.Stdin)
			response, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("failed to read response: %w", err)
			}

			response = strings.TrimSpace(strings.ToLower(response))
			if response != "y" && response != "yes" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		if err := handler.LogoutAll(); err != nil {
			return fmt.Errorf("failed to clear all tokens: %w", err)
		}

		if !authQuiet {
			fmt.Printf("Cleared %d stored token(s).\n", len(statuses))
		}
		return nil
	}

	// Determine which endpoint to logout from
	var endpoint string
	if authEndpoint != "" {
		endpoint = authEndpoint
	} else if authServer != "" {
		// MCP server logout - note that MCP server auth is managed by the aggregator,
		// not stored locally. We can inform the user about this.
		fmt.Println("Note: MCP server authentication is managed by the aggregator.")
		fmt.Println("To disconnect a server, use the aggregator's management interface.")
		fmt.Println("To clear all local tokens including aggregator auth, run: muster auth logout --all")
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

	if !authQuiet {
		fmt.Printf("Logged out from %s\n", endpoint)
	}
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

	if !authQuiet {
		fmt.Printf("Refreshing token for %s...\n", endpoint)
	}
	if err := handler.RefreshToken(ctx, endpoint); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	if !authQuiet {
		fmt.Println("Token refreshed successfully.")
	}
	return nil
}

func runAuthWhoami(cmd *cobra.Command, args []string) error {
	handler, err := ensureAuthHandler()
	if err != nil {
		return err
	}

	// Get the endpoint
	var endpoint string
	if authEndpoint != "" {
		endpoint = authEndpoint
	} else {
		endpoint, err = getEndpointFromConfig()
		if err != nil {
			return err
		}
	}

	status := handler.GetStatusForEndpoint(endpoint)
	if status == nil {
		return fmt.Errorf("no authentication information for %s", endpoint)
	}

	if !status.Authenticated {
		fmt.Printf("Not authenticated to %s\n", endpoint)
		fmt.Println("\nTo authenticate, run:")
		fmt.Printf("  muster auth login --endpoint %s\n", endpoint)
		return nil
	}

	// Display identity information
	fmt.Printf("Endpoint:  %s\n", status.Endpoint)
	if status.IssuerURL != "" {
		fmt.Printf("Issuer:    %s\n", status.IssuerURL)
	}
	if !status.ExpiresAt.IsZero() {
		fmt.Printf("Expires:   %s\n", formatExpiryWithDirection(status.ExpiresAt))
	}

	return nil
}
