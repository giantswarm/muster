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
			"Successfully connected to '%s'!\n\n"+
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
				"Successfully connected to %s!\n\n"+
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
	// Try to get ID token from multiple sources:
	// 1. OAuth proxy token store (for tokens obtained via core_auth_login to remote servers)
	// 2. Request context (for tokens from muster's OAuth server protection)
	//
	// When a user authenticates TO muster (via Google/Dex OAuth), the token is stored
	// in the OAuth server's token store and injected into the request context by
	// createAccessTokenInjectorMiddleware. This is the primary SSO use case.
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

	// Create a token key using the muster issuer (since the token is from muster's auth)
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

	// L3 fix: Log when namespace defaults to "default" for transparency
	if namespace == "" {
		logging.Debug("SessionConnection", "No namespace set for server %s event, defaulting to 'default'", serverName)
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
func ShouldUseTokenForwarding(serverInfo *ServerInfo) bool {
	if serverInfo == nil || serverInfo.AuthConfig == nil {
		return false
	}
	// Use case-insensitive comparison for auth type (L2 fix)
	return strings.EqualFold(serverInfo.AuthConfig.Type, "oauth") && serverInfo.AuthConfig.ForwardToken
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

	// Decode the payload (second part)
	// JWT uses base64url encoding without padding
	payload := parts[1]
	// Add padding if necessary
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		// Try standard base64 as fallback (some implementations use it)
		decoded, err = base64.StdEncoding.DecodeString(parts[1])
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
