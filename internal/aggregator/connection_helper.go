package aggregator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

// errMissingSession is the user-facing message returned when a tool call
// lacks session/subject context. Shared by requireSessionContext and
// requireSessionContextResult so the wording stays in sync.
const errMissingSession = "Error: authentication context missing — no active session"

// requireSessionContext extracts sessionID and subject from ctx,
// returning an error that names serverName when either is missing.
func requireSessionContext(ctx context.Context, serverName string) (sessionID, sub string, err error) {
	sessionID = getSessionIDFromContext(ctx)
	sub = getUserSubjectFromContext(ctx)
	if sessionID == "" || sub == "" {
		return "", "", fmt.Errorf("no session context available for server %s", serverName)
	}
	return sessionID, sub, nil
}

// requireSessionContextResult is like requireSessionContext but returns an
// api.CallToolResult error suitable for direct return from tool handlers.
func requireSessionContextResult(ctx context.Context) (sessionID, sub string, errResult *api.CallToolResult) {
	sessionID = getSessionIDFromContext(ctx)
	sub = getUserSubjectFromContext(ctx)
	if sessionID == "" || sub == "" {
		return "", "", &api.CallToolResult{
			Content: []interface{}{errMissingSession},
			IsError: true,
		}
	}
	return sessionID, sub, nil
}

// ConnectionResult contains the result of establishing a session connection.
// This is returned by establishConnection and used by callers to format
// their specific result types (api.CallToolResult or mcp.CallToolResult).
//
// The Client field holds the live, initialized MCP client. Ownership is
// transferred to the caller, who must either pool or close it.
type ConnectionResult struct {
	// ServerName is the name of the server that was connected
	ServerName string
	// ToolCount is the number of tools available from the server
	ToolCount int
	// ResourceCount is the number of resources available from the server
	ResourceCount int
	// PromptCount is the number of prompts available from the server
	PromptCount int
	// Client is the live MCP client. The caller owns its lifecycle and must
	// either pool it for reuse or close it when done.
	Client MCPClient
	// TokenExpiry records when the client's bearer token expires. Zero means
	// no expiry tracking (e.g., token forwarding clients). Callers should pass
	// this to SessionConnectionPool.PutWithExpiry for proactive refresh.
	TokenExpiry time.Time
	// ExchangedToken is the RFC 8693 exchanged bearer this client sends
	// downstream. Populated only by the token-exchange path — forward-token
	// and DynamicAuth connections leave it empty. Callers should pass it to
	// SessionConnectionPool.SetExchangedToken so the admin UI can surface it.
	ExchangedToken string
}

// establishConnection creates a connection to an MCP server and populates
// the CapabilityStore. This is the shared implementation used by both:
//   - AuthToolProvider.tryConnectWithToken (core_auth_login tool)
//   - AggregatorServer.tryConnectWithToken (OAuth browser callback, manager.go)
//
// This method:
//  1. Creates the appropriate client (DynamicAuthClient or static headers)
//  2. Initializes the connection and fetches capabilities
//  3. Populates the CapabilityStore and registers tools
//  4. Broadcasts tool change notifications
//
// Both sessionID and sub are extracted from the context. The sessionID is used
// as the cache key for per-login-session isolation, while sub is used for user
// identity operations (notifications).
//
// Args:
//   - ctx: Context for the operation (must contain sessionID and sub)
//   - a: The aggregator server instance
//   - serverName: Name of the MCP server
//   - serverURL: URL of the MCP server
//   - issuer: OAuth issuer URL (empty for non-OAuth servers)
//   - scope: OAuth scope (empty for non-OAuth servers)
//   - accessToken: The access token to use for authentication
//
// Returns the connection result or an error if connection failed.
func establishConnection(
	ctx context.Context,
	a *AggregatorServer,
	serverName, serverURL, issuer, scope, accessToken string,
) (*ConnectionResult, error) {
	sessionID, sub, err := requireSessionContext(ctx, serverName)
	if err != nil {
		return nil, err
	}

	oauthHandler := api.GetOAuthHandler()

	var client internalmcp.MCPClient
	if oauthHandler != nil && oauthHandler.IsEnabled() && issuer != "" {
		tokenStore := internalmcp.NewMusterTokenStore(sessionID, sub, issuer, oauthHandler)
		client = internalmcp.NewDynamicAuthClient(serverURL, tokenStore, scope)
		logging.Debug("Connection", "Using DynamicAuthClient for session %s, server %s (issuer=%s)",
			logging.TruncateIdentifier(sessionID), serverName, issuer)
	} else {
		headers := map[string]string{
			"Authorization": "Bearer " + accessToken,
		}
		client = internalmcp.NewStreamableHTTPClientWithHeaders(serverURL, headers)
		logging.Debug("Connection", "Using static auth headers for session %s, server %s",
			logging.TruncateIdentifier(sessionID), serverName)
	}

	// Try to initialize the client
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to initialize connection: %w", err)
	}

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list resources for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverName, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list prompts for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverName, err)
		prompts = nil
	}

	// Populate the CapabilityStore keyed by session ID for per-login isolation
	if a.capabilityStore != nil {
		if err := a.capabilityStore.Set(ctx, sessionID, serverName, &Capabilities{
			Tools: tools, Resources: resources, Prompts: prompts,
		}); err != nil {
			logging.Warn("Connection", "Failed to store capabilities for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverName, err)
		}
	}

	if a.authStore != nil {
		if err := a.authStore.MarkAuthenticated(ctx, sessionID, serverName); err != nil {
			logging.Warn("Connection", "Failed to mark auth for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverName, err)
		}
	}

	// Sync service state to Connected now that authentication succeeded
	notifyMCPServerConnected(serverName, "authentication")

	logging.Info("Connection", "User %s connected to %s with %d tools, %d resources, %d prompts",
		logging.TruncateIdentifier(sub), serverName, len(tools), len(resources), len(prompts))

	return &ConnectionResult{
		ServerName:    serverName,
		ToolCount:     len(tools),
		ResourceCount: len(resources),
		PromptCount:   len(prompts),
		Client:        client,
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
//  1. Request context - contains the ID token when called from within an HTTP request handler.
//     Injected by createAccessTokenInjectorMiddleware from the Valkey token store.
//  2. OAuth manager's token store - contains the token populated by SetSessionCreationHandler
//     and SetTokenRefreshHandler, looked up by (sessionID, musterIssuer). This is the primary
//     source for background closures (headerFunc) that run outside the HTTP request lifecycle
//     with context.Background().
//
// The context token takes priority because it's the freshest, directly from the current request.
//
// Args:
//   - ctx: Request context that may contain an injected ID token
//   - sessionID: The session ID (token family ID) for token store lookups
//   - musterIssuer: The issuer URL to look up in the OAuth proxy store
//
// Returns the ID token string, or empty string if no token is available.
func getIDTokenForForwarding(ctx context.Context, sessionID, musterIssuer string) string {
	if idToken, ok := server.GetIDTokenFromContext(ctx); ok && idToken != "" {
		logging.Debug("Connection", "Found ID token in request context for session %s",
			logging.TruncateIdentifier(sessionID))
		return idToken
	}

	oauthHandler := api.GetOAuthHandler()
	if oauthHandler != nil && oauthHandler.IsEnabled() && musterIssuer != "" {
		fullToken := oauthHandler.GetFullTokenByIssuer(sessionID, musterIssuer)
		if fullToken != nil && fullToken.IDToken != "" {
			logging.Debug("Connection", "Found ID token in OAuth proxy store for session %s, issuer %s",
				logging.TruncateIdentifier(sessionID), musterIssuer)
			return fullToken.IDToken
		}
	}

	logging.Debug("Connection", "No ID token found for session %s",
		logging.TruncateIdentifier(sessionID))
	return ""
}

// EstablishConnectionWithTokenForwarding attempts to establish a connection
// using ID token forwarding for SSO. This is used when an MCPServer has forwardToken: true.
//
// The function:
//  1. Gets the user's ID token from muster's OAuth session
//  2. Forwards it to the downstream MCP server
//  3. If successful, populates the CapabilityStore and registers tools
//
// Both sessionID and sub are extracted from ctx (set by OAuth middleware).
//
// Args:
//   - ctx: Context for the operation (must contain sessionID and sub)
//   - a: The aggregator server instance
//   - serverInfo: The server info containing URL and auth config
//   - musterIssuer: The issuer URL of muster's OAuth provider (used to get the ID token)
//
// Returns:
//   - *ConnectionResult: The connection result if successful
//   - error: The error if connection failed
func EstablishConnectionWithTokenForwarding(
	ctx context.Context,
	a *AggregatorServer,
	serverInfo *ServerInfo,
	musterIssuer string,
) (*ConnectionResult, error) {
	sessionID, sub, err := requireSessionContext(ctx, serverInfo.Name)
	if err != nil {
		return nil, err
	}

	if a.authStore != nil {
		authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, serverInfo.Name)
		if authenticated {
			logging.Debug("Connection", "Session %s already authenticated to %s, skipping token forwarding",
				logging.TruncateIdentifier(sessionID), serverInfo.Name)
			return &ConnectionResult{ServerName: serverInfo.Name}, nil
		}
	}

	idToken := getIDTokenForForwarding(ctx, sessionID, musterIssuer)
	if idToken == "" {
		logging.Debug("Connection", "No ID token available for user %s",
			logging.TruncateIdentifier(sub))
		return nil, fmt.Errorf("no ID token available for forwarding")
	}

	// Validate ID token is not expired before forwarding
	// This avoids unnecessary network round-trips with expired tokens
	if isIDTokenExpired(idToken) {
		logging.Warn("Connection", "ID token expired for user %s, cannot forward to %s",
			logging.TruncateIdentifier(sub), serverInfo.Name)
		return nil, fmt.Errorf("ID token has expired, needs refresh before forwarding")
	}

	logging.Info("Connection", "Attempting ID token forwarding for user %s to server %s",
		logging.TruncateIdentifier(sub), serverInfo.Name)

	// Create a client with a dynamic header function that resolves the latest
	// ID token on each request. This ensures token refresh is picked up
	// automatically without needing to re-establish the connection.
	//
	// IMPORTANT: Use context.Background() instead of the captured request ctx,
	// because the original request context becomes stale/cancelled after the
	// connection-establishing request completes. The OAuth token store (keyed by
	// sub + musterIssuer) is the stable source for refreshed tokens.
	//
	// The onStaleToken callback evicts the pooled connection and revokes the
	// auth entry when the token cannot be resolved after repeated attempts.
	// This stops mcp-go's infinite retry loop with expired tokens.
	onStaleToken := func() {
		if a.connPool != nil {
			a.connPool.Evict(sessionID, serverInfo.Name)
			logging.Info("Connection", "Evicted stale SSO connection for session %s to %s",
				logging.TruncateIdentifier(sessionID), serverInfo.Name)
		}
		if a.authStore != nil {
			if err := a.authStore.Revoke(context.Background(), sessionID, serverInfo.Name); err != nil {
				logging.Warn("Connection", "Failed to revoke auth for session %s server %s after stale token eviction: %v",
					logging.TruncateIdentifier(sessionID), serverInfo.Name, err)
			}
		}
	}
	headerFunc := makeTokenForwardingHeaderFunc(sessionID, sub, musterIssuer, serverInfo.Name, idToken, onStaleToken)
	client := internalmcp.NewStreamableHTTPClientWithHeaderFunc(serverInfo.URL, headerFunc)

	// Try to initialize the client with the forwarded token
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()

		// Log the token forwarding failure
		logging.Warn("Connection", "ID token forwarding failed for user %s to server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)

		// Emit event for token forwarding failure
		emitTokenForwardingEvent(serverInfo.Name, serverInfo.GetNamespace(), false, err.Error())

		return nil, fmt.Errorf("ID token forwarding failed: %w", err)
	}

	// Token forwarding succeeded - emit success event
	logging.Info("Connection", "ID token forwarding succeeded for user %s to server %s",
		logging.TruncateIdentifier(sub), serverInfo.Name)
	emitTokenForwardingEvent(serverInfo.Name, serverInfo.GetNamespace(), true, "")

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to list tools after token forwarding: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list resources for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list prompts for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)
		prompts = nil
	}

	// Populate the CapabilityStore keyed by session ID for per-login isolation
	if a.capabilityStore != nil {
		if err := a.capabilityStore.Set(ctx, sessionID, serverInfo.Name, &Capabilities{
			Tools: tools, Resources: resources, Prompts: prompts,
		}); err != nil {
			logging.Warn("Connection", "Failed to store capabilities for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverInfo.Name, err)
		}
	}

	if a.authStore != nil {
		if err := a.authStore.MarkAuthenticated(ctx, sessionID, serverInfo.Name); err != nil {
			logging.Warn("Connection", "Failed to mark auth for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverInfo.Name, err)
		}
	}

	// Sync service state to Connected now that SSO succeeded
	notifyMCPServerConnected(serverInfo.Name, "SSO token forwarding")

	logging.Info("Connection", "User %s connected to %s via SSO token forwarding with %d tools",
		logging.TruncateIdentifier(sub), serverInfo.Name, len(tools))

	return &ConnectionResult{
		ServerName:    serverInfo.Name,
		ToolCount:     len(tools),
		ResourceCount: len(resources),
		PromptCount:   len(prompts),
		Client:        client,
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
		logging.Warn("Connection", "No namespace set for server %s event, defaulting to 'default' - check MCPServer configuration", serverName)
		namespace = "default" //nolint:goconst
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

// EstablishConnectionWithTokenExchange attempts to establish a connection
// using RFC 8693 Token Exchange for cross-cluster SSO. This is used when an MCPServer has
// tokenExchange configured.
//
// The function:
//  1. Gets the user's ID token from muster's OAuth session
//  2. Extracts the user ID (sub claim) from the token
//  3. Exchanges it for a token valid on the remote cluster's Dex
//  4. If successful, populates the CapabilityStore and registers tools
//
// Both sessionID and sub are extracted from ctx (set by OAuth middleware).
//
// Args:
//   - ctx: Context for the operation (must contain sessionID and sub)
//   - a: The aggregator server instance
//   - serverInfo: The server info containing URL and auth config
//   - musterIssuer: The issuer URL of muster's OAuth provider (used to get the ID token)
//
// Returns:
//   - *ConnectionResult: The connection result if successful
//   - error: The error if connection failed
func EstablishConnectionWithTokenExchange(
	ctx context.Context,
	a *AggregatorServer,
	serverInfo *ServerInfo,
	musterIssuer string,
) (*ConnectionResult, error) {
	if serverInfo == nil || serverInfo.AuthConfig == nil || serverInfo.AuthConfig.TokenExchange == nil {
		return nil, fmt.Errorf("invalid server configuration for token exchange")
	}

	sessionID, sub, err := requireSessionContext(ctx, serverInfo.Name)
	if err != nil {
		return nil, err
	}
	if a.authStore != nil {
		if authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, serverInfo.Name); authenticated {
			logging.Debug("Connection", "Session %s already authenticated to %s, skipping token exchange",
				logging.TruncateIdentifier(sessionID), serverInfo.Name)
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
	// 2. OAuth proxy token store (for tokens from muster's own OAuth session)
	idToken := getIDTokenForForwarding(ctx, sessionID, musterIssuer)
	if idToken == "" {
		logging.Debug("Connection", "No ID token available for user %s",
			logging.TruncateIdentifier(sub))
		return nil, fmt.Errorf("no ID token available for token exchange")
	}

	// Validate ID token is not expired before exchanging
	if isIDTokenExpired(idToken) {
		logging.Warn("Connection", "ID token expired for user %s, cannot exchange for %s",
			logging.TruncateIdentifier(sub), serverInfo.Name)
		return nil, fmt.Errorf("ID token has expired, needs refresh before exchange")
	}

	// Extract user ID from the token for cache key generation
	userID := extractUserIDFromToken(idToken)
	if userID == "" {
		logging.Warn("Connection", "Failed to extract user ID from token for user %s",
			logging.TruncateIdentifier(sub))
		return nil, fmt.Errorf("failed to extract user ID from token")
	}

	logging.Info("Connection", "Attempting token exchange for user %s to server %s",
		logging.TruncateIdentifier(sub), serverInfo.Name)

	// Load client credentials from secret if configured.
	// Note: This intentionally mutates serverInfo.AuthConfig.TokenExchange to populate
	// the resolved credentials. This is safe because serverInfo is a local copy used
	// only for this connection attempt.
	if serverInfo.AuthConfig.TokenExchange.ClientCredentialsSecretRef != nil {
		credentials, err := loadTokenExchangeCredentials(ctx, serverInfo)
		if err != nil {
			logging.Error("Connection", err, "Failed to load token exchange credentials for %s", serverInfo.Name)
			return nil, fmt.Errorf("failed to load client credentials: %w", err)
		}
		serverInfo.AuthConfig.TokenExchange.ClientID = credentials.ClientID
		serverInfo.AuthConfig.TokenExchange.ClientSecret = credentials.ClientSecret
		logging.Debug("Connection", "Loaded client credentials for token exchange to %s (client_id=%s)",
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
			logging.Warn("Connection", "Failed to format audience scopes for %s: %v (continuing without audiences)",
				serverInfo.Name, err)
		} else {
			serverInfo.AuthConfig.TokenExchange.Scopes = updatedScopes
			logging.Debug("Connection", "Added %d required audiences to token exchange scopes for %s",
				len(serverInfo.AuthConfig.RequiredAudiences), serverInfo.Name)
		}
	}

	// Perform the token exchange against the remote Dex.
	//
	// Transport-level routing (e.g. mTLS via Teleport for private MCs) is
	// configured per-CR via spec.transport (TB-0). Wiring it into this code
	// path is the responsibility of TB-7's CR-driven transport dispatcher.
	// Until that lands, this path always uses the default HTTP client —
	// equivalent to direct HTTPS to spec.auth.tokenExchange.dexTokenEndpoint.
	var exchangedToken string
	exchangedToken, err = oauthHandler.ExchangeTokenForRemoteCluster(
		ctx,
		idToken,
		userID,
		serverInfo.AuthConfig.TokenExchange,
	)
	if err != nil {
		logging.Warn("Connection", "Token exchange failed for user %s to server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)

		// Emit event for token exchange failure
		emitTokenExchangeEvent(serverInfo.Name, serverInfo.GetNamespace(), false, err.Error())

		// Audit log for failed token exchange (compliance/security monitoring)
		logging.Audit(logging.AuditEvent{
			Action:  "token_exchange",
			Outcome: "failure",
			Subject: logging.TruncateIdentifier(sub),
			UserID:  logging.TruncateIdentifier(userID),
			Target:  serverInfo.Name,
			Details: fmt.Sprintf("endpoint=%s connector=%s", serverInfo.AuthConfig.TokenExchange.DexTokenEndpoint, serverInfo.AuthConfig.TokenExchange.ConnectorID),
			Error:   err.Error(),
		})

		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	// Token exchange succeeded - emit success event and audit log
	logging.Info("Connection", "Token exchange succeeded for user %s to server %s",
		logging.TruncateIdentifier(sub), serverInfo.Name)
	emitTokenExchangeEvent(serverInfo.Name, serverInfo.GetNamespace(), true, "")

	// Audit log for successful token exchange (compliance/security monitoring)
	logging.Audit(logging.AuditEvent{
		Action:  "token_exchange",
		Outcome: "success",
		Subject: logging.TruncateIdentifier(sub),
		UserID:  logging.TruncateIdentifier(userID),
		Target:  serverInfo.Name,
		Details: fmt.Sprintf("endpoint=%s connector=%s", serverInfo.AuthConfig.TokenExchange.DexTokenEndpoint, serverInfo.AuthConfig.TokenExchange.ConnectorID),
	})

	// Extract the exchanged token's expiry for proactive pool refresh.
	// If the token is near expiry, getOrCreateClientForToolCall will
	// proactively evict the pooled client and re-exchange before the
	// downstream server returns 401.
	tokenExpiry := getTokenExpiryTime(exchangedToken)

	// Create a header function using the exchanged token. The token has a fixed
	// lifetime; if it expires while the client is pooled, the downstream server
	// returns 401. In that case, callToolWithTokenExchangeRetry evicts the stale
	// pool entry, re-exchanges a fresh token, and retries the tool call.
	headerFunc := func(_ context.Context) map[string]string {
		return map[string]string{"Authorization": "Bearer " + exchangedToken}
	}

	// Create a client with the dynamic header function.
	//
	// Transport-level routing (e.g. mTLS via Teleport for private MCs) is
	// configured per-CR via spec.transport (TB-0). Wiring it into this code
	// path is the responsibility of TB-7's CR-driven dispatcher.
	client := internalmcp.NewStreamableHTTPClientWithHeaderFunc(serverInfo.URL, headerFunc)

	// Try to initialize the client with the exchanged token
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()

		logging.Warn("Connection", "Connection with exchanged token failed for user %s to server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)

		return nil, fmt.Errorf("connection with exchanged token failed: %w", err)
	}

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to list tools after token exchange: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list resources for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list prompts for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)
		prompts = nil
	}

	// Populate the CapabilityStore keyed by session ID for per-login isolation
	if a.capabilityStore != nil {
		if storeErr := a.capabilityStore.Set(ctx, sessionID, serverInfo.Name, &Capabilities{
			Tools: tools, Resources: resources, Prompts: prompts,
		}); storeErr != nil {
			logging.Warn("Connection", "Failed to store capabilities for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverInfo.Name, storeErr)
		}
	}

	if a.authStore != nil {
		if err := a.authStore.MarkAuthenticated(ctx, sessionID, serverInfo.Name); err != nil {
			logging.Warn("Connection", "Failed to mark auth for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverInfo.Name, err)
		}
	}

	// Sync service state to Connected now that token exchange succeeded
	notifyMCPServerConnected(serverInfo.Name, "RFC 8693 token exchange")

	logging.Info("Connection", "User %s connected to %s via RFC 8693 token exchange with %d tools",
		logging.TruncateIdentifier(sub), serverInfo.Name, len(tools))

	return &ConnectionResult{
		ServerName:     serverInfo.Name,
		ToolCount:      len(tools),
		ResourceCount:  len(resources),
		PromptCount:    len(prompts),
		Client:         client,
		TokenExpiry:    tokenExpiry,
		ExchangedToken: exchangedToken,
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
		logging.Warn("Connection", "No namespace set for server %s event, defaulting to 'default' - check MCPServer configuration", serverName)
		namespace = "default" //nolint:goconst
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

// tokenExchangeRefreshMargin is the time before expiry at which a pooled
// token-exchange client triggers a background re-exchange. Dex access tokens
// typically live for 30 minutes, so a 5-minute margin gives ample time for
// the background goroutine to complete the exchange + client initialization
// without blocking the user's request.
const tokenExchangeRefreshMargin = 5 * time.Minute

// headerFuncWarnInterval is the minimum interval between WARN-level log messages
// when the headerFunc closure fails to resolve an ID token. Between warnings,
// failures are logged at DEBUG to avoid flooding logs from stale sessions.
const headerFuncWarnInterval = 1 * time.Minute

// maxConsecutiveTokenFailures is the number of consecutive token resolution
// failures in the headerFunc before the stale connection is signaled for
// eviction. At mcp-go's 1-second retry interval, 3 failures ≈ 3 seconds of
// retrying with stale tokens before the connection is proactively closed.
const maxConsecutiveTokenFailures = 3

// makeTokenForwardingHeaderFunc creates the header function closure used by
// EstablishConnectionWithTokenForwarding. The returned closure resolves the latest
// ID token from the OAuth store on each invocation and falls back to fallbackToken
// when the store lookup fails.
//
// Warning rate-limiting: when the token lookup fails, a WARN is emitted at most
// once per headerFuncWarnInterval. Subsequent failures within the window are logged
// at DEBUG. When the token recovers after a failure period, an INFO is emitted.
//
// Stale token eviction: after maxConsecutiveTokenFailures consecutive failures,
// the onStaleToken callback is invoked asynchronously (in a goroutine) to evict
// the connection from the pool and close the client. This stops mcp-go's infinite
// retry loop because closing the client cancels the transport's context.
// The callback fires at most once per consecutive failure streak; the counter
// resets when a valid token is resolved, allowing re-eviction if the token
// disappears again after recovery.
//
// The closure is safe to call without a mutex because headerFunc is called
// sequentially per connection by the MCP client.
func makeTokenForwardingHeaderFunc(
	sessionID, sub, musterIssuer, serverName, fallbackToken string,
	onStaleToken func(),
) func(context.Context) map[string]string {
	var lastWarnTime time.Time
	var consecutiveFailures int
	var staleEvicted bool
	hadToken := true
	return func(_ context.Context) map[string]string {
		latestToken := getIDTokenForForwarding(context.Background(), sessionID, musterIssuer)
		if latestToken == "" {
			consecutiveFailures++

			if time.Since(lastWarnTime) >= headerFuncWarnInterval {
				logging.Warn("Connection", "Authentication failed: no ID token in OAuth store for session %s to %s, using original token (%d/%d consecutive failures)",
					logging.TruncateIdentifier(sessionID), serverName, consecutiveFailures, maxConsecutiveTokenFailures)
				lastWarnTime = time.Now()
			} else {
				logging.Debug("Connection", "Authentication failed: no ID token in OAuth store for session %s to %s, using original token (warning suppressed, %d/%d consecutive failures)",
					logging.TruncateIdentifier(sessionID), serverName, consecutiveFailures, maxConsecutiveTokenFailures)
			}
			hadToken = false

			if consecutiveFailures >= maxConsecutiveTokenFailures && !staleEvicted && onStaleToken != nil {
				staleEvicted = true
				logging.Warn("Connection", "Token resolution failed %d consecutive times for session %s to %s — evicting stale connection",
					consecutiveFailures, logging.TruncateIdentifier(sessionID), serverName)
				go func() {
					defer func() {
						if r := recover(); r != nil {
							logging.Error("Connection", fmt.Errorf("panic in onStaleToken: %v", r),
								"onStaleToken callback panicked for session %s to %s",
								logging.TruncateIdentifier(sessionID), serverName)
						}
					}()
					onStaleToken()
				}()
			}

			latestToken = fallbackToken
		} else {
			if !hadToken {
				logging.Info("Connection", "ID token recovered in OAuth store for session %s to %s",
					logging.TruncateIdentifier(sessionID), serverName)
			}
			consecutiveFailures = 0
			staleEvicted = false
			hadToken = true
			if latestToken != fallbackToken {
				logging.Info("Connection", "Token expired, refreshing: resolved updated ID token from OAuth store for user %s to %s",
					logging.TruncateIdentifier(sub), serverName)
			}
		}
		return map[string]string{
			"Authorization": "Bearer " + latestToken,
		}
	}
}

// deferredCloseDelay is how long to wait before closing a replaced pooled
// client during background token refresh. This gives any in-flight request
// using the old client time to complete before the underlying connection is
// torn down.
const deferredCloseDelay = 60 * time.Second

// notifyMCPServerConnected updates the MCPServer service state to Connected after
// successful authentication. This syncs the session-level connection success to
// the service-level state, ensuring that `muster list mcpserver` shows the correct
// connected state.
//
// This is a best-effort operation - failures are logged at warn level but don't
// fail the connection flow.
func notifyMCPServerConnected(serverName, authMethod string) {
	if err := api.UpdateMCPServerState(serverName, api.StateConnected, api.HealthHealthy, nil); err != nil {
		logging.Warn("Connection", "Failed to update MCPServer %s state after %s: %v",
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
