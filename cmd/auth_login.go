package cmd

import (
	"context"
	"fmt"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/api"

	"github.com/spf13/cobra"
)

// Login-specific flags
var (
	loginAll    bool
	loginServer string
	loginSilent bool
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
  muster auth login --all              # Login to aggregator + all pending MCP servers
  muster auth login --silent           # Attempt silent re-auth (requires IdP support)`,
	RunE: runAuthLogin,
}

func init() {
	// Login-specific flags (only on login subcommand)
	authLoginCmd.Flags().BoolVar(&loginAll, "all", false, "Login to aggregator and all pending MCP servers")
	authLoginCmd.Flags().StringVar(&loginServer, "server", "", "MCP server name (managed by aggregator) to authenticate to")
	authLoginCmd.Flags().BoolVar(&loginSilent, "silent", false, "Attempt silent re-auth using OIDC prompt=none (requires IdP support, not supported by Dex)")
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Silent refresh is disabled by default (Dex doesn't support prompt=none)
	// Use --silent flag to opt-in if your IdP supports it
	handler, err := ensureAuthHandlerWithOptions(AuthHandlerOptions{
		NoSilentRefresh: !loginSilent,
	})
	if err != nil {
		return err
	}

	// Determine which endpoint to authenticate to
	var endpoint string
	if authEndpoint != "" {
		endpoint = authEndpoint
	} else {
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

	// Before starting the browser flow, try connecting via mcp-go.
	// If the access token expired but a valid refresh token exists,
	// mcp-go's transport refreshes it transparently -- no browser needed.
	if tryMCPConnection(ctx, handler, endpoint) {
		authPrint("Already authenticated to %s\n", endpoint)
		return nil
	}

	return handler.Login(ctx, endpoint)
}

// loginToMCPServer authenticates to a specific MCP server through the aggregator.
// It queries the auth://status resource to find the server's auth tool and invokes it.
func loginToMCPServer(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint, serverName string) error {
	// Try fetching auth status directly -- the mcp-go transport handles token
	// refresh transparently, so this also serves as the connectivity check.
	authStatus, err := ensureAuthenticatedAndGetStatus(ctx, handler, aggregatorEndpoint)
	if err != nil {
		return err
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
			authPrint("Server '%s' is already connected and does not require authentication.\n", serverName)
			return nil
		}
		authPrint("Server '%s' is in state '%s' and cannot be authenticated.\n", serverName, serverInfo.Status)
		return nil
	}

	if serverInfo.AuthTool == "" {
		return fmt.Errorf("server '%s' requires authentication but no auth tool is available", serverName)
	}

	// Call the auth tool and wait for completion
	authPrint("Authenticating to %s...\n", serverName)
	return triggerMCPServerAuthWithWait(ctx, handler, aggregatorEndpoint, serverName, serverInfo.AuthTool, DefaultAuthWaitConfig())
}

// loginToAll authenticates to the aggregator and all pending MCP servers.
func loginToAll(ctx context.Context, handler api.AuthHandler, aggregatorEndpoint string) error {
	// Fetch auth status directly -- the mcp-go transport handles token
	// refresh transparently. Falls back to interactive login on 401.
	authStatus, err := ensureAuthenticatedAndGetStatus(ctx, handler, aggregatorEndpoint)
	if err != nil {
		return err
	}

	// Find all servers requiring authentication, excluding SSO servers
	// SSO servers (token forwarding/exchange) are handled automatically by proactive SSO
	// and don't need separate browser-based OAuth authentication
	var pendingServers []pkgoauth.ServerAuthStatus
	for _, srv := range authStatus.Servers {
		if srv.Status != pkgoauth.ServerStatusAuthRequired || srv.AuthTool == "" {
			continue
		}
		// Skip SSO-enabled servers unless SSO explicitly failed
		if (srv.TokenForwardingEnabled || srv.TokenExchangeEnabled) && !srv.SSOAttemptFailed {
			continue
		}
		pendingServers = append(pendingServers, srv)
	}

	if len(pendingServers) == 0 {
		authPrintln("\nNo MCP servers require authentication.")
		authPrintln("All authentication complete.")
		return nil
	}

	authPrint("\nFound %d MCP server(s) requiring authentication:\n", len(pendingServers))
	for _, srv := range pendingServers {
		authPrint("  - %s\n", srv.Name)
	}
	authPrintln()

	// Authenticate to each server sequentially, waiting for each to complete
	// This ensures SSO cookies are available for subsequent flows
	waitCfg := DefaultAuthWaitConfig()
	successCount := 0
	for i, srv := range pendingServers {
		authPrint("[%d/%d] Authenticating to %s\n", i+1, len(pendingServers), srv.Name)
		if err := triggerMCPServerAuthWithWait(ctx, handler, aggregatorEndpoint, srv.Name, srv.AuthTool, waitCfg); err != nil {
			authPrint("  Failed: %v\n", err)
		} else {
			successCount++
		}
	}

	authPrint("\nAuthentication complete. %d/%d servers authenticated.\n", successCount, len(pendingServers))
	return nil
}
