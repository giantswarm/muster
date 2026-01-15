package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"muster/internal/api"
	"muster/internal/cli"
	"muster/internal/config"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
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

// authLoginCmd represents the auth login command
var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate to a muster aggregator",
	Long: `Authenticate to a muster aggregator using OAuth.

This command initiates an OAuth browser-based authentication flow to obtain
access tokens for connecting to OAuth-protected muster aggregators.

Examples:
  muster auth login                          # Login to configured aggregator
  muster auth login --endpoint <url>         # Login to specific endpoint
  muster auth login --server <name>          # Login to specific MCP server
  muster auth login --all                    # Login to aggregator + all pending MCP servers`,
	RunE: runAuthLogin,
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

// authStatusCmd represents the auth status command
var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show authentication status",
	Long: `Show the current authentication status for all known endpoints.

This command displays which endpoints you are authenticated to, when
tokens expire, and which endpoints require authentication.

Examples:
  muster auth status                     # Show all auth status
  muster auth status --endpoint <url>    # Show status for specific endpoint
  muster auth status --server <name>     # Show status for specific MCP server`,
	RunE: runAuthStatus,
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

// ensureAuthHandler ensures an auth handler is registered and returns it.
func ensureAuthHandler() (api.AuthHandler, error) {
	handler := api.GetAuthHandler()
	if handler != nil {
		return handler, nil
	}

	// Create and register the auth adapter
	adapter, err := cli.NewAuthAdapter()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize authentication: %w", err)
	}
	adapter.Register()

	return api.GetAuthHandler(), nil
}

// getEndpointFromConfig returns the aggregator endpoint from config.
func getEndpointFromConfig() (string, error) {
	cfg, err := config.LoadConfig(authConfigPath)
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}
	return cli.GetAggregatorEndpoint(&cfg), nil
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	handler, err := ensureAuthHandler()
	if err != nil {
		return err
	}

	// Determine which endpoint to authenticate to
	var endpoint string
	if authEndpoint != "" {
		endpoint = authEndpoint
	} else if authServer != "" {
		// TODO: Look up server endpoint from running aggregator
		return fmt.Errorf("--server flag is not yet implemented. Use --endpoint instead")
	} else {
		// Use configured aggregator endpoint
		endpoint, err = getEndpointFromConfig()
		if err != nil {
			return err
		}
	}

	if authAll {
		// Login to aggregator first
		fmt.Printf("Authenticating to %s...\n", endpoint)
		if err := handler.Login(ctx, endpoint); err != nil {
			return err
		}
		fmt.Println("done")

		// TODO: Get list of MCP servers requiring auth and authenticate to each
		fmt.Println("\nAll authentication complete.")
		return nil
	}

	// Single endpoint login
	return handler.Login(ctx, endpoint)
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
		return fmt.Errorf("--server flag is not yet implemented. Use --endpoint instead")
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

func runAuthStatus(cmd *cobra.Command, args []string) error {
	handler, err := ensureAuthHandler()
	if err != nil {
		return err
	}

	// If specific endpoint is requested
	if authEndpoint != "" {
		status := handler.GetStatusForEndpoint(authEndpoint)
		return printAuthStatus(status)
	}

	if authServer != "" {
		return fmt.Errorf("--server flag is not yet implemented. Use --endpoint instead")
	}

	// Show all statuses
	statuses := handler.GetStatus()

	// Also check the configured aggregator
	configuredEndpoint, err := getEndpointFromConfig()
	if err == nil {
		// Check if we already have this endpoint in the list
		found := false
		for _, s := range statuses {
			if s.Endpoint == configuredEndpoint {
				found = true
				break
			}
		}
		if !found {
			// Check auth required for configured endpoint
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			authRequired, _ := handler.CheckAuthRequired(ctx, configuredEndpoint)
			statuses = append([]api.AuthStatus{{
				Endpoint:      configuredEndpoint,
				Authenticated: handler.HasValidToken(configuredEndpoint),
				Error: func() string {
					if authRequired && !handler.HasValidToken(configuredEndpoint) {
						return "Not authenticated"
					}
					return ""
				}(),
			}}, statuses...)
		}
	}

	if len(statuses) == 0 {
		fmt.Println("No authentication information available.")
		fmt.Println("\nTo authenticate to a remote muster aggregator, run:")
		fmt.Println("  muster auth login --endpoint <url>")
		return nil
	}

	return printAuthStatuses(statuses)
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

func printAuthStatus(status *api.AuthStatus) error {
	if status == nil {
		fmt.Println("No authentication information available.")
		return nil
	}

	fmt.Printf("\nEndpoint:  %s\n", status.Endpoint)
	if status.Authenticated {
		fmt.Printf("Status:    %s\n", text.FgGreen.Sprint("Authenticated"))
		if !status.ExpiresAt.IsZero() {
			remaining := time.Until(status.ExpiresAt)
			if remaining > 0 {
				fmt.Printf("Expires:   in %s\n", formatDuration(remaining))
			} else {
				fmt.Printf("Expires:   %s\n", text.FgYellow.Sprint("Expired"))
			}
		}
		if status.IssuerURL != "" {
			fmt.Printf("Issuer:    %s\n", status.IssuerURL)
		}
	} else {
		fmt.Printf("Status:    %s\n", text.FgYellow.Sprint("Not authenticated"))
		if status.Error != "" {
			fmt.Printf("Error:     %s\n", status.Error)
		}
	}

	return nil
}

func printAuthStatuses(statuses []api.AuthStatus) error {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleLight)
	t.AppendHeader(table.Row{"Endpoint", "Status", "Expires", "Issuer"})

	for _, status := range statuses {
		var statusStr, expiresStr, issuerStr string

		if status.Authenticated {
			statusStr = text.FgGreen.Sprint("Authenticated")
			if !status.ExpiresAt.IsZero() {
				remaining := time.Until(status.ExpiresAt)
				if remaining > 0 {
					expiresStr = formatDuration(remaining)
				} else {
					expiresStr = text.FgYellow.Sprint("Expired")
				}
			}
			issuerStr = truncateURL(status.IssuerURL, 40)
		} else {
			if status.Error != "" {
				statusStr = text.FgYellow.Sprint("Not authenticated")
			} else {
				statusStr = text.FgHiBlack.Sprint("N/A")
			}
		}

		t.AppendRow(table.Row{
			truncateURL(status.Endpoint, 50),
			statusStr,
			expiresStr,
			issuerStr,
		})
	}

	t.Render()
	return nil
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "< 1 minute"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

func truncateURL(url string, maxLen int) string {
	if len(url) <= maxLen {
		return url
	}
	// Try to keep the hostname visible
	if idx := strings.Index(url, "://"); idx != -1 {
		start := url[:idx+3]
		rest := url[idx+3:]
		if len(rest) > maxLen-len(start)-3 {
			rest = rest[:maxLen-len(start)-3] + "..."
		}
		return start + rest
	}
	return url[:maxLen-3] + "..."
}
