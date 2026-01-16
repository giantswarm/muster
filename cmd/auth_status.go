package cmd

import (
	"context"
	"fmt"
	"time"

	"muster/internal/api"
	pkgoauth "muster/pkg/oauth"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// Status-specific flags
var (
	statusServer string
)

// authStatusCmd represents the auth status command
var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show authentication status",
	Long: `Show the current authentication status for all known endpoints.

This command displays which endpoints you are authenticated to, when
tokens expire, and which endpoints require authentication.

Examples:
  muster auth status                   # Show all auth status
  muster auth status --endpoint <url>  # Show status for specific endpoint
  muster auth status --server <name>   # Show status for specific MCP server`,
	RunE: runAuthStatus,
}

func init() {
	// Status-specific flags
	authStatusCmd.Flags().StringVar(&statusServer, "server", "", "Show status for specific MCP server")
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	handler, err := ensureAuthHandler()
	if err != nil {
		return err
	}

	// Get the aggregator endpoint (use --endpoint if provided, otherwise from config)
	var aggregatorEndpoint string
	if authEndpoint != "" {
		aggregatorEndpoint = authEndpoint
	} else {
		aggregatorEndpoint, err = getEndpointFromConfig()
		if err != nil {
			return err
		}
	}

	// If specific server is requested, show that server's status
	// Note: --server takes precedence over just showing the aggregator endpoint status
	if statusServer != "" {
		return showMCPServerStatus(cmd.Context(), handler, aggregatorEndpoint, statusServer)
	}

	// Show aggregator status
	if !authQuiet {
		fmt.Println("Muster Aggregator")
	}
	status := handler.GetStatusForEndpoint(aggregatorEndpoint)
	if status != nil {
		if !authQuiet {
			fmt.Printf("  Endpoint:  %s\n", aggregatorEndpoint)
		}
		if status.Authenticated {
			if !authQuiet {
				fmt.Printf("  Status:    %s\n", text.FgGreen.Sprint("Authenticated"))
				if !status.ExpiresAt.IsZero() {
					fmt.Printf("  Expires:   %s\n", formatExpiryWithDirection(status.ExpiresAt))
				}
				if status.IssuerURL != "" {
					fmt.Printf("  Issuer:    %s\n", status.IssuerURL)
				}
			}
		} else {
			// Check if auth is required
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			authRequired, _ := handler.CheckAuthRequired(ctx, aggregatorEndpoint)
			cancel()

			if !authQuiet {
				if authRequired {
					fmt.Printf("  Status:    %s\n", text.FgYellow.Sprint("Not authenticated"))
					fmt.Printf("             Run: muster auth login\n")
				} else {
					fmt.Printf("  Status:    %s\n", text.FgHiBlack.Sprint("No authentication required"))
				}
			}
		}
	}

	// Try to get MCP server status from the aggregator
	if handler.HasValidToken(aggregatorEndpoint) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		authStatus, err := getAuthStatusFromAggregator(ctx, handler, aggregatorEndpoint)
		if err == nil && len(authStatus.Servers) > 0 {
			if !authQuiet {
				fmt.Println("\nMCP Servers")
				printMCPServerStatuses(authStatus.Servers)
			}
		}
	}

	return nil
}

// showMCPServerStatus shows the authentication status of a specific MCP server.
func showMCPServerStatus(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint, serverName string) error {
	// Need to be authenticated to the aggregator first
	if !handler.HasValidToken(aggregatorEndpoint) {
		return fmt.Errorf("not authenticated to aggregator. Run 'muster auth login' first")
	}

	authStatus, err := getAuthStatusFromAggregator(ctx, handler, aggregatorEndpoint)
	if err != nil {
		return fmt.Errorf("failed to get server status: %w", err)
	}

	// Find the requested server
	for _, srv := range authStatus.Servers {
		if srv.Name == serverName {
			if !authQuiet {
				fmt.Printf("\nMCP Server: %s\n", srv.Name)
				fmt.Printf("  Status:   %s\n", formatMCPServerStatus(srv.Status))
				if srv.Issuer != "" {
					fmt.Printf("  Issuer:   %s\n", srv.Issuer)
				}
				if srv.AuthTool != "" && srv.Status == pkgoauth.ServerStatusAuthRequired {
					fmt.Printf("  Action:   Run: muster auth login --server %s\n", srv.Name)
				}
			}
			return nil
		}
	}

	return fmt.Errorf("server '%s' not found. Use 'muster auth status' to see available servers", serverName)
}

// printMCPServerStatuses prints the status of all MCP servers.
func printMCPServerStatuses(servers []pkgoauth.ServerAuthStatus) {
	// Count servers requiring auth
	var pendingCount int
	for _, srv := range servers {
		if srv.Status == pkgoauth.ServerStatusAuthRequired {
			pendingCount++
		}
	}

	if pendingCount > 0 {
		fmt.Printf("  (%d pending authentication)\n", pendingCount)
	}

	for _, srv := range servers {
		statusStr := formatMCPServerStatus(srv.Status)
		if srv.Status == pkgoauth.ServerStatusAuthRequired && srv.AuthTool != "" {
			fmt.Printf("  %-20s %s   Run: muster auth login --server %s\n", srv.Name, statusStr, srv.Name)
		} else {
			fmt.Printf("  %-20s %s\n", srv.Name, statusStr)
		}
	}
}

// formatMCPServerStatus formats the MCP server status with colors.
func formatMCPServerStatus(status string) string {
	switch status {
	case pkgoauth.ServerStatusConnected:
		return text.FgGreen.Sprint("Connected")
	case pkgoauth.ServerStatusAuthRequired:
		return text.FgYellow.Sprint("Not authenticated")
	case pkgoauth.ServerStatusDisconnected:
		return text.FgRed.Sprint("Disconnected")
	case pkgoauth.ServerStatusError:
		return text.FgRed.Sprint("Error")
	default:
		return text.FgHiBlack.Sprint(status)
	}
}
