package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"muster/internal/agent"
	"muster/internal/api"
	"muster/internal/cli"
	"muster/internal/config"
	pkgoauth "muster/pkg/oauth"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/mark3labs/mcp-go/mcp"
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

// getEndpointFromConfig returns the aggregator endpoint from config.
func getEndpointFromConfig() (string, error) {
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

// triggerMCPServerAuth triggers the OAuth flow for an MCP server by calling its auth tool.
func triggerMCPServerAuth(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint, serverName, authTool string) error {
	client, err := createConnectedClient(ctx, handler, aggregatorEndpoint)
	if err != nil {
		return err
	}
	defer client.Close()

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

	// Try to open browser first, only show URL if it fails
	if !authQuiet {
		fmt.Print("Opening browser for authentication...")
	}

	err = openBrowserForAuth(authURL)
	if err != nil {
		if !authQuiet {
			fmt.Println(" failed")
			fmt.Printf("Please open this URL in your browser:\n  %s\n\n", authURL)
		}
	} else {
		if !authQuiet {
			fmt.Println(" done")
		}
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

// truncateURL truncates a URL to a maximum length while keeping the hostname visible.
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
