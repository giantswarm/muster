package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"muster/internal/agent"
	"muster/internal/api"
	"muster/internal/cli"
	"muster/internal/config"
	pkgoauth "muster/pkg/oauth"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/mark3labs/mcp-go/mcp"
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
	} else {
		// Use configured aggregator endpoint
		endpoint, err = getEndpointFromConfig()
		if err != nil {
			return err
		}
	}

	// Handle --server flag: authenticate to a specific MCP server
	if authServer != "" {
		return loginToMCPServer(ctx, handler, endpoint, authServer)
	}

	// Handle --all flag: authenticate to aggregator + all pending MCP servers
	if authAll {
		return loginToAll(ctx, handler, endpoint)
	}

	// Single aggregator login
	return handler.Login(ctx, endpoint)
}

// loginToMCPServer authenticates to a specific MCP server through the aggregator.
// It queries the auth://status resource to find the server's auth tool and invokes it.
func loginToMCPServer(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint, serverName string) error {
	// First ensure we're authenticated to the aggregator
	if !handler.HasValidToken(aggregatorEndpoint) {
		fmt.Println("Authenticating to aggregator first...")
		if err := handler.Login(ctx, aggregatorEndpoint); err != nil {
			return fmt.Errorf("failed to authenticate to aggregator: %w", err)
		}
	}

	// Get auth status from aggregator
	authStatus, err := getAuthStatusFromAggregator(ctx, handler, aggregatorEndpoint)
	if err != nil {
		return fmt.Errorf("failed to get auth status: %w", err)
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

	if serverInfo.Status != "auth_required" {
		if serverInfo.Status == "connected" {
			fmt.Printf("Server '%s' is already connected and does not require authentication.\n", serverName)
			return nil
		}
		fmt.Printf("Server '%s' is in state '%s' and cannot be authenticated.\n", serverName, serverInfo.Status)
		return nil
	}

	if serverInfo.AuthTool == "" {
		return fmt.Errorf("server '%s' requires authentication but no auth tool is available", serverName)
	}

	// Call the auth tool to get the auth URL
	fmt.Printf("Authenticating to %s...\n", serverName)
	return triggerMCPServerAuth(ctx, handler, aggregatorEndpoint, serverName, serverInfo.AuthTool)
}

// loginToAll authenticates to the aggregator and all pending MCP servers.
func loginToAll(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint string) error {
	// Login to aggregator first
	fmt.Printf("Authenticating to aggregator (%s)...\n", aggregatorEndpoint)
	if err := handler.Login(ctx, aggregatorEndpoint); err != nil {
		return fmt.Errorf("failed to authenticate to aggregator: %w", err)
	}
	fmt.Println("done")

	// Get auth status from aggregator
	authStatus, err := getAuthStatusFromAggregator(ctx, handler, aggregatorEndpoint)
	if err != nil {
		fmt.Printf("\nWarning: Could not get MCP server status: %v\n", err)
		fmt.Println("Aggregator authentication complete.")
		return nil
	}

	// Find all servers requiring authentication
	var pendingServers []pkgoauth.ServerAuthStatus
	for _, srv := range authStatus.Servers {
		if srv.Status == "auth_required" && srv.AuthTool != "" {
			pendingServers = append(pendingServers, srv)
		}
	}

	if len(pendingServers) == 0 {
		fmt.Println("\nNo MCP servers require authentication.")
		fmt.Println("All authentication complete.")
		return nil
	}

	fmt.Printf("\nFound %d MCP server(s) requiring authentication:\n", len(pendingServers))
	for _, srv := range pendingServers {
		fmt.Printf("  - %s\n", srv.Name)
	}
	fmt.Println()

	// Authenticate to each server
	successCount := 0
	for i, srv := range pendingServers {
		fmt.Printf("[%d/%d] Authenticating to %s...\n", i+1, len(pendingServers), srv.Name)
		if err := triggerMCPServerAuth(ctx, handler, aggregatorEndpoint, srv.Name, srv.AuthTool); err != nil {
			fmt.Printf("  Failed: %v\n", err)
		} else {
			successCount++
		}
		// Small delay between auth flows to allow SSO redirects to complete
		if i < len(pendingServers)-1 {
			time.Sleep(2 * time.Second)
		}
	}

	fmt.Printf("\nAuthentication complete. %d/%d servers authenticated.\n", successCount, len(pendingServers))
	return nil
}

// getAuthStatusFromAggregator queries the auth://status resource from the aggregator.
func getAuthStatusFromAggregator(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint string) (*pkgoauth.AuthStatusResponse, error) {
	// Create a client to connect to the aggregator
	logger := agent.NewDevNullLogger()
	transport := agent.TransportStreamableHTTP
	if strings.HasSuffix(aggregatorEndpoint, "/sse") {
		transport = agent.TransportSSE
	}

	client := agent.NewClient(aggregatorEndpoint, logger, transport)

	// Set auth token if available
	token, err := handler.GetBearerToken(aggregatorEndpoint)
	if err == nil && token != "" {
		client.SetAuthorizationHeader(token)
	}

	// Connect to aggregator
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to aggregator: %w", err)
	}
	defer client.Close()

	// Initialize client (required for resource operations)
	if err := client.InitializeAndLoadData(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize client: %w", err)
	}

	// Get the auth://status resource
	result, err := client.GetResource(ctx, "auth://status")
	if err != nil {
		return nil, fmt.Errorf("failed to get auth://status resource: %w", err)
	}

	if len(result.Contents) == 0 {
		return nil, fmt.Errorf("auth://status resource returned no content")
	}

	// Parse the response
	var responseText string
	for _, content := range result.Contents {
		if textContent, ok := mcp.AsTextResourceContents(content); ok {
			responseText = textContent.Text
			break
		}
	}

	if responseText == "" {
		return nil, fmt.Errorf("auth://status resource returned no text content")
	}

	var authStatus pkgoauth.AuthStatusResponse
	if err := json.Unmarshal([]byte(responseText), &authStatus); err != nil {
		return nil, fmt.Errorf("failed to parse auth status: %w", err)
	}

	return &authStatus, nil
}

// triggerMCPServerAuth triggers the OAuth flow for an MCP server by calling its auth tool.
func triggerMCPServerAuth(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint, serverName, authTool string) error {
	// Create a client to connect to the aggregator
	logger := agent.NewDevNullLogger()
	transport := agent.TransportStreamableHTTP
	if strings.HasSuffix(aggregatorEndpoint, "/sse") {
		transport = agent.TransportSSE
	}

	client := agent.NewClient(aggregatorEndpoint, logger, transport)

	// Set auth token if available
	token, err := handler.GetBearerToken(aggregatorEndpoint)
	if err == nil && token != "" {
		client.SetAuthorizationHeader(token)
	}

	// Connect to aggregator
	if err := client.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to aggregator: %w", err)
	}
	defer client.Close()

	// Initialize client
	if err := client.InitializeAndLoadData(ctx); err != nil {
		return fmt.Errorf("failed to initialize client: %w", err)
	}

	// Call the auth tool
	result, err := client.CallTool(ctx, authTool, nil)
	if err != nil {
		return fmt.Errorf("failed to call auth tool: %w", err)
	}

	// Extract the auth URL from the result
	authURL := extractAuthURL(result)
	if authURL == "" {
		return fmt.Errorf("auth tool did not return an auth URL")
	}

	// Open browser for authentication
	fmt.Printf("Opening browser for authentication...\n")
	fmt.Printf("If the browser doesn't open, visit:\n  %s\n\n", authURL)

	if err := openBrowserForAuth(authURL); err != nil {
		fmt.Printf("Failed to open browser: %v\n", err)
		fmt.Printf("Please open the URL manually: %s\n", authURL)
	}

	return nil
}

// extractAuthURL extracts the authentication URL from a tool call result.
func extractAuthURL(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}

	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			// Try to parse as JSON first
			var authResp struct {
				AuthURL string `json:"auth_url"`
			}
			if err := json.Unmarshal([]byte(textContent.Text), &authResp); err == nil && authResp.AuthURL != "" {
				return authResp.AuthURL
			}

			// Fallback: look for URL pattern in the text
			lines := strings.Split(textContent.Text, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
					return line
				}
			}
		}
	}

	return ""
}

// openBrowserForAuth opens the browser for OAuth authentication.
func openBrowserForAuth(url string) error {
	// Import the oauth package's OpenBrowser function
	return agent.OpenBrowserForAuth(url)
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

func runAuthStatus(cmd *cobra.Command, args []string) error {
	handler, err := ensureAuthHandler()
	if err != nil {
		return err
	}

	// Get the aggregator endpoint
	aggregatorEndpoint, err := getEndpointFromConfig()
	if err != nil {
		return err
	}

	// If specific endpoint is requested
	if authEndpoint != "" {
		status := handler.GetStatusForEndpoint(authEndpoint)
		return printAuthStatus(status)
	}

	// If specific server is requested, show that server's status
	if authServer != "" {
		return showMCPServerStatus(cmd.Context(), handler, aggregatorEndpoint, authServer)
	}

	// Show aggregator status
	fmt.Println("Muster Aggregator")
	status := handler.GetStatusForEndpoint(aggregatorEndpoint)
	if status != nil {
		fmt.Printf("  Endpoint:  %s\n", aggregatorEndpoint)
		if status.Authenticated {
			fmt.Printf("  Status:    %s\n", text.FgGreen.Sprint("Authenticated"))
			if !status.ExpiresAt.IsZero() {
				remaining := time.Until(status.ExpiresAt)
				if remaining > 0 {
					fmt.Printf("  Expires:   in %s\n", formatDuration(remaining))
				} else {
					fmt.Printf("  Expires:   %s\n", text.FgYellow.Sprint("Expired"))
				}
			}
		} else {
			// Check if auth is required
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			authRequired, _ := handler.CheckAuthRequired(ctx, aggregatorEndpoint)
			cancel()

			if authRequired {
				fmt.Printf("  Status:    %s\n", text.FgYellow.Sprint("Not authenticated"))
				fmt.Printf("             Run: muster auth login\n")
			} else {
				fmt.Printf("  Status:    %s\n", text.FgHiBlack.Sprint("No authentication required"))
			}
		}
	}

	// Try to get MCP server status from the aggregator
	if handler.HasValidToken(aggregatorEndpoint) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		authStatus, err := getAuthStatusFromAggregator(ctx, handler, aggregatorEndpoint)
		if err == nil && len(authStatus.Servers) > 0 {
			fmt.Println("\nMCP Servers")
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
			fmt.Printf("\nMCP Server: %s\n", srv.Name)
			fmt.Printf("  Status:   %s\n", formatMCPServerStatus(srv.Status))
			if srv.Issuer != "" {
				fmt.Printf("  Issuer:   %s\n", srv.Issuer)
			}
			if srv.AuthTool != "" && srv.Status == "auth_required" {
				fmt.Printf("  Action:   Run: muster auth login --server %s\n", srv.Name)
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
		if srv.Status == "auth_required" {
			pendingCount++
		}
	}

	if pendingCount > 0 {
		fmt.Printf("  (%d pending authentication)\n", pendingCount)
	}

	for _, srv := range servers {
		statusStr := formatMCPServerStatus(srv.Status)
		if srv.Status == "auth_required" && srv.AuthTool != "" {
			fmt.Printf("  %-20s %s   Run: muster auth login --server %s\n", srv.Name, statusStr, srv.Name)
		} else {
			fmt.Printf("  %-20s %s\n", srv.Name, statusStr)
		}
	}
}

// formatMCPServerStatus formats the MCP server status with colors.
func formatMCPServerStatus(status string) string {
	switch status {
	case "connected":
		return text.FgGreen.Sprint("Connected")
	case "auth_required":
		return text.FgYellow.Sprint("Not authenticated")
	case "disconnected":
		return text.FgRed.Sprint("Disconnected")
	case "error":
		return text.FgRed.Sprint("Error")
	default:
		return text.FgHiBlack.Sprint(status)
	}
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
