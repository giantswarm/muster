package aggregator

import (
	"context"
	"encoding/json"

	"muster/internal/api"
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
// Note: Proactive SSO connections for servers with forwardToken: true are established
// during session initialization (see handleSessionInit), not during auth status reads.
// This ensures auth://status is a pure read operation without side effects.
//
// This enables the CLI to correctly show whether the user is authenticated to each
// MCP server, not just whether the server requires authentication globally.
func (a *AggregatorServer) handleAuthStatusResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	// Get session ID from context for session-specific auth status
	sessionID := getSessionIDFromContext(ctx)

	servers := a.registry.GetAllServers()
	response := pkgoauth.AuthStatusResponse{Servers: make([]pkgoauth.ServerAuthStatus, 0, len(servers))}

	for name, info := range servers {
		// Check if this server uses SSO via token forwarding
		usesTokenForwarding := ShouldUseTokenForwarding(info)

		// Check if SSO token reuse is enabled (default: true)
		tokenReuseEnabled := true
		if info.AuthConfig != nil && info.AuthConfig.SSO != nil {
			tokenReuseEnabled = *info.AuthConfig.SSO
		}

		// Check if SSO was attempted but failed for this session/server
		ssoAttemptFailed := false
		if a.sessionRegistry != nil && usesTokenForwarding {
			ssoAttemptFailed = a.sessionRegistry.HasSSOFailed(sessionID, name)
		}

		status := pkgoauth.ServerAuthStatus{
			Name:                   name,
			Status:                 string(info.Status),
			TokenForwardingEnabled: usesTokenForwarding,
			TokenReuseEnabled:      tokenReuseEnabled,
			SSOAttemptFailed:       ssoAttemptFailed,
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
// When users authenticate to muster via `muster auth login`, this issuer is used to
// retrieve their ID token for forwarding to SSO-enabled downstream servers.
//
// The issuer is determined from the OAuth server configuration (BaseURL).
// Returns empty string if OAuth is not enabled or not configured.
func (a *AggregatorServer) getMusterIssuer() string {
	if !a.config.OAuthServer.Enabled || a.config.OAuthServer.Config == nil {
		return ""
	}

	// The config is stored as interface{}, cast to the actual config type
	cfg, ok := a.config.OAuthServer.Config.(config.OAuthServerConfig)
	if !ok {
		// Log at warn level since this indicates a potential configuration issue
		logging.Warn("Aggregator", "OAuthServer.Config type assertion failed: got %T, expected config.OAuthServerConfig",
			a.config.OAuthServer.Config)
		return ""
	}

	return cfg.BaseURL
}

// getMusterIssuerWithFallback returns the OAuth issuer URL, with a fallback to
// finding any token in the session that has an ID token.
//
// This is useful when we need to determine the issuer for a specific session
// but the configuration might not be explicitly set. The fallback searches
// the OAuth proxy token store for any token with an ID token.
//
// Args:
//   - sessionID: The session to search for fallback tokens (only used if config lookup fails)
//
// Returns the issuer URL, or empty string if none can be determined.
func (a *AggregatorServer) getMusterIssuerWithFallback(sessionID string) string {
	// First, try to get from configuration
	if issuer := a.getMusterIssuer(); issuer != "" {
		return issuer
	}

	// Fallback: Look for any token in the session that has an ID token.
	// This is a best-effort approach when the configured issuer is not available.
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return ""
	}

	fullToken := oauthHandler.FindTokenWithIDToken(sessionID)
	if fullToken != nil && fullToken.Issuer != "" {
		logging.Debug("Aggregator", "Using fallback issuer from session token: %s", fullToken.Issuer)
		return fullToken.Issuer
	}

	return ""
}

// handleSessionInit is called on the first authenticated MCP request for a session.
// It triggers proactive SSO connections to all SSO-enabled servers (forwardToken: true)
// using muster's ID token.
//
// This enables seamless SSO: users authenticate once to muster (via `muster auth login`)
// and automatically gain access to all SSO-enabled MCP servers without needing to call
// `core_auth_login` for each server individually.
//
// Note: This callback runs asynchronously and should not block.
func (a *AggregatorServer) handleSessionInit(ctx context.Context, sessionID string) {
	// Get muster issuer for SSO token forwarding
	musterIssuer := a.getMusterIssuer()
	if musterIssuer == "" {
		logging.Debug("Aggregator", "Session init: No muster issuer configured, skipping proactive SSO")
		return
	}

	servers := a.registry.GetAllServers()
	var ssoServers []*ServerInfo

	// Find all SSO-enabled servers that require authentication
	for _, info := range servers {
		if info.Status == StatusAuthRequired && ShouldUseTokenForwarding(info) {
			ssoServers = append(ssoServers, info)
		}
	}

	if len(ssoServers) == 0 {
		logging.Debug("Aggregator", "Session init: No SSO-enabled servers require authentication")
		return
	}

	logging.Info("Aggregator", "Session init: Establishing proactive SSO connections for session %s to %d servers",
		logging.TruncateSessionID(sessionID), len(ssoServers))

	// Attempt to connect to each SSO-enabled server
	for _, info := range ssoServers {
		result, _, err := EstablishSessionConnectionWithTokenForwarding(
			ctx, a, sessionID, info, musterIssuer,
		)
		if err == nil && result != nil {
			logging.Info("Aggregator", "Session init: Connected session %s to SSO server %s via token forwarding",
				logging.TruncateSessionID(sessionID), info.Name)
		} else {
			// Log at Warn level for visibility - SSO failures should be investigated
			logging.Warn("Aggregator", "Session init: SSO connection to %s failed for session %s: %v",
				info.Name, logging.TruncateSessionID(sessionID), err)

			// Track the SSO failure so the UI can show "SSO failed"
			if a.sessionRegistry != nil {
				a.sessionRegistry.MarkSSOFailed(sessionID, info.Name)
			}
		}
	}
}
