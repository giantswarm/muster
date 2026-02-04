package cmd

import (
	"context"
	"fmt"
	"strings"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/cli"

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
	authPrint("  Endpoint:  %s\n", aggregatorEndpoint)

	// Get local token status first
	localStatus := handler.GetStatusForEndpoint(aggregatorEndpoint)

	// If we have a local token, verify it with the server before showing "Authenticated"
	if localStatus != nil && localStatus.Authenticated {
		return showVerifiedAuthStatus(handler, aggregatorEndpoint, localStatus)
	}

	// No local token - check if auth is required
	return showUnauthenticatedStatus(handler, aggregatorEndpoint)
}

// showVerifiedAuthStatus verifies the token with the server and displays the authenticated status.
// This catches the case where the token was invalidated server-side (e.g., IdP revoked
// the session) but the local expiry time hasn't passed yet.
func showVerifiedAuthStatus(handler api.AuthHandler, endpoint string, localStatus *api.AuthStatus) error {
	// Try to make an actual request to the server to verify the token
	ctx, cancel := context.WithTimeout(context.Background(), DefaultStatusCheckTimeout)
	authStatus, serverErr := getAuthStatusFromAggregator(ctx, handler, endpoint)
	cancel()

	if serverErr != nil {
		return handleServerVerificationError(handler, endpoint, localStatus, serverErr)
	}

	// Server verified the token - show authenticated status
	printAuthenticatedStatus(localStatus)

	// Show MCP server status since we already have it
	if len(authStatus.Servers) > 0 {
		authPrintln("\nMCP Servers")
		printMCPServerStatuses(authStatus.Servers)
	}

	return nil
}

// handleServerVerificationError handles errors from server-side token verification.
func handleServerVerificationError(handler api.AuthHandler, endpoint string, localStatus *api.AuthStatus, serverErr error) error {
	// Check if this is a 401 error (token invalidated server-side)
	if pkgoauth.Is401Error(serverErr) {
		_ = handler.Logout(endpoint)
		authPrint("  Status:    %s\n", text.FgYellow.Sprint("Token invalidated"))
		authPrint("             Your session was terminated by the identity provider.\n")
		authPrint("             Run: muster auth login\n")
		return nil
	}

	// Other connection error - might be network issue, server down, etc.
	printConnectionError(serverErr, endpoint)

	// Still show local token info as it might be useful
	if !localStatus.ExpiresAt.IsZero() {
		authPrint("  (Local token expires: %s)\n", formatExpiryWithDirection(localStatus.ExpiresAt))
	}
	return nil
}

// printAuthenticatedStatus prints the status for a verified authenticated session.
func printAuthenticatedStatus(localStatus *api.AuthStatus) {
	authPrint("  Status:    %s\n", text.FgGreen.Sprint("Authenticated"))
	if !localStatus.ExpiresAt.IsZero() {
		authPrint("  Expires:   %s\n", formatExpiryWithDirection(localStatus.ExpiresAt))
	}
	if localStatus.HasRefreshToken {
		authPrint("  Refresh:   %s\n", text.FgGreen.Sprint("Available"))
	} else {
		authPrint("  Refresh:   %s\n", text.FgYellow.Sprint("Not available (re-auth required on expiry)"))
	}
	if localStatus.IssuerURL != "" {
		authPrint("  Issuer:    %s\n", localStatus.IssuerURL)
	}
}

// showUnauthenticatedStatus checks if auth is required and shows appropriate status.
func showUnauthenticatedStatus(handler api.AuthHandler, endpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), ShortAuthCheckTimeout)
	authRequired, checkErr := handler.CheckAuthRequired(ctx, endpoint)
	cancel()

	if checkErr != nil {
		printConnectionError(checkErr, endpoint)
		return nil
	}

	if authRequired {
		authPrint("  Status:    %s\n", text.FgYellow.Sprint("Not authenticated"))
		authPrint("             Run: muster auth login\n")
	} else {
		authPrint("  Status:    %s\n", text.FgHiBlack.Sprint("No authentication required"))
	}
	return nil
}

// printConnectionError prints a formatted connection error message.
func printConnectionError(err error, endpoint string) {
	connErr := cli.ClassifyConnectionError(err, endpoint)
	authPrint("  Status:    %s\n", text.FgRed.Sprint("Connection failed"))
	authPrint("             %s: %s\n", connErr.Type, formatConnectionErrorReason(err))
}

// showMCPServerStatus shows the authentication status of a specific MCP server.
func showMCPServerStatus(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint, serverName string) error {
	// Need to be authenticated to the aggregator first
	if !handler.HasValidToken(aggregatorEndpoint) {
		return fmt.Errorf("not authenticated to aggregator. Run 'muster auth login' first")
	}

	authStatus, err := getAuthStatusFromAggregator(ctx, handler, aggregatorEndpoint)
	if err != nil {
		// Check if this is a 401 error (token invalidated server-side)
		if pkgoauth.Is401Error(err) {
			_ = handler.Logout(aggregatorEndpoint)
			return fmt.Errorf("your session was terminated by the identity provider. Run: muster auth login")
		}
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
	// Separate reachable servers from unreachable ones
	var reachableServers []pkgoauth.ServerAuthStatus
	var unreachableCount int

	for _, srv := range servers {
		if srv.Status == pkgoauth.ServerStatusUnreachable {
			unreachableCount++
		} else {
			reachableServers = append(reachableServers, srv)
		}
	}

	// Count servers requiring auth and find longest name for alignment
	var pendingCount int
	var maxNameLen int
	var maxSSOLen int
	for _, srv := range reachableServers {
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

	for _, srv := range reachableServers {
		statusStr := formatMCPServerStatus(srv.Status)
		ssoType := getSSOType(srv)

		// Build the SSO label with consistent width padding
		var ssoLabel string
		if ssoType != "" {
			// Pad SSO type to max width for alignment, with "SSO:" prefix for context
			ssoLabel = fmt.Sprintf(" [SSO: %-*s]", maxSSOLen, ssoType)
		} else if maxSSOLen > 0 {
			// Add empty space to maintain alignment when other servers have SSO
			// Account for "SSO: " prefix (5 chars) in the padding
			ssoLabel = fmt.Sprintf("  %*s ", maxSSOLen+5, "")
		}

		if srv.Status == pkgoauth.ServerStatusAuthRequired && srv.AuthTool != "" {
			fmt.Printf("  %-*s %s%s  Run: muster auth login --server %s\n", maxNameLen, srv.Name, statusStr, ssoLabel, srv.Name)
		} else {
			fmt.Printf("  %-*s %s%s\n", maxNameLen, srv.Name, statusStr, ssoLabel)
		}
	}

	// Show summary for unreachable servers (don't show auth prompts for them)
	if unreachableCount > 0 {
		fmt.Printf("\n  (%d unreachable - not shown)\n", unreachableCount)
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
	case pkgoauth.ServerStatusUnreachable:
		return text.FgHiBlack.Sprint("Unreachable")
	default:
		return text.FgHiBlack.Sprint(status)
	}
}

// ssoLabelForwarded is the label shown for SSO via token forwarding.
const ssoLabelForwarded = "Forwarded"

// ssoLabelExchanged is the label shown for SSO via token exchange.
const ssoLabelExchanged = "Exchanged"

// ssoLabelFailed is the label shown when SSO was attempted but failed.
const ssoLabelFailed = "Failed"

// getSSOType returns a human-readable SSO type for the server.
// Returns empty string if no SSO mechanism is applicable.
//
// SSO mechanisms:
//   - "Forwarded": Muster forwards its ID token to this server
//   - "Exchanged": Muster exchanges its token for one valid on the remote IdP
//   - "Failed": SSO was attempted but failed (token rejected)
func getSSOType(srv pkgoauth.ServerAuthStatus) string {
	// Show failure indicator if SSO was attempted but failed
	if srv.SSOAttemptFailed {
		return ssoLabelFailed
	}
	if srv.TokenExchangeEnabled {
		return ssoLabelExchanged
	}
	if srv.TokenForwardingEnabled {
		return ssoLabelForwarded
	}
	return ""
}

// formatConnectionErrorReason extracts a concise reason from a connection error.
// It removes verbose prefixes and presents the core issue.
func formatConnectionErrorReason(err error) string {
	if err == nil {
		return "unknown error"
	}

	errStr := err.Error()

	// Extract the most relevant part of the error message
	// TLS errors often have verbose prefixes like "Get https://...: x509: ..."
	if idx := strings.Index(errStr, "x509:"); idx != -1 {
		return strings.TrimSpace(errStr[idx:])
	}

	// For connection errors, extract the actual failure reason
	if idx := strings.Index(errStr, "connect:"); idx != -1 {
		return strings.TrimSpace(errStr[idx:])
	}

	// For dial errors, extract the core message
	if colonIdx := strings.LastIndex(errStr, ":"); strings.Contains(errStr, "dial tcp") && colonIdx != -1 {
		return strings.TrimSpace(errStr[colonIdx+1:])
	}

	// Return the error as-is if no simplification applies
	return errStr
}
