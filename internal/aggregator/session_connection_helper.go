package aggregator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"muster/internal/api"
	"muster/internal/events"
	internalmcp "muster/internal/mcpserver"
	"muster/internal/oauth"
	"muster/internal/server"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// SessionConnectionResult contains the result of establishing a session connection.
// This is returned by establishSessionConnection and used by callers to format
// their specific result types (api.CallToolResult or mcp.CallToolResult).
type SessionConnectionResult struct {
	// ServerName is the name of the server that was connected
	ServerName string
	// ToolCount is the number of tools available from the server
	ToolCount int
	// ResourceCount is the number of resources available from the server
	ResourceCount int
	// PromptCount is the number of prompts available from the server
	PromptCount int
}

// establishSessionConnection creates a connection to an MCP server and registers
// it with the session. This is the shared implementation used by both:
//   - AuthToolProvider.tryConnectWithToken (core_auth_login tool)
//   - AggregatorServer.tryConnectWithToken (legacy path, manager.go)
//
// This method:
//  1. Creates the appropriate client (DynamicAuthClient or static headers)
//  2. Initializes the connection and fetches capabilities
//  3. Creates and registers the session connection
//  4. Notifies the session of tool changes
//
// Args:
//   - ctx: Context for the operation
//   - a: The aggregator server instance
//   - sessionID: The session to register the connection with
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
	sessionID, serverName, serverURL, issuer, scope, accessToken string,
) (*SessionConnectionResult, error) {
	// Get OAuth handler for dynamic token refresh
	oauthHandler := api.GetOAuthHandler()

	// Create a token provider for dynamic token injection
	// If OAuth handler is available, use dynamic auth client for automatic refresh
	// Otherwise, fall back to static headers (backwards compatibility)
	var client internalmcp.MCPClient
	if oauthHandler != nil && oauthHandler.IsEnabled() && issuer != "" {
		// Create a dynamic auth client that refreshes tokens automatically
		tokenProvider := NewSessionTokenProvider(sessionID, issuer, scope, oauthHandler)
		client = internalmcp.NewDynamicAuthClient(serverURL, tokenProvider)
		logging.Debug("SessionConnection", "Using DynamicAuthClient for session %s, server %s (issuer=%s)",
			logging.TruncateSessionID(sessionID), serverName, issuer)
	} else {
		// Fallback to static headers
		headers := map[string]string{
			"Authorization": "Bearer " + accessToken,
		}
		client = internalmcp.NewStreamableHTTPClientWithHeaders(serverURL, headers)
		logging.Debug("SessionConnection", "Using static auth headers for session %s, server %s",
			logging.TruncateSessionID(sessionID), serverName)
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
		logging.Debug("SessionConnection", "Failed to list resources for session %s, server %s: %v",
			logging.TruncateSessionID(sessionID), serverName, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("SessionConnection", "Failed to list prompts for session %s, server %s: %v",
			logging.TruncateSessionID(sessionID), serverName, err)
		prompts = nil
	}

	// Upgrade the session connection
	session := a.sessionRegistry.GetOrCreateSession(sessionID)

	// Create the token key for future reference
	var tokenKey *oauth.TokenKey
	if issuer != "" {
		tokenKey = &oauth.TokenKey{
			SessionID: sessionID,
			Issuer:    issuer,
			Scope:     scope,
		}
	}

	// Create the session connection
	conn := &SessionConnection{
		ServerName:  serverName,
		Status:      StatusSessionConnected,
		Client:      client,
		TokenKey:    tokenKey,
		ConnectedAt: time.Now(),
	}
	conn.UpdateTools(tools)
	conn.UpdateResources(resources)
	conn.UpdatePrompts(prompts)

	session.SetConnection(serverName, conn)

	// Register the session-specific tools with the mcp-go server so they can be called
	a.registerSessionTools(serverName, tools)

	// Send targeted notification to the session that their tools have changed
	a.NotifySessionToolsChanged(sessionID)

	logging.Info("SessionConnection", "Session %s connected to %s with %d tools, %d resources, %d prompts",
		logging.TruncateSessionID(sessionID), serverName, len(tools), len(resources), len(prompts))

	return &SessionConnectionResult{
		ServerName:    serverName,
		ToolCount:     len(tools),
		ResourceCount: len(resources),
		PromptCount:   len(prompts),
	}, nil
}

// FormatAsAPIResult formats the connection result as an api.CallToolResult.
// Used by AuthToolProvider.tryConnectWithToken.
func (r *SessionConnectionResult) FormatAsAPIResult() *api.CallToolResult {
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
func (r *SessionConnectionResult) FormatAsMCPResult() *mcp.CallToolResult {
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
//     via core_auth_login. These are keyed by (sessionID, issuer).
//
// The context token takes priority because it represents the user's current authentication
// to muster, which is what we want to forward for SSO.
//
// Args:
//   - ctx: Request context that may contain an injected ID token
//   - sessionID: The session identifier
//   - musterIssuer: The issuer URL to look up in the OAuth proxy store
//
// Returns the ID token string, or empty string if no token is available.
func getIDTokenForForwarding(ctx context.Context, sessionID, musterIssuer string) string {
	// First, check the request context for an ID token from muster's OAuth server protection.
	// This is the primary SSO use case: user authenticates TO muster, and we forward that
	// token to downstream servers that trust muster's OAuth client ID.
	if idToken, ok := server.GetAccessTokenFromContext(ctx); ok && idToken != "" {
		logging.Debug("SessionConnection", "Found ID token in request context for session %s",
			logging.TruncateSessionID(sessionID))
		return idToken
	}

	// Fallback: check the OAuth proxy token store.
	// This handles the case where tokens were obtained via a previous core_auth_login call.
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler != nil && oauthHandler.IsEnabled() && musterIssuer != "" {
		fullToken := oauthHandler.GetFullTokenByIssuer(sessionID, musterIssuer)
		if fullToken != nil && fullToken.IDToken != "" {
			logging.Debug("SessionConnection", "Found ID token in OAuth proxy store for session %s, issuer %s",
				logging.TruncateSessionID(sessionID), musterIssuer)
			return fullToken.IDToken
		}
	}

	logging.Debug("SessionConnection", "No ID token found for session %s",
		logging.TruncateSessionID(sessionID))
	return ""
}

// EstablishSessionConnectionWithTokenForwarding attempts to establish a session connection
// using ID token forwarding for SSO. This is used when an MCPServer has forwardToken: true.
//
// The function:
//  1. Gets the user's ID token from muster's OAuth session
//  2. Forwards it to the downstream MCP server
//  3. If successful, establishes the session connection
//  4. If forwarding fails and fallbackToOwnAuth is true, returns an error indicating fallback is needed
//
// Args:
//   - ctx: Context for the operation
//   - a: The aggregator server instance
//   - sessionID: The session to register the connection with
//   - serverInfo: The server info containing URL and auth config
//   - musterIssuer: The issuer URL of muster's OAuth provider (used to get the ID token)
//
// Returns:
//   - *SessionConnectionResult: The connection result if successful
//   - needsFallback: true if token forwarding failed and fallback is configured
//   - error: The error if connection failed
func EstablishSessionConnectionWithTokenForwarding(
	ctx context.Context,
	a *AggregatorServer,
	sessionID string,
	serverInfo *ServerInfo,
	musterIssuer string,
) (*SessionConnectionResult, bool, error) {
	// Try to get ID token from multiple sources (in priority order):
	// 1. Request context (for tokens from muster's OAuth server protection)
	// 2. OAuth proxy token store (for tokens obtained via core_auth_login to remote servers)
	//
	// When a user authenticates TO muster (via Google/Dex OAuth), the token is
	// injected into the request context by createAccessTokenInjectorMiddleware.
	// This is the primary SSO use case.
	idToken := getIDTokenForForwarding(ctx, sessionID, musterIssuer)
	if idToken == "" {
		logging.Debug("SessionConnection", "No ID token available for session %s, fallback to regular auth",
			logging.TruncateSessionID(sessionID))
		return nil, true, fmt.Errorf("no ID token available for forwarding")
	}

	// Validate ID token is not expired before forwarding
	// This avoids unnecessary network round-trips with expired tokens
	if isIDTokenExpired(idToken) {
		logging.Warn("SessionConnection", "ID token expired for session %s, cannot forward to %s",
			logging.TruncateSessionID(sessionID), serverInfo.Name)
		return nil, true, fmt.Errorf("ID token has expired, needs refresh before forwarding")
	}

	logging.Info("SessionConnection", "Attempting ID token forwarding for session %s to server %s",
		logging.TruncateSessionID(sessionID), serverInfo.Name)

	// Create a client with the forwarded ID token
	headers := map[string]string{
		"Authorization": "Bearer " + idToken,
	}
	client := internalmcp.NewStreamableHTTPClientWithHeaders(serverInfo.URL, headers)

	// Try to initialize the client with the forwarded token
	if err := client.Initialize(ctx); err != nil {
		client.Close()

		// Log the token forwarding failure
		logging.Warn("SessionConnection", "ID token forwarding failed for session %s to server %s: %v",
			logging.TruncateSessionID(sessionID), serverInfo.Name, err)

		// Emit event for token forwarding failure
		emitTokenForwardingEvent(serverInfo.Name, serverInfo.GetNamespace(), false, err.Error())

		// Check if fallback is configured
		if serverInfo.AuthConfig != nil && serverInfo.AuthConfig.FallbackToOwnAuth {
			return nil, true, fmt.Errorf("ID token forwarding failed: %w", err)
		}
		return nil, false, fmt.Errorf("ID token forwarding failed and fallback disabled: %w", err)
	}

	// Token forwarding succeeded - emit success event
	logging.Info("SessionConnection", "ID token forwarding succeeded for session %s to server %s",
		logging.TruncateSessionID(sessionID), serverInfo.Name)
	emitTokenForwardingEvent(serverInfo.Name, serverInfo.GetNamespace(), true, "")

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return nil, true, fmt.Errorf("failed to list tools after token forwarding: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("SessionConnection", "Failed to list resources for session %s, server %s: %v",
			logging.TruncateSessionID(sessionID), serverInfo.Name, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("SessionConnection", "Failed to list prompts for session %s, server %s: %v",
			logging.TruncateSessionID(sessionID), serverInfo.Name, err)
		prompts = nil
	}

	// Upgrade the session connection
	session := a.sessionRegistry.GetOrCreateSession(sessionID)

	// Create a token key using the muster issuer (since the token is from muster's auth).
	// Scope is intentionally omitted: this is a forwarded ID token from muster's OAuth,
	// not a token obtained via server-specific OAuth flow with its own scopes.
	tokenKey := &oauth.TokenKey{
		SessionID: sessionID,
		Issuer:    musterIssuer,
	}

	// Create the session connection
	conn := &SessionConnection{
		ServerName:  serverInfo.Name,
		Status:      StatusSessionConnected,
		Client:      client,
		TokenKey:    tokenKey,
		ConnectedAt: time.Now(),
	}
	conn.UpdateTools(tools)
	conn.UpdateResources(resources)
	conn.UpdatePrompts(prompts)

	session.SetConnection(serverInfo.Name, conn)

	// Register the session-specific tools
	a.registerSessionTools(serverInfo.Name, tools)

	// Notify the session
	a.NotifySessionToolsChanged(sessionID)

	logging.Info("SessionConnection", "Session %s connected to %s via SSO token forwarding with %d tools",
		logging.TruncateSessionID(sessionID), serverInfo.Name, len(tools))

	return &SessionConnectionResult{
		ServerName:    serverInfo.Name,
		ToolCount:     len(tools),
		ResourceCount: len(resources),
		PromptCount:   len(prompts),
	}, false, nil
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

// EstablishSessionConnectionWithTokenExchange attempts to establish a session connection
// using RFC 8693 Token Exchange for cross-cluster SSO. This is used when an MCPServer has
// tokenExchange configured.
//
// The function:
//  1. Gets the user's ID token from muster's OAuth session
//  2. Extracts the user ID (sub claim) from the token
//  3. Exchanges it for a token valid on the remote cluster's Dex
//  4. If successful, establishes the session connection with the exchanged token
//  5. If exchange fails and fallbackToOwnAuth is true, returns an error indicating fallback is needed
//
// Args:
//   - ctx: Context for the operation
//   - a: The aggregator server instance
//   - sessionID: The session to register the connection with
//   - serverInfo: The server info containing URL and auth config
//   - musterIssuer: The issuer URL of muster's OAuth provider (used to get the ID token)
//
// Returns:
//   - *SessionConnectionResult: The connection result if successful
//   - needsFallback: true if token exchange failed and fallback is configured
//   - error: The error if connection failed
func EstablishSessionConnectionWithTokenExchange(
	ctx context.Context,
	a *AggregatorServer,
	sessionID string,
	serverInfo *ServerInfo,
	musterIssuer string,
) (*SessionConnectionResult, bool, error) {
	// Get the OAuth handler for token exchange
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return nil, true, fmt.Errorf("OAuth handler not available for token exchange")
	}

	// Get ID token from multiple sources (in priority order):
	// 1. Request context (for tokens from muster's OAuth server protection)
	// 2. OAuth proxy token store (for tokens obtained via core_auth_login)
	idToken := getIDTokenForForwarding(ctx, sessionID, musterIssuer)
	if idToken == "" {
		logging.Debug("SessionConnection", "No ID token available for session %s, fallback to regular auth",
			logging.TruncateSessionID(sessionID))
		return nil, true, fmt.Errorf("no ID token available for token exchange")
	}

	// Validate ID token is not expired before exchanging
	if isIDTokenExpired(idToken) {
		logging.Warn("SessionConnection", "ID token expired for session %s, cannot exchange for %s",
			logging.TruncateSessionID(sessionID), serverInfo.Name)
		return nil, true, fmt.Errorf("ID token has expired, needs refresh before exchange")
	}

	// Extract user ID from the token for cache key generation
	userID := extractUserIDFromToken(idToken)
	if userID == "" {
		logging.Warn("SessionConnection", "Failed to extract user ID from token for session %s",
			logging.TruncateSessionID(sessionID))
		return nil, true, fmt.Errorf("failed to extract user ID from token")
	}

	logging.Info("SessionConnection", "Attempting token exchange for session %s to server %s",
		logging.TruncateSessionID(sessionID), serverInfo.Name)

	// Perform the token exchange
	exchangedToken, err := oauthHandler.ExchangeTokenForRemoteCluster(
		ctx,
		idToken,
		userID,
		serverInfo.AuthConfig.TokenExchange,
	)
	if err != nil {
		logging.Warn("SessionConnection", "Token exchange failed for session %s to server %s: %v",
			logging.TruncateSessionID(sessionID), serverInfo.Name, err)

		// Emit event for token exchange failure
		emitTokenExchangeEvent(serverInfo.Name, serverInfo.GetNamespace(), false, err.Error())

		// Audit log for failed token exchange (compliance/security monitoring)
		logging.Audit(logging.AuditEvent{
			Action:    "token_exchange",
			Outcome:   "failure",
			SessionID: logging.TruncateSessionID(sessionID),
			UserID:    logging.TruncateSessionID(userID),
			Target:    serverInfo.Name,
			Details:   fmt.Sprintf("endpoint=%s connector=%s", serverInfo.AuthConfig.TokenExchange.DexTokenEndpoint, serverInfo.AuthConfig.TokenExchange.ConnectorID),
			Error:     err.Error(),
		})

		// Check if fallback is configured
		if serverInfo.AuthConfig.FallbackToOwnAuth {
			return nil, true, fmt.Errorf("token exchange failed: %w", err)
		}
		return nil, false, fmt.Errorf("token exchange failed and fallback disabled: %w", err)
	}

	// Token exchange succeeded - emit success event and audit log
	logging.Info("SessionConnection", "Token exchange succeeded for session %s to server %s",
		logging.TruncateSessionID(sessionID), serverInfo.Name)
	emitTokenExchangeEvent(serverInfo.Name, serverInfo.GetNamespace(), true, "")

	// Audit log for successful token exchange (compliance/security monitoring)
	logging.Audit(logging.AuditEvent{
		Action:    "token_exchange",
		Outcome:   "success",
		SessionID: logging.TruncateSessionID(sessionID),
		UserID:    logging.TruncateSessionID(userID),
		Target:    serverInfo.Name,
		Details:   fmt.Sprintf("endpoint=%s connector=%s", serverInfo.AuthConfig.TokenExchange.DexTokenEndpoint, serverInfo.AuthConfig.TokenExchange.ConnectorID),
	})

	// Create a client with the exchanged token
	headers := map[string]string{
		"Authorization": "Bearer " + exchangedToken,
	}
	client := internalmcp.NewStreamableHTTPClientWithHeaders(serverInfo.URL, headers)

	// Try to initialize the client with the exchanged token
	if err := client.Initialize(ctx); err != nil {
		client.Close()

		logging.Warn("SessionConnection", "Connection with exchanged token failed for session %s to server %s: %v",
			logging.TruncateSessionID(sessionID), serverInfo.Name, err)

		// Check if fallback is configured
		if serverInfo.AuthConfig.FallbackToOwnAuth {
			return nil, true, fmt.Errorf("connection with exchanged token failed: %w", err)
		}
		return nil, false, fmt.Errorf("connection with exchanged token failed and fallback disabled: %w", err)
	}

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return nil, true, fmt.Errorf("failed to list tools after token exchange: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("SessionConnection", "Failed to list resources for session %s, server %s: %v",
			logging.TruncateSessionID(sessionID), serverInfo.Name, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("SessionConnection", "Failed to list prompts for session %s, server %s: %v",
			logging.TruncateSessionID(sessionID), serverInfo.Name, err)
		prompts = nil
	}

	// Upgrade the session connection
	session := a.sessionRegistry.GetOrCreateSession(sessionID)

	// Create a token key using the muster issuer (since the original token is from muster's auth).
	tokenKey := &oauth.TokenKey{
		SessionID: sessionID,
		Issuer:    musterIssuer,
	}

	// Create the session connection
	conn := &SessionConnection{
		ServerName:  serverInfo.Name,
		Status:      StatusSessionConnected,
		Client:      client,
		TokenKey:    tokenKey,
		ConnectedAt: time.Now(),
	}
	conn.UpdateTools(tools)
	conn.UpdateResources(resources)
	conn.UpdatePrompts(prompts)

	session.SetConnection(serverInfo.Name, conn)

	// Register the session-specific tools
	a.registerSessionTools(serverInfo.Name, tools)

	// Notify the session
	a.NotifySessionToolsChanged(sessionID)

	logging.Info("SessionConnection", "Session %s connected to %s via RFC 8693 token exchange with %d tools",
		logging.TruncateSessionID(sessionID), serverInfo.Name, len(tools))

	return &SessionConnectionResult{
		ServerName:    serverInfo.Name,
		ToolCount:     len(tools),
		ResourceCount: len(resources),
		PromptCount:   len(prompts),
	}, false, nil
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
	if idToken == "" {
		return ""
	}

	// JWT format: header.payload.signature
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}

	// Decode the payload using RawURLEncoding (handles missing padding automatically)
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try standard base64 as fallback for non-standard implementations
		decoded, err = base64.RawStdEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
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
	if idToken == "" {
		return true
	}

	// JWT format: header.payload.signature
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		logging.Debug("TokenValidation", "ID token has invalid format (expected JWT)")
		return true
	}

	// Decode the payload using RawURLEncoding (handles missing padding automatically)
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try standard base64 as fallback for non-standard implementations
		decoded, err = base64.RawStdEncoding.DecodeString(parts[1])
		if err != nil {
			logging.Debug("TokenValidation", "Failed to decode ID token payload: %v", err)
			return true
		}
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
