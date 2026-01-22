package aggregator

import (
	"context"
	"encoding/json"
	"sync"

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
// IMPORTANT: Per issue #292, this handler uses the Session Registry as the
// source of truth for per-user connection/auth state, NOT the MCPServer CRD status.
//
// Status determination (per issue #292):
//   - Infrastructure availability: From aggregator's internal registry (reachable, unreachable)
//   - Per-user auth/connection: From Session Registry (connected, auth_required, etc.)
//
// The MCPServer CRD Phase only reflects infrastructure state (Ready/Pending/Failed),
// while this resource shows the per-user session state.
//
// Note: Proactive SSO connections for servers with forwardToken: true are established
// during session initialization (see handleSessionInit), not during auth status reads.
// This ensures auth://status is a pure read operation without side effects.
func (a *AggregatorServer) handleAuthStatusResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	// Get session ID from context for session-specific auth status
	sessionID := getSessionIDFromContext(ctx)

	servers := a.registry.GetAllServers()
	response := pkgoauth.AuthStatusResponse{Servers: make([]pkgoauth.ServerAuthStatus, 0, len(servers))}

	for name, info := range servers {
		// Check if this server uses SSO via token exchange (RFC 8693) or token forwarding
		usesTokenExchange := ShouldUseTokenExchange(info)
		usesTokenForwarding := ShouldUseTokenForwarding(info)

		// Check if SSO token reuse is enabled (default: true)
		tokenReuseEnabled := true
		if info.AuthConfig != nil && info.AuthConfig.SSO != nil {
			tokenReuseEnabled = *info.AuthConfig.SSO
		}

		// Check if SSO was attempted but failed for this session/server
		ssoAttemptFailed := false
		if a.sessionRegistry != nil && (usesTokenExchange || usesTokenForwarding) {
			ssoAttemptFailed = a.sessionRegistry.HasSSOFailed(sessionID, name)
		}

		status := pkgoauth.ServerAuthStatus{
			Name:                   name,
			Status:                 string(info.Status),
			TokenForwardingEnabled: usesTokenForwarding,
			TokenExchangeEnabled:   usesTokenExchange,
			TokenReuseEnabled:      tokenReuseEnabled,
			SSOAttemptFailed:       ssoAttemptFailed,
		}

		// Determine status from Session Registry (per-user session state)
		// This is the source of truth for connection/auth state per issue #292
		status.Status = a.determineSessionAuthStatus(sessionID, name, info)

		// If auth is required for this session, include auth tool info
		if status.Status == pkgoauth.ServerStatusAuthRequired && info.AuthInfo != nil {
			status.Issuer = info.AuthInfo.Issuer
			status.Scope = info.AuthInfo.Scope
			// Per ADR-008: Use core_auth_login with server parameter instead of synthetic tools
			status.AuthTool = "core_auth_login"
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

// determineSessionAuthStatus determines the auth/connection status for a specific
// session and server combination.
//
// Per issue #292, this function uses the Session Registry as the primary source
// of truth for per-user session state:
//   - If session has an authenticated connection -> "connected"
//   - If session has pending auth -> "auth_required"
//   - If server infrastructure is unreachable -> "unreachable" (no auth possible)
//   - If server requires auth and session hasn't authenticated -> "auth_required"
//   - If server doesn't require auth and is reachable -> "connected"
//
// This cleanly separates:
//   - Infrastructure state (CRD Phase: Ready/Pending/Failed)
//   - Session state (this function: connected/auth_required/etc.)
func (a *AggregatorServer) determineSessionAuthStatus(sessionID, serverName string, info *ServerInfo) string {
	// Handle unreachable servers first - no auth possible
	if info.Status == StatusUnreachable {
		return pkgoauth.ServerStatusUnreachable
	}

	// Check Session Registry for per-user connection state
	if a.sessionRegistry != nil {
		if conn, exists := a.sessionRegistry.GetConnection(sessionID, serverName); exists && conn != nil {
			switch conn.Status {
			case StatusSessionConnected:
				logging.Debug("Aggregator", "Session %s has authenticated connection to %s",
					logging.TruncateSessionID(sessionID), serverName)
				return pkgoauth.ServerStatusConnected

			case StatusSessionPendingAuth:
				return pkgoauth.ServerStatusAuthRequired

			case StatusSessionFailed:
				// Session had a failure - check if it was an auth issue
				if conn.AuthStatus == AuthStatusTokenExpired {
					return pkgoauth.ServerStatusAuthRequired
				}
				// Other failures might be infrastructure issues
				return pkgoauth.ServerStatusFailed
			}
		}
	}

	// No session connection exists - check infrastructure state
	if info.Status == StatusAuthRequired && info.AuthInfo != nil {
		// Server requires auth globally but this session hasn't authenticated
		return pkgoauth.ServerStatusAuthRequired
	}

	// Server is connected at infrastructure level (global client exists)
	if info.IsConnected() {
		return pkgoauth.ServerStatusConnected
	}

	// Default to disconnected
	return "disconnected"
}

// getMusterIssuer returns the OAuth issuer URL configured for muster's OAuth server.
// This is used for SSO token forwarding - the issuer identifies the ID token source.
// When users authenticate to muster via `muster auth login`, this issuer is used to
// retrieve their ID token for forwarding to SSO-enabled downstream servers.
//
// The issuer is determined from the OAuth provider configuration:
//   - For Dex provider: returns Dex.IssuerURL (the upstream IdP's issuer)
//   - For other providers: returns BaseURL (muster's own URL as issuer)
//
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

	// For Dex provider, the issuer is the Dex server's URL, not muster's base URL.
	// Tokens are stored with the Dex issuer URL, so we need to use that for lookups.
	if cfg.Provider == "dex" && cfg.Dex.IssuerURL != "" {
		return cfg.Dex.IssuerURL
	}

	// For other providers (or fallback), use muster's base URL as the issuer
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
// SSO connections are established in parallel to minimize total time and prevent race
// conditions with session timeouts. All servers are attempted concurrently using goroutines.
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
	// SSO can be via token exchange (RFC 8693) or token forwarding
	for _, info := range servers {
		if info.Status == StatusAuthRequired && (ShouldUseTokenExchange(info) || ShouldUseTokenForwarding(info)) {
			ssoServers = append(ssoServers, info)
		}
	}

	if len(ssoServers) == 0 {
		logging.Debug("Aggregator", "Session init: No SSO-enabled servers require authentication")
		return
	}

	logging.Info("Aggregator", "Session init: Establishing proactive SSO connections for session %s to %d servers (parallel)",
		logging.TruncateSessionID(sessionID), len(ssoServers))

	// Mark the session as having SSO initialization in progress
	// This prevents the session from being cleaned up while SSO connections are being established
	if a.sessionRegistry != nil {
		a.sessionRegistry.StartSSOInit(sessionID)
		defer a.sessionRegistry.EndSSOInit(sessionID)
	}

	// Attempt to connect to all SSO-enabled servers in parallel
	// This minimizes total time and prevents race conditions with session timeouts
	var wg sync.WaitGroup
	for _, info := range ssoServers {
		wg.Add(1)
		go func(serverInfo *ServerInfo) {
			defer wg.Done()
			a.establishSSOConnection(ctx, sessionID, serverInfo, musterIssuer)
		}(info)
	}
	wg.Wait()

	// Note: Individual SSO failures are logged within establishSSOConnection.
	// Context cancellation will cause all goroutines to fail gracefully.
	logging.Debug("Aggregator", "Session init: Completed SSO initialization for session %s",
		logging.TruncateSessionID(sessionID))
}

// establishSSOConnection attempts to establish an SSO connection to a single server.
// This is called from handleSessionInit for each SSO-enabled server.
func (a *AggregatorServer) establishSSOConnection(
	ctx context.Context,
	sessionID string,
	serverInfo *ServerInfo,
	musterIssuer string,
) {
	var result *SessionConnectionResult
	var err error
	var ssoMethod string

	// Token exchange takes precedence over token forwarding
	if ShouldUseTokenExchange(serverInfo) {
		result, _, err = EstablishSessionConnectionWithTokenExchange(
			ctx, a, sessionID, serverInfo, musterIssuer,
		)
		ssoMethod = "token exchange (RFC 8693)"
	} else {
		result, _, err = EstablishSessionConnectionWithTokenForwarding(
			ctx, a, sessionID, serverInfo, musterIssuer,
		)
		ssoMethod = "token forwarding"
	}

	if err == nil && result != nil {
		logging.Info("Aggregator", "Session init: Connected session %s to SSO server %s via %s",
			logging.TruncateSessionID(sessionID), serverInfo.Name, ssoMethod)
	} else {
		// Log at Warn level for visibility - SSO failures should be investigated
		logging.Warn("Aggregator", "Session init: SSO connection to %s failed for session %s: %v",
			serverInfo.Name, logging.TruncateSessionID(sessionID), err)

		// Track the SSO failure so the UI can show "SSO failed"
		if a.sessionRegistry != nil {
			a.sessionRegistry.MarkSSOFailed(sessionID, serverInfo.Name)
		}
	}
}
