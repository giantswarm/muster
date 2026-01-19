package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"muster/internal/api"
	"muster/internal/config"
	pkgoauth "muster/pkg/oauth"

	"github.com/spf13/cobra"
)

var (
	authEndpoint   string
	authContext    string
	authConfigPath string
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
  muster auth logout -s <name>         # Logout from specific MCP server
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
	logoutAll    bool
	logoutYes    bool
	logoutServer string
)

// authPrint prints output only if the --quiet flag is not set.
// Use this for progress messages and non-essential output.
func authPrint(format string, args ...interface{}) {
	if !authQuiet {
		fmt.Printf(format, args...)
	}
}

// authPrintln prints a line only if the --quiet flag is not set.
// Use this for progress messages and non-essential output.
func authPrintln(a ...interface{}) {
	if !authQuiet {
		fmt.Println(a...)
	}
}

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authRefreshCmd)
	authCmd.AddCommand(authWhoamiCmd)

	// Common flags for auth commands (shared across subcommands)
	authCmd.PersistentFlags().StringVar(&authEndpoint, "endpoint", "", "Specific endpoint URL to authenticate to")
	authCmd.PersistentFlags().StringVar(&authContext, "context", "", "Use a specific context (env: MUSTER_CONTEXT)")
	authCmd.PersistentFlags().StringVar(&authConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
	authCmd.PersistentFlags().BoolVarP(&authQuiet, "quiet", "q", false, "Suppress non-essential output")

	// Logout-specific flags (only on logout subcommand)
	authLogoutCmd.Flags().BoolVar(&logoutAll, "all", false, "Clear all stored tokens")
	authLogoutCmd.Flags().BoolVarP(&logoutYes, "yes", "y", false, "Skip confirmation prompt for --all")
	authLogoutCmd.Flags().StringVarP(&logoutServer, "server", "s", "", "MCP server name to disconnect")
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
			authPrintln("No stored tokens to clear.")
			return nil
		}

		// Show what will be cleared and ask for confirmation
		if !logoutYes {
			// Count authenticated tokens
			authCount := 0
			for _, status := range statuses {
				if status.Authenticated {
					authCount++
				}
			}
			fmt.Printf("The following %d token(s) will be cleared:\n", authCount)
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

		authPrint("Cleared %d stored token(s).\n", len(statuses))
		return nil
	}

	// Determine which endpoint to logout from
	var endpoint string
	if authEndpoint != "" {
		endpoint = authEndpoint
	} else if logoutServer != "" {
		// MCP server logout - show guidance based on SSO mechanism
		return showMCPServerLogoutGuidance(cmd.Context(), handler, logoutServer)
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

	authPrint("Logged out from %s\n", endpoint)
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

	authPrint("Refreshing token for %s...\n", endpoint)
	if err := handler.RefreshToken(ctx, endpoint); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	authPrintln("Token refreshed successfully.")
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

	// Display identity information - identity first, then context
	if status.Email != "" {
		fmt.Printf("Identity:  %s\n", status.Email)
	} else if status.Subject != "" {
		fmt.Printf("Identity:  %s\n", status.Subject)
	}
	fmt.Printf("Endpoint:  %s\n", status.Endpoint)
	if status.IssuerURL != "" {
		fmt.Printf("Issuer:    %s\n", status.IssuerURL)
	}
	if !status.ExpiresAt.IsZero() {
		fmt.Printf("Expires:   %s\n", formatExpiryWithDirection(status.ExpiresAt))
	}

	return nil
}

// showMCPServerLogoutGuidance displays logout guidance for a specific MCP server.
// It explains the authentication mechanism in use and how to disconnect.
// Note: This function provides informational output only; it does not perform logout.
func showMCPServerLogoutGuidance(ctx context.Context, handler api.AuthHandler, serverName string) error {
	// Get the aggregator endpoint
	endpoint, err := getEndpointFromConfig()
	if err != nil {
		return err
	}

	// Check if we're authenticated to the aggregator
	if !handler.HasValidToken(endpoint) {
		authPrintln("Not authenticated to aggregator.")
		authPrintln("Run 'muster auth login' first to check server status.")
		return nil
	}

	// Query the aggregator for server status
	authStatus, err := getAuthStatusFromAggregator(ctx, handler, endpoint)
	if err != nil {
		// Fall back to generic message if we can't get status
		authPrintln("Note: MCP server authentication is managed by the aggregator.")
		authPrintln("To disconnect all servers, run: muster auth logout")
		return nil
	}

	// Find the requested server
	var serverInfo *pkgoauth.ServerAuthStatus
	for i := range authStatus.Servers {
		if authStatus.Servers[i].Name == serverName {
			serverInfo = &authStatus.Servers[i]
			break
		}
	}

	if serverInfo == nil {
		return fmt.Errorf("server '%s' not found. Use 'muster auth status' to see available servers", serverName)
	}

	// Provide appropriate message based on authentication mechanism
	if serverInfo.TokenForwardingEnabled {
		authPrint("Server '%s' uses SSO via Token Forwarding.\n\n", serverName)
		authPrintln("This server automatically receives your muster ID token.")
		authPrintln("You authenticated once to muster, and that identity is forwarded")
		authPrintln("to this server. To disconnect, log out from muster:")
		authPrintln("  muster auth logout")
	} else if serverInfo.Issuer != "" {
		authPrint("Server '%s' uses SSO via Token Reuse.\n\n", serverName)
		authPrintln("This server shares an OAuth issuer with other servers.")
		authPrintln("Your token for this issuer is reused across all servers that")
		authPrintln("trust the same identity provider. To disconnect:")
		authPrintln("  muster auth logout")
	} else {
		authPrint("Server '%s' uses direct authentication.\n\n", serverName)
		authPrintln("MCP server sessions are managed by the aggregator and will be")
		authPrintln("cleared when you log out from the aggregator:")
		authPrintln("  muster auth logout")
	}

	return nil
}
