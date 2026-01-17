package aggregator

import (
	"context"
	"fmt"
	"time"

	"muster/internal/api"
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
