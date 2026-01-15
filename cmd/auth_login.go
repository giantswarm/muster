package cmd

import (
	"context"
	"fmt"
	"time"

	"muster/internal/api"
	pkgoauth "muster/pkg/oauth"

	"github.com/spf13/cobra"
)

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
