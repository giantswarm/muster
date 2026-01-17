package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"muster/internal/agent"
	"muster/internal/agent/oauth"
	"muster/internal/api"
	"muster/internal/cli"
	"muster/internal/config"
	pkgoauth "muster/pkg/oauth"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/mark3labs/mcp-go/mcp"
)

// Auth wait timeout constants.
const (
	// DefaultAuthWaitTimeout is the default timeout for waiting for authentication to complete.
	DefaultAuthWaitTimeout = 2 * time.Minute
	// DefaultAuthPollInterval is the default interval for polling authentication status.
	DefaultAuthPollInterval = 500 * time.Millisecond
	// DefaultStatusCheckTimeout is the timeout for checking connection status and fetching resources.
	DefaultStatusCheckTimeout = 10 * time.Second
	// ShortAuthCheckTimeout is a shorter timeout for quick auth requirement checks.
	ShortAuthCheckTimeout = 5 * time.Second
)

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

// getEndpointFromConfig returns the aggregator endpoint using context or config.
// It respects the precedence: --endpoint > --context > MUSTER_CONTEXT > current-context > config.
func getEndpointFromConfig() (string, error) {
	// Try to resolve endpoint from context first
	endpoint, err := cli.ResolveEndpoint(authEndpoint, authContext)
	if err != nil {
		return "", err
	}
	if endpoint != "" {
		return endpoint, nil
	}

	// Fall back to config-based resolution
	cfg, err := config.LoadConfig(authConfigPath)
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}
	return cli.GetAggregatorEndpoint(&cfg), nil
}

// createConnectedClient creates and connects an authenticated client to the aggregator.
// The caller is responsible for calling client.Close() when done.
func createConnectedClient(ctx context.Context, handler api.AuthHandler, endpoint string) (*agent.Client, error) {
	logger := agent.NewDevNullLogger()
	transport := agent.TransportStreamableHTTP
	if strings.HasSuffix(endpoint, "/sse") {
		transport = agent.TransportSSE
	}

	client := agent.NewClient(endpoint, logger, transport)

	// Set auth token if available
	token, err := handler.GetBearerToken(endpoint)
	if err == nil && token != "" {
		client.SetAuthorizationHeader(token)
	}

	// Set persistent session ID for MCP server token persistence
	// This enables tokens to persist across CLI invocations
	if sessionID := handler.GetSessionID(); sessionID != "" {
		client.SetHeader(api.ClientSessionIDHeader, sessionID)
	}

	// Connect to aggregator
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to aggregator: %w", err)
	}

	// Initialize client (required for resource operations)
	if err := client.InitializeAndLoadData(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to initialize client: %w", err)
	}

	return client, nil
}

// getAuthStatusFromAggregator queries the auth://status resource from the aggregator.
func getAuthStatusFromAggregator(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint string) (*pkgoauth.AuthStatusResponse, error) {
	client, err := createConnectedClient(ctx, handler, aggregatorEndpoint)
	if err != nil {
		return nil, err
	}
	defer client.Close()

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

// AuthWaitConfig configures how long to wait for authentication completion.
type AuthWaitConfig struct {
	// WaitForCompletion enables polling for auth completion.
	WaitForCompletion bool
	// Timeout is the maximum time to wait for completion.
	Timeout time.Duration
	// PollInterval is how often to check auth status.
	PollInterval time.Duration
}

// DefaultAuthWaitConfig returns a default configuration for auth waiting.
func DefaultAuthWaitConfig() AuthWaitConfig {
	return AuthWaitConfig{
		WaitForCompletion: true,
		Timeout:           DefaultAuthWaitTimeout,
		PollInterval:      DefaultAuthPollInterval,
	}
}

// triggerMCPServerAuthWithWait triggers the OAuth flow and optionally waits for completion.
func triggerMCPServerAuthWithWait(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint, serverName, authTool string, waitCfg AuthWaitConfig) error {
	client, err := createConnectedClient(ctx, handler, aggregatorEndpoint)
	if err != nil {
		return err
	}
	defer client.Close()

	// Call the auth tool with the server name as argument.
	// Per ADR-008, core_auth_login requires a "server" parameter to identify
	// which MCP server to authenticate to.
	args := map[string]interface{}{
		"server": serverName,
	}
	result, err := client.CallTool(ctx, authTool, args)
	if err != nil {
		return fmt.Errorf("failed to call auth tool: %w", err)
	}

	// Check if the response indicates already connected
	// This happens when the user has an existing session connection to this server
	if isAlreadyConnectedResponse(result) {
		authPrint("%s %s is already connected.\n", text.FgGreen.Sprint("✓"), serverName)
		authPrintln("Use 'muster auth logout --server " + serverName + "' to disconnect first if you want to re-authenticate with a different account.")
		return nil
	}

	// Extract the auth URL from the result
	authURL := extractAuthURL(result)
	if authURL == "" {
		return fmt.Errorf("auth tool did not return an auth URL")
	}

	// Try to open browser
	authPrintln("Opening browser for authentication...")

	err = openBrowserForAuth(authURL)
	if err != nil {
		authPrintln("Could not open browser automatically.")
		authPrint("\nPlease open this URL in your browser:\n  %s\n\n", authURL)
		// Continue - user can still manually open the URL
	}

	// If waiting is enabled, poll until completion using the SAME client.
	// This is critical because the OAuth callback updates the session associated with
	// this client's connection. Using a different client would create a new session
	// that doesn't have the authenticated connection.
	if waitCfg.WaitForCompletion {
		authPrint("Waiting for %s authentication to complete...\n", serverName)
		if err := waitForServerAuthWithClient(ctx, client, serverName, waitCfg); err != nil {
			return err
		}
		authPrint("%s %s authenticated successfully.\n", text.FgGreen.Sprint("✓"), serverName)
	}

	return nil
}

// waitForServerAuthWithClient polls the auth://status resource until the server is connected.
// It uses the provided client to maintain the same session that initiated the OAuth flow.
// This is critical because the OAuth callback updates the session associated with this
// client's connection - using a different client would check a different session.
func waitForServerAuthWithClient(ctx context.Context, client *agent.Client, serverName string, cfg AuthWaitConfig) error {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	pollInterval := cfg.PollInterval
	if pollInterval == 0 {
		pollInterval = 500 * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf(`timeout waiting for %s to authenticate

Please complete authentication in your browser, then run:
  muster auth login --server %s`, serverName, serverName)
		case <-ticker.C:
			status, err := getAuthStatusFromClient(ctx, client)
			if err != nil {
				// Transient error, keep polling
				continue
			}

			for _, srv := range status.Servers {
				if srv.Name == serverName {
					if srv.Status == pkgoauth.ServerStatusConnected {
						return nil
					}
					// Still waiting - auth_required or other state
					break
				}
			}
		}
	}
}

// getAuthStatusFromClient queries the auth://status resource using an existing client.
// This preserves the session ID, which is critical for checking per-session auth status.
func getAuthStatusFromClient(ctx context.Context, client *agent.Client) (*pkgoauth.AuthStatusResponse, error) {
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

// isAlreadyConnectedResponse checks if the auth tool result indicates the server is already connected.
// This happens in two cases:
// 1. The user has an existing session connection to THIS specific server
// 2. SSO token reuse succeeded - a token from another server with the same issuer was used
//
// In both cases, the server returns a success message without an auth URL,
// which we should handle gracefully instead of treating as an error.
func isAlreadyConnectedResponse(result *mcp.CallToolResult) bool {
	if result == nil || len(result.Content) == 0 {
		return false
	}

	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			// Check for various "already connected" or "successfully connected" markers
			// using constants defined in api.AuthMsg* for consistency
			msg := textContent.Text
			if strings.Contains(msg, api.AuthMsgAlreadyConnected) ||
				strings.Contains(msg, api.AuthMsgSuccessfullyConnected) ||
				strings.Contains(msg, api.AuthMsgAlreadyAuthenticated) {
				return true
			}
		}
	}

	return false
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
	return oauth.OpenBrowser(url)
}

// formatDuration formats a duration in a human-readable way.
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}
	if d < time.Minute {
		return "< 1 minute"
	}
	if d < time.Hour {
		minutes := int(d.Minutes())
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
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

// formatExpiryWithDirection formats a time as "in X" or "expired X ago".
func formatExpiryWithDirection(expiresAt time.Time) string {
	remaining := time.Until(expiresAt)
	if remaining > 0 {
		return "in " + formatDuration(remaining)
	}
	// Token is expired
	expiredAgo := -remaining
	return text.FgYellow.Sprintf("expired %s ago", formatDuration(expiredAgo))
}
