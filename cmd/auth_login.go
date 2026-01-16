package cmd

import (
	"context"
	"fmt"

	"muster/internal/api"
	pkgoauth "muster/pkg/oauth"

	"github.com/spf13/cobra"
)

// Login-specific flags
var (
	loginAll    bool
	loginServer string
)

// authLoginCmd represents the auth login command
var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate to a muster aggregator",
	Long: `Authenticate to a muster aggregator using OAuth.

This command initiates an OAuth browser-based authentication flow to obtain
access tokens for connecting to OAuth-protected muster aggregators.

Examples:
  muster auth login                    # Login to configured aggregator
  muster auth login --endpoint <url>   # Login to specific endpoint
  muster auth login --server <name>    # Login to specific MCP server
  muster auth login --all              # Login to aggregator + all pending MCP servers`,
	RunE: runAuthLogin,
}

func init() {
	// Login-specific flags (only on login subcommand)
	authLoginCmd.Flags().BoolVar(&loginAll, "all", false, "Login to aggregator and all pending MCP servers")
	authLoginCmd.Flags().StringVar(&loginServer, "server", "", "MCP server name (managed by aggregator) to authenticate to")
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
	if loginServer != "" {
		return loginToMCPServer(ctx, handler, endpoint, loginServer)
	}

	// Handle --all flag: authenticate to aggregator + all pending MCP servers
	if loginAll {
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
		if !authQuiet {
			fmt.Println("Authenticating to aggregator first...")
		}
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

	if serverInfo.Status != pkgoauth.ServerStatusAuthRequired {
		if serverInfo.Status == pkgoauth.ServerStatusConnected {
			if !authQuiet {
				fmt.Printf("Server '%s' is already connected and does not require authentication.\n", serverName)
			}
			return nil
		}
		if !authQuiet {
			fmt.Printf("Server '%s' is in state '%s' and cannot be authenticated.\n", serverName, serverInfo.Status)
		}
		return nil
	}

	if serverInfo.AuthTool == "" {
		return fmt.Errorf("server '%s' requires authentication but no auth tool is available", serverName)
	}

	// Call the auth tool and wait for completion
	if !authQuiet {
		fmt.Printf("Authenticating to %s...\n", serverName)
	}
	return triggerMCPServerAuthWithWait(ctx, handler, aggregatorEndpoint, serverName, serverInfo.AuthTool, DefaultAuthWaitConfig())
}

// loginToAll authenticates to the aggregator and all pending MCP servers.
func loginToAll(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint string) error {
	// Login to aggregator first
	if !authQuiet {
		fmt.Printf("Authenticating to aggregator (%s)...\n", aggregatorEndpoint)
	}
	if err := handler.Login(ctx, aggregatorEndpoint); err != nil {
		return fmt.Errorf("failed to authenticate to aggregator: %w", err)
	}
	if !authQuiet {
		fmt.Println("done")
	}

	// Get auth status from aggregator
	authStatus, err := getAuthStatusFromAggregator(ctx, handler, aggregatorEndpoint)
	if err != nil {
		if !authQuiet {
			fmt.Printf("\nWarning: Could not get MCP server status: %v\n", err)
			fmt.Println("Aggregator authentication complete.")
		}
		return nil
	}

	// Find all servers requiring authentication
	var pendingServers []pkgoauth.ServerAuthStatus
	for _, srv := range authStatus.Servers {
		if srv.Status == pkgoauth.ServerStatusAuthRequired && srv.AuthTool != "" {
			pendingServers = append(pendingServers, srv)
		}
	}

	if len(pendingServers) == 0 {
		if !authQuiet {
			fmt.Println("\nNo MCP servers require authentication.")
			fmt.Println("All authentication complete.")
		}
		return nil
	}

	if !authQuiet {
		fmt.Printf("\nFound %d MCP server(s) requiring authentication:\n", len(pendingServers))
		for _, srv := range pendingServers {
			fmt.Printf("  - %s\n", srv.Name)
		}
		fmt.Println()
	}

	// Authenticate to each server sequentially, waiting for each to complete
	// This ensures SSO cookies are available for subsequent flows
	waitCfg := DefaultAuthWaitConfig()
	successCount := 0
	for i, srv := range pendingServers {
		if !authQuiet {
			fmt.Printf("[%d/%d] Authenticating to %s\n", i+1, len(pendingServers), srv.Name)
		}
		if err := triggerMCPServerAuthWithWait(ctx, handler, aggregatorEndpoint, srv.Name, srv.AuthTool, waitCfg); err != nil {
			if !authQuiet {
				fmt.Printf("  Failed: %v\n", err)
			}
		} else {
			successCount++
		}
	}

	if !authQuiet {
		fmt.Printf("\nAuthentication complete. %d/%d servers authenticated.\n", successCount, len(pendingServers))
	}
	return nil
}
