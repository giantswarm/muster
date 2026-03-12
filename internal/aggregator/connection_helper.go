package aggregator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	internalmcp "github.com/giantswarm/muster/internal/mcpserver"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/events"
	"github.com/giantswarm/muster/internal/server"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/giantswarm/mcp-oauth/providers/dex"
	"github.com/mark3labs/mcp-go/mcp"
)

// ConnectionResult contains the result of establishing a session connection.
// This is returned by establishSessionConnection and used by callers to format
// their specific result types (api.CallToolResult or mcp.CallToolResult).
type ConnectionResult struct {
	// ServerName is the name of the server that was connected
	ServerName string
	// ToolCount is the number of tools available from the server
	ToolCount int
	// ResourceCount is the number of resources available from the server
	ResourceCount int
	// PromptCount is the number of prompts available from the server
	PromptCount int
}

// establishSessionConnection creates a connection to an MCP server and populates
// the CapabilityCache. This is the shared implementation used by both:
//   - AuthToolProvider.tryConnectWithToken (core_auth_login tool)
//   - AggregatorServer.tryConnectWithToken (OAuth browser callback, manager.go)
//
// This method:
//  1. Creates the appropriate client (DynamicAuthClient or static headers)
//  2. Initializes the connection and fetches capabilities
//  3. Populates the CapabilityCache and registers tools
//  4. Broadcasts tool change notifications
//
// Args:
//   - ctx: Context for the operation
//   - a: The aggregator server instance
//   - sub: The user subject to populate the CapabilityCache for
//   - serverName: Name of the MCP server
//   - serverURL: URL of the MCP server
//   - issuer: OAuth issuer URL (empty for non-OAuth servers)
//   - scope: OAuth scope (empty for non-OAuth servers)
//   - accessToken: The access token to use for authentication
//
// Returns the connection result or an error if connection failed.
func establishSessionConnection(
	ctx context.Context,
	a *AggregatorServer,
	sub, serverName, serverURL, issuer, scope, accessToken string,
) (*ConnectionResult, error) {
	// Get OAuth handler for dynamic token refresh
	oauthHandler := api.GetOAuthHandler()

	// Create the appropriate client based on OAuth availability.
	// If OAuth handler is available, use DynamicAuthClient with mcp-go's built-in
	// OAuth handler for automatic token injection and typed 401 errors.
	// Otherwise, fall back to static headers (backwards compatibility).
	var client internalmcp.MCPClient
	if oauthHandler != nil && oauthHandler.IsEnabled() && issuer != "" {
		tokenStore := internalmcp.NewMusterTokenStore(sub, issuer, oauthHandler)
		client = internalmcp.NewDynamicAuthClient(serverURL, tokenStore, scope)
		logging.Debug("SessionConnection", "Using DynamicAuthClient for user %s, server %s (issuer=%s)",
			logging.TruncateSessionID(sub), serverName, issuer)
	} else {
		// Fallback to static headers
		headers := map[string]string{
			"Authorization": "Bearer " + accessToken,
		}
		client = internalmcp.NewStreamableHTTPClientWithHeaders(serverURL, headers)
		logging.Debug("SessionConnection", "Using static auth headers for user %s, server %s",
			logging.TruncateSessionID(sub), serverName)
	}

	// Try to initialize the client
	if err := client.Initialize(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to initialize connection: %w", err)
	}

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("SessionConnection", "Failed to list resources for user %s, server %s: %v",
			logging.TruncateSessionID(sub), serverName, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("SessionConnection", "Failed to list prompts for user %s, server %s: %v",
			logging.TruncateSessionID(sub), serverName, err)
		prompts = nil
	}

	// Close the initial client now that capabilities have been fetched.
	// Clients are created on demand for tool execution (Phase 2B).
	client.Close()

	// Populate the CapabilityCache for the user-based listing path
	if a.capabilityCache != nil {
		a.capabilityCache.Set(sub, serverName, tools, resources, prompts)
	}

	// Register tools with the mcp-go server so they can be called
	a.registerSessionTools(serverName, tools)

	// Notify the authenticating user's sessions about new tools
	a.NotifyToolsChanged(sub)

	// Sync service state to Connected now that authentication succeeded
	notifyMCPServerConnected(serverName, "authentication")

	logging.Info("SessionConnection", "User %s connected to %s with %d tools, %d resources, %d prompts",
		logging.TruncateSessionID(sub), serverName, len(tools), len(resources), len(prompts))

	return &ConnectionResult{
		ServerName:    serverName,
		ToolCount:     len(tools),
		ResourceCount: len(resources),
		PromptCount:   len(prompts),
	}, nil
}

// FormatAsAPIResult formats the connection result as an api.CallToolResult.
// Used by AuthToolProvider.tryConnectWithToken.
func (r *ConnectionResult) FormatAsAPIResult() *api.CallToolResult {
	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf(
			api.AuthMsgSuccessfullyConnected+" to '%s'!\n\n"+
				"Available capabilities:\n"+
				"- Tools: %d\n"+
				"- Resources: %d\n"+
				"- Prompts: %d\n\n"+
				"You can now use the tools from this server.",
			r.ServerName, r.ToolCount, r.ResourceCount, r.PromptCount,
		)},
		IsError: false,
	}
}

// FormatAsMCPResult formats the connection result as an mcp.CallToolResult.
// Used by AggregatorServer.tryConnectWithToken.
func (r *ConnectionResult) FormatAsMCPResult() *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(fmt.Sprintf(
				api.AuthMsgSuccessfullyConnected+" to %s!\n\n"+
					"Available capabilities:\n"+
					"- Tools: %d\n"+
					"- Resources: %d\n"+
					"- Prompts: %d\n\n"+
					"You can now use the tools from this server.",
				r.ServerName, r.ToolCount, r.ResourceCount, r.PromptCount,
			)),
		},
		IsError: false,
	}
}

// getIDTokenForForwarding retrieves an ID token for SSO token forwarding from available sources.
//
// Token sources are checked in priority order:
//  1. Request context - contains the ID token when user authenticated TO muster via OAuth server
//     protection (Google/Dex). This is injected by createAccessTokenInjectorMiddleware.
//  2. OAuth proxy token store - contains tokens when user authenticated WITH remote servers
//     via core_auth_login. These are keyed by (sub, issuer).
//
// The context token takes priority because it represents the user's current authentication
// to muster, which is what we want to forward for SSO.
//
// Args:
//   - ctx: Request context that may contain an injected ID token
//   - sub: The user subject
//   - musterIssuer: The issuer URL to look up in the OAuth proxy store
//
// Returns the ID token string, or empty string if no token is available.
func getIDTokenForForwarding(ctx context.Context, sub, musterIssuer string) string {
	// First, check the request context for an ID token from muster's OAuth server protection.
	// This is the primary SSO use case: user authenticates TO muster, and we forward that
	// token to downstream servers that trust muster's OAuth client ID.
	if idToken, ok := server.GetIDTokenFromContext(ctx); ok && idToken != "" {
		logging.Debug("SessionConnection", "Found ID token in request context for user %s",
			logging.TruncateSessionID(sub))
		return idToken
	}

	// Fallback: check the OAuth proxy token store.
	// This handles the case where tokens were obtained via a previous core_auth_login call.
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler != nil && oauthHandler.IsEnabled() && musterIssuer != "" {
		fullToken := oauthHandler.GetFullTokenByIssuer(sub, musterIssuer)
		if fullToken != nil && fullToken.IDToken != "" {
			logging.Debug("SessionConnection", "Found ID token in OAuth proxy store for user %s, issuer %s",
				logging.TruncateSessionID(sub), musterIssuer)
			return fullToken.IDToken
		}
	}

	logging.Debug("SessionConnection", "No ID token found for user %s",
		logging.TruncateSessionID(sub))
	return ""
}

// EstablishSessionConnectionWithTokenForwarding attempts to establish a connection
// using ID token forwarding for SSO. This is used when an MCPServer has forwardToken: true.
//
// The function:
//  1. Gets the user's ID token from muster's OAuth session
//  2. Forwards it to the downstream MCP server
//  3. If successful, populates the CapabilityCache and registers tools
//
// Args:
//   - ctx: Context for the operation
//   - a: The aggregator server instance
//   - sub: The user subject
//   - serverInfo: The server info containing URL and auth config
//   - musterIssuer: The issuer URL of muster's OAuth provider (used to get the ID token)
//
// Returns:
//   - *ConnectionResult: The connection result if successful
//   - error: The error if connection failed
func EstablishSessionConnectionWithTokenForwarding(
	ctx context.Context,
	a *AggregatorServer,
	sub string,
	serverInfo *ServerInfo,
	musterIssuer string,
) (*ConnectionResult, error) {
	// Guard against concurrent connection attempts using CapabilityCache.
	// If the cache entry exists and is not expired, skip re-establishment.
	fwdSub := api.GetSubjectFromContext(ctx)
	if fwdSub == "" {
		fwdSub = defaultUser
	}
	if a.capabilityCache != nil {
		if entry, exists := a.capabilityCache.Get(fwdSub, serverInfo.Name); exists && !entry.IsExpired() {
			logging.Debug("SessionConnection", "User %s already connected to %s, skipping token forwarding",
				logging.TruncateSessionID(fwdSub), serverInfo.Name)
			return &ConnectionResult{ServerName: serverInfo.Name}, nil
		}
	}

	// Try to get ID token from multiple sources (in priority order):
	// 1. Request context (for tokens from muster's OAuth server protection)
	// 2. OAuth proxy token store (for tokens obtained via core_auth_login to remote servers)
	//
	// When a user authenticates TO muster (via Google/Dex OAuth), the token is
	// injected into the request context by createAccessTokenInjectorMiddleware.
	// This is the primary SSO use case.
	idToken := getIDTokenForForwarding(ctx, sub, musterIssuer)
	if idToken == "" {
		logging.Debug("SessionConnection", "No ID token available for user %s",
			logging.TruncateSessionID(sub))
		return nil, fmt.Errorf("no ID token available for forwarding")
	}

	// Validate ID token is not expired before forwarding
	// This avoids unnecessary network round-trips with expired tokens
	if isIDTokenExpired(idToken) {
		logging.Warn("SessionConnection", "ID token expired for user %s, cannot forward to %s",
			logging.TruncateSessionID(sub), serverInfo.Name)
		return nil, fmt.Errorf("ID token has expired, needs refresh before forwarding")
	}

	logging.Info("SessionConnection", "Attempting ID token forwarding for user %s to server %s",
		logging.TruncateSessionID(sub), serverInfo.Name)

	// Create a client with a dynamic header function that resolves the latest
	// ID token on each request. This ensures token refresh is picked up
	// automatically without needing to re-establish the connection.
	//
	// IMPORTANT: Use context.Background() instead of the captured request ctx,
	// because the original request context becomes stale/cancelled after the
	// connection-establishing request completes. The OAuth token store (keyed by
	// sub + musterIssuer) is the stable source for refreshed tokens.
	headerFunc := func(_ context.Context) map[string]string {
		latestToken := getIDTokenForForwarding(context.Background(), sub, musterIssuer)
		if latestToken == "" {
			logging.Warn("SessionConnection", "Authentication failed: no ID token in OAuth store for user %s to %s, using original token",
				logging.TruncateSessionID(sub), serverInfo.Name)
			// Fall back to the original token; the server will return 401 if expired
			latestToken = idToken
		} else if latestToken != idToken {
			logging.Info("SessionConnection", "Token expired, refreshing: resolved updated ID token from OAuth store for user %s to %s",
				logging.TruncateSessionID(sub), serverInfo.Name)
		}
		return map[string]string{
			"Authorization": "Bearer " + latestToken,
		}
	}
	client := internalmcp.NewStreamableHTTPClientWithHeaderFunc(serverInfo.URL, headerFunc)

	// Try to initialize the client with the forwarded token
	if err := client.Initialize(ctx); err != nil {
		client.Close()

		// Log the token forwarding failure
		logging.Warn("SessionConnection", "ID token forwarding failed for user %s to server %s: %v",
			logging.TruncateSessionID(sub), serverInfo.Name, err)

		// Emit event for token forwarding failure
		emitTokenForwardingEvent(serverInfo.Name, serverInfo.GetNamespace(), false, err.Error())

		return nil, fmt.Errorf("ID token forwarding failed: %w", err)
	}

	// Token forwarding succeeded - emit success event
	logging.Info("SessionConnection", "ID token forwarding succeeded for user %s to server %s",
		logging.TruncateSessionID(sub), serverInfo.Name)
	emitTokenForwardingEvent(serverInfo.Name, serverInfo.GetNamespace(), true, "")

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to list tools after token forwarding: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("SessionConnection", "Failed to list resources for user %s, server %s: %v",
			logging.TruncateSessionID(sub), serverInfo.Name, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("SessionConnection", "Failed to list prompts for user %s, server %s: %v",
			logging.TruncateSessionID(sub), serverInfo.Name, err)
		prompts = nil
	}

	// Close the initial client now that capabilities have been fetched.
	// Clients are created on demand for tool execution (Phase 2B).
	client.Close()

	// Populate the CapabilityCache
	if a.capabilityCache != nil {
		a.capabilityCache.Set(sub, serverInfo.Name, tools, resources, prompts)
	}

	// Register tools with the mcp-go server
	a.registerSessionTools(serverInfo.Name, tools)

	// Notify the authenticating user's sessions about new tools
	a.NotifyToolsChanged(sub)

	// Sync service state to Connected now that SSO succeeded
	notifyMCPServerConnected(serverInfo.Name, "SSO token forwarding")

	logging.Info("SessionConnection", "User %s connected to %s via SSO token forwarding with %d tools",
		logging.TruncateSessionID(sub), serverInfo.Name, len(tools))

	return &ConnectionResult{
		ServerName:    serverInfo.Name,
		ToolCount:     len(tools),
		ResourceCount: len(resources),
		PromptCount:   len(prompts),
	}, nil
}

// emitTokenForwardingEvent emits an event for token forwarding success or failure.
func emitTokenForwardingEvent(serverName, namespace string, success bool, errorMsg string) {
	eventManager := api.GetEventManager()
	if eventManager == nil {
		return
	}

	// Log when namespace is missing - this indicates a configuration issue
	if namespace == "" {
		logging.Warn("SessionConnection", "No namespace set for server %s event, defaulting to 'default' - check MCPServer configuration", serverName)
		namespace = "default"
	}

	objRef := api.ObjectReference{
		Kind:      "MCPServer",
		Name:      serverName,
		Namespace: namespace,
	}

	var reason events.EventReason
	var eventType, message string

	if success {
		reason = events.ReasonMCPServerTokenForwarded
		eventType = "Normal"
		message = fmt.Sprintf("ID token successfully forwarded for SSO authentication to MCPServer %s", serverName)
	} else {
		reason = events.ReasonMCPServerTokenForwardingFailed
		eventType = "Warning"
		message = fmt.Sprintf("ID token forwarding failed for MCPServer %s: %s", serverName, errorMsg)
	}

	_ = eventManager.CreateEvent(context.Background(), objRef, string(reason), message, eventType)
}

// ShouldUseTokenForwarding checks if token forwarding should be used for a server.
// Token forwarding is enabled when:
//   - AuthConfig.ForwardToken is true (forwardToken implies OAuth-based auth)
//   - OR: AuthConfig.Type is "oauth" and ForwardToken is true
//
// Setting forwardToken: true implicitly enables OAuth authentication since
// token forwarding only makes sense in an OAuth context.
func ShouldUseTokenForwarding(serverInfo *ServerInfo) bool {
	if serverInfo == nil || serverInfo.AuthConfig == nil {
		return false
	}
	// ForwardToken implies OAuth authentication - no need to check Type explicitly
	return serverInfo.AuthConfig.ForwardToken
}

// ShouldUseTokenExchange checks if RFC 8693 token exchange should be used for a server.
// Token exchange is enabled when:
//   - AuthConfig.TokenExchange is not nil
//   - AuthConfig.TokenExchange.Enabled is true
//   - Required fields (DexTokenEndpoint, ConnectorID) are set
//
// Token exchange takes precedence over token forwarding if both are configured.
func ShouldUseTokenExchange(serverInfo *ServerInfo) bool {
	if serverInfo == nil || serverInfo.AuthConfig == nil || serverInfo.AuthConfig.TokenExchange == nil {
		return false
	}
	config := serverInfo.AuthConfig.TokenExchange
	return config.Enabled && config.DexTokenEndpoint != "" && config.ConnectorID != ""
}

// EstablishSessionConnectionWithTokenExchange attempts to establish a connection
// using RFC 8693 Token Exchange for cross-cluster SSO. This is used when an MCPServer has
// tokenExchange configured.
//
// The function:
//  1. Gets the user's ID token from muster's OAuth session
//  2. Extracts the user ID (sub claim) from the token
//  3. Exchanges it for a token valid on the remote cluster's Dex
//  4. If successful, populates the CapabilityCache and registers tools
//
// Args:
//   - ctx: Context for the operation
//   - a: The aggregator server instance
//   - sub: The user subject
//   - serverInfo: The server info containing URL and auth config
//   - musterIssuer: The issuer URL of muster's OAuth provider (used to get the ID token)
//
// Returns:
//   - *ConnectionResult: The connection result if successful
//   - error: The error if connection failed
func EstablishSessionConnectionWithTokenExchange(
	ctx context.Context,
	a *AggregatorServer,
	sub string,
	serverInfo *ServerInfo,
	musterIssuer string,
) (*ConnectionResult, error) {
	// Defensive check: validate preconditions that should be ensured by ShouldUseTokenExchange
	if serverInfo == nil || serverInfo.AuthConfig == nil || serverInfo.AuthConfig.TokenExchange == nil {
		return nil, fmt.Errorf("invalid server configuration for token exchange")
	}

	// Guard against concurrent connection attempts using CapabilityCache.
	exchSub := api.GetSubjectFromContext(ctx)
	if exchSub == "" {
		exchSub = defaultUser
	}
	if a.capabilityCache != nil {
		if entry, exists := a.capabilityCache.Get(exchSub, serverInfo.Name); exists && !entry.IsExpired() {
			logging.Debug("SessionConnection", "User %s already connected to %s, skipping token exchange",
				logging.TruncateSessionID(exchSub), serverInfo.Name)
			return &ConnectionResult{ServerName: serverInfo.Name}, nil
		}
	}

	// Get the OAuth handler for token exchange
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return nil, fmt.Errorf("OAuth handler not available for token exchange")
	}

	// Get ID token from multiple sources (in priority order):
	// 1. Request context (for tokens from muster's OAuth server protection)
	// 2. OAuth proxy token store (for tokens obtained via core_auth_login)
	idToken := getIDTokenForForwarding(ctx, sub, musterIssuer)
	if idToken == "" {
		logging.Debug("SessionConnection", "No ID token available for user %s",
			logging.TruncateSessionID(sub))
		return nil, fmt.Errorf("no ID token available for token exchange")
	}

	// Validate ID token is not expired before exchanging
	if isIDTokenExpired(idToken) {
		logging.Warn("SessionConnection", "ID token expired for user %s, cannot exchange for %s",
			logging.TruncateSessionID(sub), serverInfo.Name)
		return nil, fmt.Errorf("ID token has expired, needs refresh before exchange")
	}

	// Extract user ID from the token for cache key generation
	userID := extractUserIDFromToken(idToken)
	if userID == "" {
		logging.Warn("SessionConnection", "Failed to extract user ID from token for user %s",
			logging.TruncateSessionID(sub))
		return nil, fmt.Errorf("failed to extract user ID from token")
	}

	logging.Info("SessionConnection", "Attempting token exchange for user %s to server %s",
		logging.TruncateSessionID(sub), serverInfo.Name)

	// Load client credentials from secret if configured.
	// Note: This intentionally mutates serverInfo.AuthConfig.TokenExchange to populate
	// the resolved credentials. This is safe because serverInfo is a local copy used
	// only for this connection attempt.
	if serverInfo.AuthConfig.TokenExchange.ClientCredentialsSecretRef != nil {
		credentials, err := loadTokenExchangeCredentials(ctx, serverInfo)
		if err != nil {
			logging.Error("SessionConnection", err, "Failed to load token exchange credentials for %s", serverInfo.Name)
			return nil, fmt.Errorf("failed to load client credentials: %w", err)
		}
		serverInfo.AuthConfig.TokenExchange.ClientID = credentials.ClientID
		serverInfo.AuthConfig.TokenExchange.ClientSecret = credentials.ClientSecret
		logging.Debug("SessionConnection", "Loaded client credentials for token exchange to %s (client_id=%s)",
			serverInfo.Name, credentials.ClientID)
	}

	// Append requiredAudiences as cross-client scopes for the token exchange.
	// This ensures the exchanged token contains the audiences needed by the downstream server
	// (e.g., for Kubernetes OIDC authentication on the remote cluster).
	// Uses dex.AppendAudienceScopes() from mcp-oauth for security-validated formatting.
	//
	// Note: This intentionally mutates serverInfo.AuthConfig.TokenExchange.Scopes to include
	// the audience scopes. This is safe because serverInfo is a local copy used only for
	// this connection attempt.
	if len(serverInfo.AuthConfig.RequiredAudiences) > 0 {
		updatedScopes, err := dex.AppendAudienceScopes(
			serverInfo.AuthConfig.TokenExchange.Scopes,
			serverInfo.AuthConfig.RequiredAudiences,
		)
		if err != nil {
			// Log the error but continue without the audiences - they should already be
			// validated at CRD admission, but handle gracefully if not.
			logging.Warn("SessionConnection", "Failed to format audience scopes for %s: %v (continuing without audiences)",
				serverInfo.Name, err)
		} else {
			serverInfo.AuthConfig.TokenExchange.Scopes = updatedScopes
			logging.Debug("SessionConnection", "Added %d required audiences to token exchange scopes for %s",
				len(serverInfo.AuthConfig.RequiredAudiences), serverInfo.Name)
		}
	}

	// Check if Teleport auth is configured - if so, we need to use Teleport HTTP client
	// for both the token exchange request and the MCP server connection.
	teleportResult := getTeleportHTTPClientIfConfigured(ctx, serverInfo)

	// If Teleport is configured but failed, return an explicit error rather than
	// falling back silently (which would cause confusing connection failures to private endpoints)
	if teleportResult.Configured && teleportResult.Error != nil {
		logging.Error("SessionConnection", teleportResult.Error, "Teleport required for %s but failed",
			serverInfo.Name)
		return nil, fmt.Errorf("teleport configuration failed: %w", teleportResult.Error)
	}

	// Perform the token exchange (using Teleport client if configured)
	var exchangedToken string
	var err error
	if teleportResult.Client != nil {
		logging.Debug("SessionConnection", "Using Teleport HTTP client for token exchange to %s", serverInfo.Name)
		exchangedToken, err = oauthHandler.ExchangeTokenForRemoteClusterWithClient(
			ctx,
			idToken,
			userID,
			serverInfo.AuthConfig.TokenExchange,
			teleportResult.Client,
		)
	} else {
		exchangedToken, err = oauthHandler.ExchangeTokenForRemoteCluster(
			ctx,
			idToken,
			userID,
			serverInfo.AuthConfig.TokenExchange,
		)
	}
	if err != nil {
		logging.Warn("SessionConnection", "Token exchange failed for user %s to server %s: %v",
			logging.TruncateSessionID(sub), serverInfo.Name, err)

		// Emit event for token exchange failure
		emitTokenExchangeEvent(serverInfo.Name, serverInfo.GetNamespace(), false, err.Error())

		// Audit log for failed token exchange (compliance/security monitoring)
		logging.Audit(logging.AuditEvent{
			Action:    "token_exchange",
			Outcome:   "failure",
			SessionID: logging.TruncateSessionID(sub),
			UserID:    logging.TruncateSessionID(userID),
			Target:    serverInfo.Name,
			Details:   fmt.Sprintf("endpoint=%s connector=%s", serverInfo.AuthConfig.TokenExchange.DexTokenEndpoint, serverInfo.AuthConfig.TokenExchange.ConnectorID),
			Error:     err.Error(),
		})

		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	// Token exchange succeeded - emit success event and audit log
	logging.Info("SessionConnection", "Token exchange succeeded for user %s to server %s",
		logging.TruncateSessionID(sub), serverInfo.Name)
	emitTokenExchangeEvent(serverInfo.Name, serverInfo.GetNamespace(), true, "")

	// Audit log for successful token exchange (compliance/security monitoring)
	logging.Audit(logging.AuditEvent{
		Action:    "token_exchange",
		Outcome:   "success",
		SessionID: logging.TruncateSessionID(sub),
		UserID:    logging.TruncateSessionID(userID),
		Target:    serverInfo.Name,
		Details:   fmt.Sprintf("endpoint=%s connector=%s", serverInfo.AuthConfig.TokenExchange.DexTokenEndpoint, serverInfo.AuthConfig.TokenExchange.ConnectorID),
	})

	// Create a simple header function using the exchanged token.
	// No token refresh logic needed since the client is short-lived (one capability fetch).
	// If the token expires during fetch, the server returns 401.
	headerFunc := func(_ context.Context) map[string]string {
		return map[string]string{"Authorization": "Bearer " + exchangedToken}
	}

	// Create a client with the dynamic header function.
	// If Teleport is configured, use the Teleport HTTP client for the MCP connection as well.
	var client *internalmcp.StreamableHTTPClient
	if teleportResult.Client != nil {
		logging.Debug("SessionConnection", "Using Teleport HTTP client for MCP connection to %s", serverInfo.Name)
		client = internalmcp.NewStreamableHTTPClientWithHeaderFuncAndHTTPClient(serverInfo.URL, headerFunc, teleportResult.Client)
	} else {
		client = internalmcp.NewStreamableHTTPClientWithHeaderFunc(serverInfo.URL, headerFunc)
	}

	// Try to initialize the client with the exchanged token
	if err := client.Initialize(ctx); err != nil {
		client.Close()

		logging.Warn("SessionConnection", "Connection with exchanged token failed for user %s to server %s: %v",
			logging.TruncateSessionID(sub), serverInfo.Name, err)

		return nil, fmt.Errorf("connection with exchanged token failed: %w", err)
	}

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to list tools after token exchange: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("SessionConnection", "Failed to list resources for user %s, server %s: %v",
			logging.TruncateSessionID(sub), serverInfo.Name, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("SessionConnection", "Failed to list prompts for user %s, server %s: %v",
			logging.TruncateSessionID(sub), serverInfo.Name, err)
		prompts = nil
	}

	// Close the initial client now that capabilities have been fetched.
	// Clients are created on demand for tool execution (Phase 2B).
	client.Close()

	// Populate the CapabilityCache
	if a.capabilityCache != nil {
		a.capabilityCache.Set(sub, serverInfo.Name, tools, resources, prompts)
	}

	// Register tools with the mcp-go server
	a.registerSessionTools(serverInfo.Name, tools)

	// Notify the authenticating user's sessions about new tools
	a.NotifyToolsChanged(sub)

	// Sync service state to Connected now that token exchange succeeded
	notifyMCPServerConnected(serverInfo.Name, "RFC 8693 token exchange")

	logging.Info("SessionConnection", "User %s connected to %s via RFC 8693 token exchange with %d tools",
		logging.TruncateSessionID(sub), serverInfo.Name, len(tools))

	return &ConnectionResult{
		ServerName:    serverInfo.Name,
		ToolCount:     len(tools),
		ResourceCount: len(resources),
		PromptCount:   len(prompts),
	}, nil
}

// emitTokenExchangeEvent emits an event for token exchange success or failure.
func emitTokenExchangeEvent(serverName, namespace string, success bool, errorMsg string) {
	eventManager := api.GetEventManager()
	if eventManager == nil {
		return
	}

	// Log when namespace is missing - this indicates a configuration issue
	if namespace == "" {
		logging.Warn("SessionConnection", "No namespace set for server %s event, defaulting to 'default' - check MCPServer configuration", serverName)
		namespace = "default"
	}

	objRef := api.ObjectReference{
		Kind:      "MCPServer",
		Name:      serverName,
		Namespace: namespace,
	}

	var reason events.EventReason
	var eventType, message string

	if success {
		reason = events.ReasonMCPServerTokenExchanged
		eventType = "Normal"
		message = fmt.Sprintf("Token successfully exchanged for cross-cluster SSO to MCPServer %s", serverName)
	} else {
		reason = events.ReasonMCPServerTokenExchangeFailed
		eventType = "Warning"
		message = fmt.Sprintf("Token exchange failed for MCPServer %s: %s", serverName, errorMsg)
	}

	_ = eventManager.CreateEvent(context.Background(), objRef, string(reason), message, eventType)
}

// decodeJWTPayload decodes the payload (second part) of a JWT token without
// cryptographic verification. This is safe for extracting claims for non-security
// purposes (e.g., cache keys, expiry checks) when the token comes from a trusted source.
//
// Returns the decoded payload bytes or an error if decoding fails.
func decodeJWTPayload(token string) ([]byte, error) {
	if token == "" {
		return nil, fmt.Errorf("token is empty")
	}

	// JWT format: header.payload.signature
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid JWT format: expected at least 2 parts")
	}

	// Decode the payload using RawURLEncoding (handles missing padding automatically)
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try standard base64 as fallback for non-standard implementations
		decoded, err = base64.RawStdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("failed to decode payload: %w", err)
		}
	}

	return decoded, nil
}

// extractUserIDFromToken extracts the user ID (sub claim) from a JWT ID token.
// This is used to generate cache keys for token exchange.
//
// SECURITY NOTE:
//   - This extracts from the token payload WITHOUT cryptographic verification.
//   - This is safe because the caller MUST ensure the token comes from a trusted source
//     (e.g., muster's OAuth session or request context, not user input).
//   - The actual token validation happens on the remote Dex server during exchange.
//   - The extracted user ID is only used for cache key generation, not authorization.
func extractUserIDFromToken(idToken string) string {
	decoded, err := decodeJWTPayload(idToken)
	if err != nil {
		return ""
	}

	// Parse the claims
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}

	return claims.Sub
}

// idTokenExpiryMargin is the minimum time before expiry that we consider a token valid.
// This accounts for clock skew and network latency during forwarding.
const idTokenExpiryMargin = 30 * time.Second

// notifyMCPServerConnected updates the MCPServer service state to Connected after
// successful authentication. This syncs the session-level connection success to
// the service-level state, ensuring that `muster list mcpserver` shows the correct
// connected state.
//
// This is a best-effort operation - failures are logged at warn level but don't
// fail the connection flow.
func notifyMCPServerConnected(serverName, authMethod string) {
	if err := api.UpdateMCPServerState(serverName, api.StateConnected, api.HealthHealthy, nil); err != nil {
		logging.Warn("SessionConnection", "Failed to update MCPServer %s state after %s: %v",
			serverName, authMethod, err)
	}
}

// isIDTokenExpired checks if a JWT ID token is expired or about to expire.
// This provides basic validation before forwarding tokens to downstream servers,
// avoiding unnecessary network round-trips with expired tokens.
//
// The function parses the JWT payload (without verifying the signature) to extract
// the 'exp' claim. Signature verification is the responsibility of the downstream server.
//
// Returns true if:
//   - The token is malformed and cannot be parsed
//   - The 'exp' claim is missing
//   - The token has expired or will expire within the margin
func isIDTokenExpired(idToken string) bool {
	decoded, err := decodeJWTPayload(idToken)
	if err != nil {
		logging.Debug("TokenValidation", "Failed to decode ID token: %v", err)
		return true
	}

	// Parse the claims
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		logging.Debug("TokenValidation", "Failed to parse ID token claims: %v", err)
		return true
	}

	// Check if exp claim exists
	if claims.Exp == 0 {
		logging.Debug("TokenValidation", "ID token missing 'exp' claim")
		return true
	}

	// Check if token is expired or about to expire
	expiresAt := time.Unix(claims.Exp, 0)
	now := time.Now()
	if now.Add(idTokenExpiryMargin).After(expiresAt) {
		logging.Debug("TokenValidation", "ID token expired or expiring soon (expires at %v, now %v)",
			expiresAt, now)
		return true
	}

	return false
}

// getTokenExpiryTime extracts the expiry time from a JWT token.
// Returns zero time if the token is malformed or missing the exp claim.
func getTokenExpiryTime(token string) time.Time {
	decoded, err := decodeJWTPayload(token)
	if err != nil {
		return time.Time{}
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}
	}

	return time.Unix(claims.Exp, 0)
}

// TeleportClientResult contains the result of getting a Teleport HTTP client.
// This provides explicit error handling for Teleport configuration issues.
type TeleportClientResult struct {
	// Client is the HTTP client configured with Teleport mTLS certificates.
	// Nil if Teleport is not configured or if there was an error.
	Client *http.Client

	// Configured indicates whether Teleport authentication was configured
	// for this server. When true but Client is nil, Error will contain the reason.
	Configured bool

	// Error contains the error if Teleport was configured but client creation failed.
	// This allows callers to distinguish between "not configured" and "configured but failed".
	Error error
}

// getTeleportHTTPClientIfConfigured returns a Teleport HTTP client if the server
// is configured to use Teleport authentication.
//
// This is used for both token exchange and MCP server connections when accessing
// private installations via Teleport Application Access.
//
// The function returns a TeleportClientResult that distinguishes between:
//   - Not configured: Configured=false, Client=nil, Error=nil (use default HTTP client)
//   - Configured and successful: Configured=true, Client!=nil, Error=nil
//   - Configured but failed: Configured=true, Client=nil, Error!=nil (caller should fail, not fallback)
//
// This explicit error handling prevents silent fallback when Teleport is required
// but misconfigured, which would lead to confusing connection errors.
func getTeleportHTTPClientIfConfigured(ctx context.Context, serverInfo *ServerInfo) TeleportClientResult {
	// Check if server has Teleport auth configured
	if serverInfo == nil || serverInfo.AuthConfig == nil {
		return TeleportClientResult{Configured: false}
	}
	if serverInfo.AuthConfig.Type != api.AuthTypeTeleport {
		return TeleportClientResult{Configured: false}
	}

	// From this point on, Teleport IS configured - errors should be explicit
	if serverInfo.AuthConfig.Teleport == nil {
		err := fmt.Errorf("teleport auth type configured but teleport settings missing")
		logging.Error("SessionConnection", err, "Teleport configuration error for %s", serverInfo.Name)
		return TeleportClientResult{Configured: true, Error: err}
	}

	// Get the Teleport handler from the API service locator
	teleportHandler := api.GetTeleportClient()
	if teleportHandler == nil {
		err := fmt.Errorf("teleport client handler not registered - ensure teleport package is initialized")
		logging.Error("SessionConnection", err, "Teleport initialization error for %s", serverInfo.Name)
		return TeleportClientResult{Configured: true, Error: err}
	}

	// Build the client configuration from the server auth settings
	teleportAuth := serverInfo.AuthConfig.Teleport
	clientConfig := api.TeleportClientConfig{
		IdentityDir:             teleportAuth.IdentityDir,
		IdentitySecretName:      teleportAuth.IdentitySecretName,
		IdentitySecretNamespace: teleportAuth.IdentitySecretNamespace,
		AppName:                 teleportAuth.AppName,
	}

	// Validate that exactly one identity source is specified
	if clientConfig.IdentityDir == "" && clientConfig.IdentitySecretName == "" {
		err := fmt.Errorf("teleport auth requires either identityDir or identitySecretName")
		logging.Error("SessionConnection", err, "Teleport configuration error for %s", serverInfo.Name)
		return TeleportClientResult{Configured: true, Error: err}
	}
	if clientConfig.IdentityDir != "" && clientConfig.IdentitySecretName != "" {
		err := fmt.Errorf("teleport auth: identityDir and identitySecretName are mutually exclusive")
		logging.Error("SessionConnection", err, "Teleport configuration error for %s", serverInfo.Name)
		return TeleportClientResult{Configured: true, Error: err}
	}

	// Get the HTTP client from the Teleport handler
	httpClient, err := teleportHandler.GetHTTPClientForConfig(ctx, clientConfig)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to get Teleport HTTP client: %w", err)
		logging.Error("SessionConnection", wrappedErr, "Teleport client error for %s", serverInfo.Name)
		return TeleportClientResult{Configured: true, Error: wrappedErr}
	}

	logging.Debug("SessionConnection", "Got Teleport HTTP client for %s", serverInfo.Name)
	return TeleportClientResult{Configured: true, Client: httpClient}
}

// loadTokenExchangeCredentials loads OAuth client credentials from a Kubernetes secret
// for token exchange authentication with remote Dex instances.
//
// Args:
//   - ctx: Context for Kubernetes API calls
//   - serverInfo: The MCP server info containing the token exchange configuration
//
// Returns:
//   - *api.ClientCredentials: The loaded credentials
//   - error: Error if credentials could not be loaded
func loadTokenExchangeCredentials(ctx context.Context, serverInfo *ServerInfo) (*api.ClientCredentials, error) {
	if serverInfo.AuthConfig == nil ||
		serverInfo.AuthConfig.TokenExchange == nil ||
		serverInfo.AuthConfig.TokenExchange.ClientCredentialsSecretRef == nil {
		return nil, fmt.Errorf("no client credentials secret reference configured")
	}

	handler := api.GetSecretCredentialsHandler()
	if handler == nil {
		return nil, fmt.Errorf("secret credentials handler not registered")
	}

	// Use the server's namespace as the default for the secret
	defaultNamespace := serverInfo.GetNamespace()
	if defaultNamespace == "" {
		defaultNamespace = "default"
	}

	return handler.LoadClientCredentials(ctx, serverInfo.AuthConfig.TokenExchange.ClientCredentialsSecretRef, defaultNamespace)
}
