package cmd

import (
	"context"
	"fmt"

	"muster/internal/api"
	pkgoauth "muster/pkg/oauth"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// minNameColumnWidth is the minimum width for the server name column in status output.
// This ensures consistent alignment in the CLI output.
const minNameColumnWidth = 20

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
	authStatusCmd.Flags().StringVar(&statusServer, "server", "", "MCP server name (managed by aggregator) to show status for")
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
	authPrintln("Muster Aggregator")
	status := handler.GetStatusForEndpoint(aggregatorEndpoint)
	if status != nil {
		authPrint("  Endpoint:  %s\n", aggregatorEndpoint)
		if status.Authenticated {
			authPrint("  Status:    %s\n", text.FgGreen.Sprint("Authenticated"))
			if !status.ExpiresAt.IsZero() {
				authPrint("  Expires:   %s\n", formatExpiryWithDirection(status.ExpiresAt))
			}
			if status.HasRefreshToken {
				authPrint("  Refresh:   %s\n", text.FgGreen.Sprint("Available"))
			} else {
				authPrint("  Refresh:   %s\n", text.FgYellow.Sprint("Not available (re-auth required on expiry)"))
			}
			if status.IssuerURL != "" {
				authPrint("  Issuer:    %s\n", status.IssuerURL)
			}
		} else {
			// Check if auth is required
			ctx, cancel := context.WithTimeout(context.Background(), ShortAuthCheckTimeout)
			authRequired, _ := handler.CheckAuthRequired(ctx, aggregatorEndpoint)
			cancel()

			if authRequired {
				authPrint("  Status:    %s\n", text.FgYellow.Sprint("Not authenticated"))
				authPrint("             Run: muster auth login\n")
			} else {
				authPrint("  Status:    %s\n", text.FgHiBlack.Sprint("No authentication required"))
			}
		}
	}

	// Try to get MCP server status from the aggregator
	if handler.HasValidToken(aggregatorEndpoint) {
		ctx, cancel := context.WithTimeout(context.Background(), DefaultStatusCheckTimeout)
		defer cancel()

		authStatus, err := getAuthStatusFromAggregator(ctx, handler, aggregatorEndpoint)
		if err == nil && len(authStatus.Servers) > 0 {
			authPrintln("\nMCP Servers")
			printMCPServerStatuses(authStatus.Servers)
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
			authPrint("\nMCP Server: %s\n", srv.Name)
			authPrint("  Status:   %s\n", formatMCPServerStatus(srv.Status))
			if ssoType := getSSOType(srv); ssoType != "" {
				authPrint("  SSO:      %s\n", ssoType)
			}
			if srv.Issuer != "" {
				authPrint("  Issuer:   %s\n", srv.Issuer)
			}
			if srv.AuthTool != "" && srv.Status == pkgoauth.ServerStatusAuthRequired {
				authPrint("  Action:   Run: muster auth login --server %s\n", srv.Name)
			}
			return nil
		}
	}

	return fmt.Errorf("server '%s' not found. Use 'muster auth status' to see available servers", serverName)
}

// printMCPServerStatuses prints the status of all MCP servers.
func printMCPServerStatuses(servers []pkgoauth.ServerAuthStatus) {
	// Count servers requiring auth and find longest name for alignment
	var pendingCount int
	var maxNameLen int
	var maxSSOLen int
	for _, srv := range servers {
		if srv.Status == pkgoauth.ServerStatusAuthRequired {
			pendingCount++
		}
		if len(srv.Name) > maxNameLen {
			maxNameLen = len(srv.Name)
		}
		if ssoType := getSSOType(srv); len(ssoType) > maxSSOLen {
			maxSSOLen = len(ssoType)
		}
	}

	// Set minimum width for alignment, but allow expansion for long names
	if maxNameLen < minNameColumnWidth {
		maxNameLen = minNameColumnWidth
	}

	if pendingCount > 0 {
		fmt.Printf("  (%d pending authentication)\n", pendingCount)
	}

	for _, srv := range servers {
		statusStr := formatMCPServerStatus(srv.Status)
		ssoType := getSSOType(srv)

		// Build the SSO label with consistent width padding
		var ssoLabel string
		if ssoType != "" {
			// Pad SSO type to max width for alignment
			ssoLabel = fmt.Sprintf(" [%-*s]", maxSSOLen, ssoType)
		} else if maxSSOLen > 0 {
			// Add empty space to maintain alignment when other servers have SSO
			ssoLabel = fmt.Sprintf("  %*s ", maxSSOLen, "")
		}

		if srv.Status == pkgoauth.ServerStatusAuthRequired && srv.AuthTool != "" {
			fmt.Printf("  %-*s %s%s  Run: muster auth login --server %s\n", maxNameLen, srv.Name, statusStr, ssoLabel, srv.Name)
		} else {
			fmt.Printf("  %-*s %s%s\n", maxNameLen, srv.Name, statusStr, ssoLabel)
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

// ssoLabelForwarded is the label shown for SSO via token forwarding.
const ssoLabelForwarded = "SSO: Forwarded"

// ssoLabelShared is the label shown for SSO via token reuse (shared login).
const ssoLabelShared = "SSO: Shared"

// ssoLabelFailed is the label shown when SSO was attempted but failed.
const ssoLabelFailed = "SSO failed"

// getSSOType returns a human-readable SSO type for the server.
// Returns empty string if no SSO mechanism is applicable.
//
// SSO mechanisms:
//   - "SSO: Forwarded": Muster forwards its ID token to this server
//   - "SSO: Shared": Server shares an OAuth issuer with other servers (shared login)
//   - "SSO failed": SSO was attempted but failed (token rejected)
func getSSOType(srv pkgoauth.ServerAuthStatus) string {
	// Show failure indicator if SSO was attempted but failed
	if srv.SSOAttemptFailed {
		return ssoLabelFailed
	}
	if srv.TokenForwardingEnabled {
		return ssoLabelForwarded
	}
	// Only show Shared if SSO is enabled and server has an issuer
	if srv.TokenReuseEnabled && srv.Issuer != "" {
		return ssoLabelShared
	}
	return ""
}
