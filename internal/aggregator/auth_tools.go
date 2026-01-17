package aggregator

import (
	"context"
	"fmt"
	"time"

	"muster/internal/api"
	internalmcp "muster/internal/mcpserver"
	"muster/internal/oauth"
	"muster/pkg/logging"
	pkgoauth "muster/pkg/oauth"
)

// AuthToolProvider provides core authentication tools for the aggregator.
// These tools allow users to authenticate to OAuth-protected MCP servers
// through `core_auth_login` and `core_auth_logout` commands.
//
// This implements ADR-008: Authentication is a muster platform concern,
// not an MCP server concern. Instead of synthetic per-server authenticate
// tools, we use core tools that take a server parameter.
type AuthToolProvider struct {
	aggregator *AggregatorServer
}

// NewAuthToolProvider creates a new authentication tool provider.
func NewAuthToolProvider(aggregator *AggregatorServer) *AuthToolProvider {
	return &AuthToolProvider{
		aggregator: aggregator,
	}
}

// GetTools returns metadata for the authentication tools.
// These tools are prefixed with "auth_" and get converted to "core_auth_*" by prefixToolName.
func (p *AuthToolProvider) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		{
			Name:        "auth_login",
			Description: "Initiate OAuth login flow for a specific MCP server. Returns an OAuth URL for the user to complete authentication in their browser.",
			Args: []api.ArgMetadata{
				{
					Name:        "server",
					Type:        "string",
					Required:    true,
					Description: "Name of the MCP server to authenticate to",
				},
			},
		},
		{
			Name:        "auth_logout",
			Description: "Clear authentication session for a specific MCP server. The server's tools will be hidden until re-authentication.",
			Args: []api.ArgMetadata{
				{
					Name:        "server",
					Type:        "string",
					Required:    true,
					Description: "Name of the MCP server to log out from",
				},
			},
		},
	}
}

// ExecuteTool executes an authentication tool by name.
func (p *AuthToolProvider) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
	case "auth_login":
		return p.handleAuthLogin(ctx, args)
	case "auth_logout":
		return p.handleAuthLogout(ctx, args)
	default:
		return nil, fmt.Errorf("unknown auth tool: %s", toolName)
	}
}

// handleAuthLogin initiates OAuth login flow for a specific MCP server.
// This implements the logic previously in handleSyntheticAuthTool, but as a core tool.
func (p *AuthToolProvider) handleAuthLogin(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return &api.CallToolResult{
			Content: []interface{}{"Error: 'server' argument is required and must be a string"},
			IsError: true,
		}, nil
	}

	logging.Info("AuthTools", "Handling auth login for server: %s", serverName)

	// Get server info from registry
	serverInfo, exists := p.aggregator.registry.GetServerInfo(serverName)
	if !exists {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Server '%s' not found. Use list_tools to see available servers.", serverName)},
			IsError: true,
		}, nil
	}

	if serverInfo.Status != StatusAuthRequired {
		// Server is already connected or doesn't require auth
		if serverInfo.IsConnected() {
			return &api.CallToolResult{
				Content: []interface{}{fmt.Sprintf("Server '%s' is already authenticated and connected.", serverName)},
				IsError: false,
			}, nil
		}
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Server '%s' does not require authentication.", serverName)},
			IsError: false,
		}, nil
	}

	// Check if OAuth handler is available
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf(
				"OAuth is not configured. Server '%s' requires authentication but OAuth proxy is not enabled. "+
					"Enable OAuth proxy in the configuration to authenticate to remote MCP servers.",
				serverName,
			)},
			IsError: true,
		}, nil
	}

	// Get session ID from context
	sessionID := getSessionIDFromContext(ctx)

	// Get the auth info for this server
	authInfo := serverInfo.AuthInfo
	if authInfo == nil {
		authInfo = &AuthInfo{}
	}

	// If issuer or scope is empty, try to discover it from the server's resource metadata
	if (authInfo.Issuer == "" || authInfo.Scope == "") && serverInfo.URL != "" {
		metadata, err := discoverProtectedResourceMetadata(ctx, serverInfo.URL)
		if err != nil {
			logging.Warn("AuthTools", "Failed to discover protected resource metadata for %s: %v", serverName, err)
		} else {
			if authInfo.Issuer == "" {
				authInfo.Issuer = metadata.Issuer
				logging.Info("AuthTools", "Discovered authorization server for %s: %s", serverName, metadata.Issuer)
			}
			if authInfo.Scope == "" && metadata.Scope != "" {
				authInfo.Scope = metadata.Scope
				logging.Info("AuthTools", "Discovered required scope for %s: %s", serverName, metadata.Scope)
			}
		}
	}

	// If still empty, we can't proceed
	if authInfo.Issuer == "" {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf(
				"Cannot authenticate to '%s': unable to discover OAuth authorization server. "+
					"The server's /.well-known/oauth-protected-resource endpoint may not be available.",
				serverName,
			)},
			IsError: true,
		}, nil
	}

	// Check if we already have a valid token for this server/issuer
	token := oauthHandler.GetTokenByIssuer(sessionID, authInfo.Issuer)
	if token != nil {
		logging.Info("AuthTools", "Found existing token for server %s, attempting to connect", serverName)

		// Try to establish connection using the existing token
		connectResult, connectErr := p.tryConnectWithToken(ctx, sessionID, serverName, serverInfo.URL, authInfo.Issuer, authInfo.Scope, token.AccessToken)
		if connectErr == nil {
			return connectResult, nil
		}

		// Check if the error is a 401 - token is expired/invalid
		if is401Error(connectErr) {
			logging.Info("AuthTools", "Token for server %s is expired/invalid, clearing and requesting fresh auth", serverName)
			oauthHandler.ClearTokenByIssuer(sessionID, authInfo.Issuer)
		} else {
			// Some other error - report it
			logging.Error("AuthTools", connectErr, "Failed to connect to server %s with existing token", serverName)
			return &api.CallToolResult{
				Content: []interface{}{fmt.Sprintf(
					"Failed to connect to '%s': %v\n\nPlease try again or check the server status.",
					serverName, connectErr,
				)},
				IsError: true,
			}, nil
		}
	}

	// No token - need to create an auth challenge
	challenge, err := oauthHandler.CreateAuthChallenge(ctx, sessionID, serverName, authInfo.Issuer, authInfo.Scope)
	if err != nil {
		logging.Error("AuthTools", err, "Failed to create auth challenge for server %s", serverName)
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to create authentication challenge: %v", err)},
			IsError: true,
		}, nil
	}

	// Return the auth challenge as a tool result with the sign-in link
	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf(
			"Authentication Required\n\n"+
				"Server: %s\n"+
				"Status: %s\n\n"+
				"Please sign in to connect to this server:\n\n"+
				"%s\n\n"+
				"After signing in, run this tool again to complete the connection.",
			serverName,
			challenge.Message,
			challenge.AuthURL,
		)},
		IsError: false,
	}, nil
}

// handleAuthLogout clears authentication session for a specific MCP server.
func (p *AuthToolProvider) handleAuthLogout(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return &api.CallToolResult{
			Content: []interface{}{"Error: 'server' argument is required and must be a string"},
			IsError: true,
		}, nil
	}

	logging.Info("AuthTools", "Handling auth logout for server: %s", serverName)

	// Get server info from registry
	serverInfo, exists := p.aggregator.registry.GetServerInfo(serverName)
	if !exists {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Server '%s' not found.", serverName)},
			IsError: true,
		}, nil
	}

	// Get session ID from context
	sessionID := getSessionIDFromContext(ctx)

	// Get the session and remove the connection
	session := p.aggregator.sessionRegistry.GetOrCreateSession(sessionID)
	conn, hasConnection := session.GetConnection(serverName)

	if hasConnection && conn.Client != nil {
		// Close the client connection
		if err := conn.Client.Close(); err != nil {
			logging.Warn("AuthTools", "Error closing client for %s: %v", serverName, err)
		}
	}

	// Remove the connection from the session
	session.RemoveConnection(serverName)

	// Clear tokens for this server's issuer if we have auth info
	if serverInfo.AuthInfo != nil && serverInfo.AuthInfo.Issuer != "" {
		oauthHandler := api.GetOAuthHandler()
		if oauthHandler != nil && oauthHandler.IsEnabled() {
			oauthHandler.ClearTokenByIssuer(sessionID, serverInfo.AuthInfo.Issuer)
		}
	}

	// Notify the session that tools have changed
	p.aggregator.NotifySessionToolsChanged(sessionID)

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf(
			"Successfully logged out from '%s'.\n\n"+
				"The server's tools are now hidden. Use core_auth_login with server='%s' to re-authenticate.",
			serverName, serverName,
		)},
		IsError: false,
	}, nil
}

// tryConnectWithToken attempts to establish a connection to an MCP server using an OAuth token.
// This is adapted from the aggregator's tryConnectWithToken method.
func (p *AuthToolProvider) tryConnectWithToken(ctx context.Context, sessionID, serverName, serverURL, issuer, scope, accessToken string) (*api.CallToolResult, error) {
	oauthHandler := api.GetOAuthHandler()

	// Create a token provider for dynamic token injection
	var client internalmcp.MCPClient
	if oauthHandler != nil && oauthHandler.IsEnabled() && issuer != "" {
		tokenProvider := NewSessionTokenProvider(sessionID, issuer, scope, oauthHandler)
		client = internalmcp.NewDynamicAuthClient(serverURL, tokenProvider)
		logging.Debug("AuthTools", "Using DynamicAuthClient for session %s, server %s (issuer=%s)",
			logging.TruncateSessionID(sessionID), serverName, issuer)
	} else {
		headers := map[string]string{
			"Authorization": "Bearer " + accessToken,
		}
		client = internalmcp.NewStreamableHTTPClientWithHeaders(serverURL, headers)
		logging.Debug("AuthTools", "Using static auth headers for session %s, server %s",
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

	// Fetch resources and prompts (optional)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("AuthTools", "Failed to list resources for session %s, server %s: %v",
			logging.TruncateSessionID(sessionID), serverName, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("AuthTools", "Failed to list prompts for session %s, server %s: %v",
			logging.TruncateSessionID(sessionID), serverName, err)
		prompts = nil
	}

	// Upgrade the session connection
	session := p.aggregator.sessionRegistry.GetOrCreateSession(sessionID)

	var tokenKey *oauth.TokenKey
	if issuer != "" {
		tokenKey = &oauth.TokenKey{
			SessionID: sessionID,
			Issuer:    issuer,
			Scope:     scope,
		}
	}

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

	// Register the session-specific tools
	p.aggregator.registerSessionTools(serverName, tools)

	// Send targeted notification
	p.aggregator.NotifySessionToolsChanged(sessionID)

	logging.Info("AuthTools", "Session %s connected to %s with %d tools, %d resources, %d prompts",
		logging.TruncateSessionID(sessionID), serverName, len(tools), len(resources), len(prompts))

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf(
			"Successfully connected to '%s'!\n\n"+
				"Available capabilities:\n"+
				"- Tools: %d\n"+
				"- Resources: %d\n"+
				"- Prompts: %d\n\n"+
				"You can now use the tools from this server.",
			serverName, len(tools), len(resources), len(prompts),
		)},
		IsError: false,
	}, nil
}

// is401Error checks if an error indicates a 401 Unauthorized response.
// This provides structured 401 detection as per ADR-008.
func is401Error(err error) bool {
	if err == nil {
		return false
	}
	// Check using pkg/oauth helper for structured detection
	return pkgoauth.Is401Error(err)
}
