package aggregator

import (
	"context"
	"encoding/json"
	"sync"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"

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
// IMPORTANT: Per issue #292, this handler uses the CapabilityCache as the
// source of truth for per-user connection/auth state, NOT the MCPServer CRD status.
//
// Status determination (per issue #292):
//   - Infrastructure availability: From aggregator's internal registry (reachable, unreachable)
//   - Per-user auth/connection: From CapabilityCache (connected, auth_required, etc.)
//
// The MCPServer CRD State only reflects infrastructure state (Running/Connected/Failed/etc.),
// while this resource shows the per-user state.
//
// Note: Proactive SSO connections for servers with forwardToken: true are established
// during session initialization (see handleSessionInit), not during auth status reads.
// This ensures auth://status is a pure read operation without side effects.
func (a *AggregatorServer) handleAuthStatusResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	// Get user subject from context for user-specific auth status
	sub := getUserSubjectFromContext(ctx)

	servers := a.registry.GetAllServers()
	response := pkgoauth.AuthStatusResponse{Servers: make([]pkgoauth.ServerAuthStatus, 0, len(servers))}

	for name, info := range servers {
		// Check if this server uses SSO via token exchange (RFC 8693) or token forwarding
		usesTokenExchange := ShouldUseTokenExchange(info)
		usesTokenForwarding := ShouldUseTokenForwarding(info)

		// Check if SSO was attempted but failed for this user/server
		ssoAttemptFailed := false
		if a.ssoTracker != nil && (usesTokenExchange || usesTokenForwarding) {
			ssoAttemptFailed = a.ssoTracker.HasSSOFailed(sub, name)
		}

		status := pkgoauth.ServerAuthStatus{
			Name:                   name,
			Status:                 string(info.Status),
			TokenForwardingEnabled: usesTokenForwarding,
			TokenExchangeEnabled:   usesTokenExchange,
			SSOAttemptFailed:       ssoAttemptFailed,
		}

		// Determine status from CapabilityCache (per-user state)
		// This is the source of truth for connection/auth state per issue #292
		status.Status = a.determineSessionAuthStatus(sub, name, info)

		// If auth is required for this session, include auth tool info
		if status.Status == pkgoauth.ServerStatusAuthRequired && info.AuthInfo != nil {
			status.Issuer = info.AuthInfo.Issuer
			status.Scope = info.AuthInfo.Scope
			// Only expose auth tool for servers that support manual browser-based OAuth.
			// SSO-enabled servers (token forwarding/exchange) are authenticated by the
			// admin, not the user -- manual login cannot fix SSO failures.
			if !status.TokenForwardingEnabled && !status.TokenExchangeEnabled {
				// Per ADR-008: Use core_auth_login with server parameter instead of synthetic tools
				status.AuthTool = "core_auth_login"
			}
		}

		response.Servers = append(response.Servers, status)
	}

	data, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}

	logging.Debug("Aggregator", "Returning auth status for %d servers (sub=%s)",
		len(response.Servers), sub)

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      AuthStatusResourceURI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

// determineSessionAuthStatus determines the auth/connection status for a specific
// user and server combination.
//
// Per issue #292, this function uses the CapabilityCache as the primary source
// of truth for per-user state:
//   - If user has cached capabilities (tools) -> "connected"
//   - If server infrastructure is unreachable -> "unreachable" (no auth possible)
//   - If server requires auth and user hasn't authenticated -> "auth_required"
//   - If server doesn't require auth and is reachable -> "connected"
//
// This cleanly separates:
//   - Infrastructure state (CRD State: Running/Connected/Failed/etc.)
//   - Per-user state (this function: connected/auth_required/etc.)
func (a *AggregatorServer) determineSessionAuthStatus(sub, serverName string, info *ServerInfo) string {
	// Handle unreachable servers first - no auth possible
	if info.Status == StatusUnreachable {
		return pkgoauth.ServerStatusUnreachable
	}

	// Check CapabilityCache for per-user connection state
	if a.capabilityCache != nil {
		if _, ok := a.capabilityCache.Get(sub, serverName); ok {
			logging.Debug("Aggregator", "User %s has cached capabilities for %s", sub, serverName)
			return pkgoauth.ServerStatusConnected
		}
	}

	// No cached capabilities - check infrastructure state
	if info.Status == StatusAuthRequired && info.AuthInfo != nil {
		// If the server is SSO-enabled and SSO init is in progress for this user,
		// return sso_pending instead of auth_required. This prevents clients from
		// calling core_auth_login while SSO is still completing.
		if (ShouldUseTokenExchange(info) || ShouldUseTokenForwarding(info)) &&
			a.ssoTracker != nil &&
			a.ssoTracker.IsSSOInitInProgress(sub) &&
			!a.ssoTracker.HasSSOFailed(sub, serverName) {
			return pkgoauth.ServerStatusSSOPending
		}
		// Server requires auth globally but this user hasn't authenticated
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
// finding any token for the user that has an ID token.
//
// This is useful when we need to determine the issuer for a specific user
// but the configuration might not be explicitly set. The fallback searches
// the OAuth proxy token store for any token with an ID token.
//
// Args:
//   - sub: The user subject to search for fallback tokens (only used if config lookup fails)
//
// Returns the issuer URL, or empty string if none can be determined.
func (a *AggregatorServer) getMusterIssuerWithFallback(sub string) string {
	// First, try to get from configuration
	if issuer := a.getMusterIssuer(); issuer != "" {
		return issuer
	}

	// Fallback: Look for any token for the user that has an ID token.
	// This is a best-effort approach when the configured issuer is not available.
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return ""
	}

	fullToken := oauthHandler.FindTokenWithIDToken(sub)
	if fullToken != nil && fullToken.Issuer != "" {
		logging.Debug("Aggregator", "Using fallback issuer from user token: %s", fullToken.Issuer)
		return fullToken.Issuer
	}

	return ""
}

// handleSessionInitPrepare is called synchronously before the async SSO goroutine.
// It sets SSOInitInProgress early to close the race window where an auth://status
// read between goroutine launch and the actual StartSSOInit call inside
// handleSessionInit could return auth_required instead of sso_pending.
//
// This is a best-effort operation: it sets the flag even if there turn out to be
// no SSO servers. handleSessionInit's defer EndSSOInit clears it immediately in
// that case, so the window of a false positive is negligible.
func (a *AggregatorServer) handleSessionInitPrepare(sub string) {
	if a.ssoTracker != nil {
		a.ssoTracker.StartSSOInit(sub)
	}
}

// handleSessionInit is called on the first authenticated MCP request for a user.
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
func (a *AggregatorServer) handleSessionInit(ctx context.Context, sub string) {
	if a.ssoTracker != nil {
		defer a.ssoTracker.EndSSOInit(sub)
	}

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

	logging.Info("Aggregator", "Session init: Establishing proactive SSO connections for user %s to %d servers (parallel)",
		sub, len(ssoServers))

	// Attempt to connect to all SSO-enabled servers in parallel
	// This minimizes total time and prevents race conditions with session timeouts
	var wg sync.WaitGroup
	for _, info := range ssoServers {
		wg.Add(1)
		go func(serverInfo *ServerInfo) {
			defer wg.Done()
			a.establishSSOConnection(ctx, sub, serverInfo, musterIssuer)
		}(info)
	}
	wg.Wait()

	// Note: Individual SSO failures are logged within establishSSOConnection.
	// Context cancellation will cause all goroutines to fail gracefully.
	logging.Debug("Aggregator", "Session init: Completed SSO initialization for user %s", sub)
}

// establishSSOConnection attempts to establish an SSO connection to a single server.
// This is called from handleSessionInit for each SSO-enabled server.
//
// This method is safe against concurrent calls (e.g., proactive SSO goroutine racing
// with an explicit core_auth_login). It checks the CapabilityCache before attempting
// connection and skips if the user already has cached capabilities.
func (a *AggregatorServer) establishSSOConnection(
	ctx context.Context,
	sub string,
	serverInfo *ServerInfo,
	musterIssuer string,
) {
	// Guard against concurrent connection attempts. The proactive SSO goroutine
	// (from handleSessionInit) can race with explicit core_auth_login calls.
	if a.capabilityCache != nil {
		if _, ok := a.capabilityCache.Get(sub, serverInfo.Name); ok {
			logging.Debug("Aggregator", "Session init: User %s already has capabilities for %s, skipping SSO",
				sub, serverInfo.Name)
			return
		}
	}

	var result *ConnectionResult
	var err error
	var ssoMethod string

	// Token exchange takes precedence over token forwarding
	if ShouldUseTokenExchange(serverInfo) {
		result, err = EstablishConnectionWithTokenExchange(
			ctx, a, sub, serverInfo, musterIssuer,
		)
		ssoMethod = "token exchange (RFC 8693)"
	} else {
		result, err = EstablishConnectionWithTokenForwarding(
			ctx, a, sub, serverInfo, musterIssuer,
		)
		ssoMethod = "token forwarding"
	}

	if err == nil && result != nil {
		logging.Info("Aggregator", "Session init: Connected user %s to SSO server %s via %s",
			sub, serverInfo.Name, ssoMethod)
	} else {
		// Log at Warn level for visibility - SSO failures should be investigated
		logging.Warn("Aggregator", "Session init: SSO connection to %s failed for user %s: %v",
			serverInfo.Name, sub, err)

		// Track the SSO failure so the UI can show "SSO failed"
		if a.ssoTracker != nil {
			a.ssoTracker.MarkSSOFailed(sub, serverInfo.Name)
		}
	}
}
