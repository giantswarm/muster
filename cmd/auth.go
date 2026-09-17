package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
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
  muster auth whoami                   # Show current identity
  muster auth token --id               # Print the ID token for other clients`,
}

// authLogoutCmd represents the auth logout command
var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear stored authentication tokens",
	Long: `Clear stored OAuth tokens.

This command removes cached authentication tokens, requiring you to
re-authenticate on the next connection to protected endpoints.

With --server the command signs the session out of one MCP server at the
aggregator and nothing else: the server's tools are hidden for this session
until 'muster auth login --server <name>', and the session stays signed in to
the aggregator. Without --server the stored token for the aggregator is
removed, which ends the session with every server.

Examples:
  muster auth logout                   # Logout from configured aggregator
  muster auth logout --endpoint <url>  # Logout from specific endpoint
  muster auth logout -s <name>         # Disconnect one MCP server, stay signed in
  muster auth logout --all             # Clear all stored tokens
  muster auth logout --all --yes       # Clear all without confirmation`,
	RunE: runAuthLogout,
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
	authCmd.AddCommand(authWhoamiCmd)
	authCmd.AddCommand(authTokenCmd)

	// Common flags for auth commands (shared across subcommands)
	authCmd.PersistentFlags().StringVar(&authEndpoint, "endpoint", "", "Specific endpoint URL to authenticate to")
	authCmd.PersistentFlags().StringVar(&authContext, "context", "", "Use a specific context (env: MUSTER_CONTEXT)")
	authCmd.PersistentFlags().StringVar(&authConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
	authCmd.PersistentFlags().BoolVarP(&authQuiet, "quiet", "q", false, "Suppress non-essential output")

	// Logout-specific flags (only on logout subcommand)
	authLogoutCmd.Flags().BoolVar(&logoutAll, "all", false, "Clear all stored tokens")
	authLogoutCmd.Flags().BoolVarP(&logoutYes, "yes", "y", false, "Skip confirmation prompt for --all")
	authLogoutCmd.Flags().StringVarP(&logoutServer, "server", "s", "", "MCP server to sign out of for this session (the aggregator session stays)")
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

	// Determine which aggregator the logout is about
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

	// --server signs the session out of one MCP server at that aggregator
	if logoutServer != "" {
		return logoutFromMCPServer(cmd.Context(), handler, endpoint, logoutServer)
	}

	if err := handler.Logout(endpoint); err != nil {
		return fmt.Errorf("failed to logout: %w", err)
	}

	authPrint("Logged out from %s\n", endpoint)
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
	if !status.IDTokenExpiresAt.IsZero() {
		fmt.Printf("ID token:  %s\n", formatIDTokenExpiry(status.IDTokenExpiresAt, time.Now()))
	}

	return nil
}

// logoutFromMCPServer signs the CLI's session out of one MCP server at the
// aggregator (core_auth_logout): the server's authenticated mark, cached tools
// and pooled connection go for this session, and the session's own sign-in to
// the aggregator stays -- the aggregator's answer is what the person reads.
// An SSO server (token forwarding, token exchange) is connected from the
// session's muster token and has no sign-out of its own; the command explains
// that instead. Without a session at the aggregator there is nothing to sign
// out of, and no browser flow is started for a logout.
func logoutFromMCPServer(ctx context.Context, handler api.AuthHandler, endpoint, serverName string) error {
	handler.InvalidateCache(endpoint)
	client, err := createConnectedClient(ctx, endpoint)
	if err != nil {
		if pkgoauth.IsOAuthUnauthorizedError(err) {
			authPrint("Not authenticated to %s; there is no session to sign '%s' out of.\n", endpoint, serverName)
			return nil
		}
		return err
	}
	defer func() { _ = client.Close() }()

	authStatus, err := parseAuthStatusResource(ctx, client)
	if err != nil {
		return err
	}
	server := findServerAuthStatus(authStatus, serverName)
	if server == nil {
		return fmt.Errorf("server '%s' not found. Use 'muster auth status' to see available servers", serverName)
	}
	if guidance := ssoLogoutGuidance(*server); guidance != "" {
		authPrint("%s", guidance)
		return nil
	}

	result, err := client.CallTool(ctx, "core_auth_logout", map[string]any{"server": serverName})
	if err != nil {
		return fmt.Errorf("failed to sign out of server '%s': %w", serverName, err)
	}
	said := toolResultText(result)
	if result.IsError {
		return fmt.Errorf("could not sign out of server '%s': %s", serverName, said)
	}
	authPrint("%s", mcpServerLogoutSummary(serverName, endpoint, said))
	return nil
}

// findServerAuthStatus returns the auth status of one server from the
// aggregator's auth://status answer, or nil when the server is not listed.
func findServerAuthStatus(authStatus *pkgoauth.AuthStatusResponse, serverName string) *pkgoauth.ServerAuthStatus {
	if authStatus == nil {
		return nil
	}
	for i := range authStatus.Servers {
		if authStatus.Servers[i].Name == serverName {
			return &authStatus.Servers[i]
		}
	}
	return nil
}

// ssoLogoutGuidance explains why a server connected through SSO cannot be
// signed out of on its own and what disconnects it; empty for a server the
// person signs in to with 'muster auth login --server'.
func ssoLogoutGuidance(server pkgoauth.ServerAuthStatus) string {
	switch {
	case server.TokenExchangeEnabled:
		return fmt.Sprintf(`Server '%s' uses SSO via Token Exchange.

This server uses RFC 8693 Token Exchange. muster exchanges its token
for one valid on the remote cluster's Identity Provider.

To disconnect, log out from muster:
  muster auth logout
`, server.Name)
	case server.TokenForwardingEnabled:
		return fmt.Sprintf(`Server '%s' uses SSO via Token Forwarding.

This server automatically receives your muster ID token. You authenticated
once to muster, and that identity is forwarded to this server.

To disconnect, log out from muster:
  muster auth logout
`, server.Name)
	}
	return ""
}

// mcpServerLogoutSummary words a completed per-server sign-out for the
// terminal: what happened to this session, what the aggregator added about
// the person's grant (a subject-scoped grant is revoked for every session of
// the person, and the servers sharing it are named), and how to reconnect.
func mcpServerLogoutSummary(serverName, endpoint, aggregatorAnswer string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Signed out of server '%s' for this session; you stay signed in to %s.\n", serverName, endpoint)
	for _, paragraph := range strings.Split(aggregatorAnswer, "\n\n") {
		if strings.Contains(paragraph, "revoked for all your sessions") {
			b.WriteString(strings.TrimSpace(paragraph) + "\n")
		}
	}
	fmt.Fprintf(&b, "To reconnect: muster auth login --server %s\n", serverName)
	return b.String()
}

// toolResultText joins the text parts of a tool result.
func toolResultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			parts = append(parts, textContent.Text)
		}
	}
	return strings.Join(parts, "\n")
}
