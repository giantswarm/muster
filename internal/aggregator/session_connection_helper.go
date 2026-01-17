package aggregator

import (
	"context"
	"fmt"
	"time"

	"muster/internal/api"
	"muster/internal/events"
	internalmcp "muster/internal/mcpserver"
	"muster/internal/oauth"
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
	// Get OAuth handler
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return nil, true, fmt.Errorf("OAuth handler not available for token forwarding")
	}

	// Get the full token (including ID token) for the muster session
	fullToken := oauthHandler.GetFullTokenByIssuer(sessionID, musterIssuer)
	if fullToken == nil || fullToken.IDToken == "" {
		logging.Debug("SessionConnection", "No ID token available for session %s from issuer %s, fallback to regular auth",
			logging.TruncateSessionID(sessionID), musterIssuer)
		return nil, true, fmt.Errorf("no ID token available for forwarding")
	}

	logging.Info("SessionConnection", "Attempting ID token forwarding for session %s to server %s",
		logging.TruncateSessionID(sessionID), serverInfo.Name)

	// Create a client with the forwarded ID token
	headers := map[string]string{
		"Authorization": "Bearer " + fullToken.IDToken,
	}
	client := internalmcp.NewStreamableHTTPClientWithHeaders(serverInfo.URL, headers)

	// Try to initialize the client with the forwarded token
	if err := client.Initialize(ctx); err != nil {
		client.Close()

		// Log the token forwarding failure
		logging.Warn("SessionConnection", "ID token forwarding failed for session %s to server %s: %v",
			logging.TruncateSessionID(sessionID), serverInfo.Name, err)

		// Emit event for token forwarding failure
		emitTokenForwardingEvent(serverInfo.Name, false, err.Error())

		// Check if fallback is configured
		if serverInfo.AuthConfig != nil && serverInfo.AuthConfig.FallbackToOwnAuth {
			return nil, true, fmt.Errorf("ID token forwarding failed: %w", err)
		}
		return nil, false, fmt.Errorf("ID token forwarding failed and fallback disabled: %w", err)
	}

	// Token forwarding succeeded - emit success event
	logging.Info("SessionConnection", "ID token forwarding succeeded for session %s to server %s",
		logging.TruncateSessionID(sessionID), serverInfo.Name)
	emitTokenForwardingEvent(serverInfo.Name, true, "")

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return nil, true, fmt.Errorf("failed to list tools after token forwarding: %w", err)
	}

	// Fetch resources and prompts (optional)
	resources, _ := client.ListResources(ctx)
	prompts, _ := client.ListPrompts(ctx)

	// Upgrade the session connection
	session := a.sessionRegistry.GetOrCreateSession(sessionID)

	// Create a token key using the muster issuer (since the token is from muster's auth)
	tokenKey := &oauth.TokenKey{
		SessionID: sessionID,
		Issuer:    musterIssuer,
		Scope:     fullToken.Scope,
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
func emitTokenForwardingEvent(serverName string, success bool, errorMsg string) {
	eventManager := api.GetEventManager()
	if eventManager == nil {
		return
	}

	var reason string
	var message string
	var eventType string

	if success {
		reason = string(events.ReasonMCPServerTokenForwarded)
		message = fmt.Sprintf("ID token successfully forwarded for SSO authentication to MCPServer %s", serverName)
		eventType = "Normal"
	} else {
		reason = string(events.ReasonMCPServerTokenForwardingFailed)
		message = fmt.Sprintf("ID token forwarding failed for MCPServer %s", serverName)
		if errorMsg != "" {
			message = fmt.Sprintf("%s: %s", message, errorMsg)
		}
		eventType = "Warning"
	}

	// Create object reference for the MCPServer
	objRef := api.ObjectReference{
		Kind:      "MCPServer",
		Name:      serverName,
		Namespace: "default",
	}

	if err := eventManager.CreateEvent(context.Background(), objRef, reason, message, eventType); err != nil {
		logging.Debug("SessionConnection", "Failed to emit token forwarding event: %v", err)
	}
}

// ShouldUseTokenForwarding checks if token forwarding should be used for a server.
func ShouldUseTokenForwarding(serverInfo *ServerInfo) bool {
	if serverInfo == nil || serverInfo.AuthConfig == nil {
		return false
	}
	return serverInfo.AuthConfig.Type == "oauth" && serverInfo.AuthConfig.ForwardToken
}
