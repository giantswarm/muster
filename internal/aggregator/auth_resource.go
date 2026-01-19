package aggregator

import (
	"context"
	"encoding/json"

	"muster/internal/config"
	"muster/pkg/logging"
	pkgoauth "muster/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
)

// AuthStatusResourceURI is the URI for the auth status MCP resource.
// This resource provides real-time authentication status for all MCP servers.
const AuthStatusResourceURI = "auth://status"

// registerAuthStatusResource registers the auth://status resource with the MCP server.
// This resource is polled by the agent to get current auth state for all servers.
func (a *AggregatorServer) registerAuthStatusResource() {
	a.mu.RLock()
	mcpServer := a.mcpServer
	a.mu.RUnlock()

	if mcpServer == nil {
		logging.Warn("Aggregator", "Cannot register auth status resource: MCP server not initialized")
		return
	}

	// Create the resource
	resource := mcp.NewResource(
		AuthStatusResourceURI,
		"Authentication status for all MCP servers. Provides information about which servers require authentication and their OAuth issuer URLs for SSO detection.",
	)

	// Add the resource with its handler
	mcpServer.AddResource(resource, a.handleAuthStatusResource)
	logging.Info("Aggregator", "Registered auth://status resource")
}

// handleAuthStatusResource handles requests for the auth://status resource.
// It returns the authentication status of all registered MCP servers.
//
// This handler provides session-specific authentication status. For OAuth-protected
// servers that require per-session authentication:
//   - If the current session has an authenticated connection, status is "connected"
//   - If the current session has not authenticated, status is "auth_required"
//
// For servers with forwardToken: true (SSO-enabled), this handler proactively
// attempts to establish a connection using the user's muster ID token. This enables
// seamless SSO where users authenticate once to muster and automatically gain
// access to all SSO-enabled MCP servers.
//
// This enables the CLI to correctly show whether the user is authenticated to each
// MCP server, not just whether the server requires authentication globally.
func (a *AggregatorServer) handleAuthStatusResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	// Get session ID from context for session-specific auth status
	sessionID := getSessionIDFromContext(ctx)

	// Get muster issuer for SSO token forwarding
	musterIssuer := a.getMusterIssuer()

	servers := a.registry.GetAllServers()
	response := pkgoauth.AuthStatusResponse{Servers: make([]pkgoauth.ServerAuthStatus, 0, len(servers))}

	for name, info := range servers {
		// Check if this server uses SSO via token forwarding
		usesTokenForwarding := ShouldUseTokenForwarding(info)

		status := pkgoauth.ServerAuthStatus{
			Name:                   name,
			Status:                 string(info.Status),
			TokenForwardingEnabled: usesTokenForwarding,
		}

		// For servers requiring auth globally, check if the current session has authenticated
		if info.Status == StatusAuthRequired && info.AuthInfo != nil {
			sessionAuthenticated := false

			// Check if this session has an authenticated connection to this server
			// (only if session registry is available - may be nil in tests)
			if a.sessionRegistry != nil {
				if conn, exists := a.sessionRegistry.GetConnection(sessionID, name); exists && conn != nil && conn.Status == StatusSessionConnected {
					sessionAuthenticated = true
					status.Status = "connected"
					logging.Debug("Aggregator", "Session %s has authenticated connection to %s",
						logging.TruncateSessionID(sessionID), name)
				}
			}

			// If not connected, check if SSO via token forwarding is available
			if !sessionAuthenticated && usesTokenForwarding && musterIssuer != "" {
				// Attempt proactive SSO connection using muster's ID token
				result, _, err := EstablishSessionConnectionWithTokenForwarding(
					ctx, a, sessionID, info, musterIssuer,
				)
				if err == nil && result != nil {
					// SSO connection succeeded
					sessionAuthenticated = true
					status.Status = "connected"
					logging.Info("Aggregator", "Proactively connected session %s to SSO server %s via token forwarding",
						logging.TruncateSessionID(sessionID), name)
				} else {
					logging.Debug("Aggregator", "SSO token forwarding failed for %s (session %s): %v",
						name, logging.TruncateSessionID(sessionID), err)
				}
			}

			if !sessionAuthenticated {
				// Session has not authenticated - include auth tool info
				status.Issuer = info.AuthInfo.Issuer
				status.Scope = info.AuthInfo.Scope
				// Per ADR-008: Use core_auth_login with server parameter instead of synthetic tools
				status.AuthTool = "core_auth_login"
			}
		} else if info.IsConnected() {
			status.Status = "connected"
		} else {
			status.Status = "disconnected"
		}

		response.Servers = append(response.Servers, status)
	}

	data, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}

	logging.Debug("Aggregator", "Returning auth status for %d servers (session=%s)",
		len(response.Servers), logging.TruncateSessionID(sessionID))

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      AuthStatusResourceURI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

// getMusterIssuer returns the OAuth issuer URL configured for muster's OAuth server.
// This is used for SSO token forwarding - the issuer identifies the ID token source.
func (a *AggregatorServer) getMusterIssuer() string {
	if a.config.OAuthServer.Enabled && a.config.OAuthServer.Config != nil {
		// The config is stored as interface{}, cast to the actual config type
		if cfg, ok := a.config.OAuthServer.Config.(config.OAuthServerConfig); ok {
			if cfg.BaseURL != "" {
				return cfg.BaseURL
			}
		}
	}
	return ""
}
