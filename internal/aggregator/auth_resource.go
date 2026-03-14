package aggregator

import (
	"context"
	"encoding/json"

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
// Note: SSO connections for servers with forwardToken: true are established on demand
// when the user first accesses tools, not during auth status reads.
// This ensures auth://status is a pure read operation without side effects.
func (a *AggregatorServer) handleAuthStatusResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	sub := getUserSubjectFromContext(ctx)
	sessionID := getSessionIDFromContext(ctx)

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

		status.Status = a.determineSessionAuthStatus(sub, sessionID, name, info)

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
// Per issue #292, this function uses the CapabilityStore as the primary source
// of truth for per-user state:
//   - If user has cached capabilities (tools) -> "connected"
//   - If server infrastructure is unreachable -> "unreachable" (no auth possible)
//   - If server requires auth and user hasn't authenticated -> "auth_required"
//   - If server doesn't require auth and is reachable -> "connected"
//
// This cleanly separates:
//   - Infrastructure state (CRD State: Running/Connected/Failed/etc.)
//   - Per-user state (this function: connected/auth_required/etc.)
func (a *AggregatorServer) determineSessionAuthStatus(sub, sessionID, serverName string, info *ServerInfo) string {
	// Handle unreachable servers first - no auth possible
	if info.Status == StatusUnreachable {
		return pkgoauth.ServerStatusUnreachable
	}

	// Check CapabilityStore keyed by session ID for per-login-session state
	if a.capabilityStore != nil {
		exists, _ := a.capabilityStore.Exists(context.Background(), sessionID, serverName)
		if exists {
			logging.Debug("Aggregator", "Session %s has cached capabilities for %s", logging.TruncateIdentifier(sessionID), serverName)
			return pkgoauth.ServerStatusConnected
		}
	}

	// No cached capabilities - check infrastructure state
	if info.Status == StatusAuthRequired && info.AuthInfo != nil {
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
//   - sessionID: The session ID (token family) to search for fallback tokens
//
// Returns the issuer URL, or empty string if none can be determined.
func (a *AggregatorServer) getMusterIssuerWithFallback(sessionID string) string {
	if issuer := a.getMusterIssuer(); issuer != "" {
		return issuer
	}

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

// triggerOnDemandSSO checks for SSO-enabled servers that don't yet have cached
// capabilities for this session and triggers background SSO connections for them.
//
// This implements the on-demand population model: instead of proactively connecting
// to all SSO servers during session initialization, connections are established
// lazily when the user first lists tools (or resources/prompts).
//
// Connections are fired as background goroutines so list_tools returns immediately.
// The next list_tools call will find the cached capabilities from completed connections.
func (a *AggregatorServer) triggerOnDemandSSO(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}

	musterIssuer := a.getMusterIssuer()
	if musterIssuer == "" {
		return
	}

	servers := a.registry.GetAllServers()
	for _, info := range servers {
		if info.Status != StatusAuthRequired {
			continue
		}
		if !ShouldUseTokenExchange(info) && !ShouldUseTokenForwarding(info) {
			continue
		}

		// Skip if already cached
		if a.capabilityStore != nil {
			exists, _ := a.capabilityStore.Exists(ctx, sessionID, info.Name)
			if exists {
				continue
			}
		}

		// Skip if SSO already failed for this user/server
		sub := getUserSubjectFromContext(ctx)
		if a.ssoTracker != nil && a.ssoTracker.HasSSOFailed(sub, info.Name) {
			continue
		}

		// Fire background SSO connection
		serverInfo := info
		go a.establishSSOConnection(ctx, serverInfo, musterIssuer)
	}
}

// establishSSOConnection attempts to establish an SSO connection to a single server.
// This is called on demand when a cache miss is detected for an SSO-enabled server,
// or when the user explicitly calls core_auth_login.
//
// This method is safe against concurrent calls. It checks the CapabilityStore before
// attempting connection and skips if the user already has cached capabilities.
func (a *AggregatorServer) establishSSOConnection(
	ctx context.Context,
	serverInfo *ServerInfo,
	musterIssuer string,
) {
	sessionID := getSessionIDFromContext(ctx)
	sub := getUserSubjectFromContext(ctx)

	// Guard against concurrent connection attempts. On-demand SSO goroutines
	// can race with explicit core_auth_login calls.
	if a.capabilityStore != nil {
		exists, _ := a.capabilityStore.Exists(ctx, sessionID, serverInfo.Name)
		if exists {
			logging.Debug("Aggregator", "SSO: Session %s already has capabilities for %s, skipping SSO",
				logging.TruncateIdentifier(sessionID), serverInfo.Name)
			return
		}
	}

	var result *ConnectionResult
	var err error
	var ssoMethod string

	// Token exchange takes precedence over token forwarding
	if ShouldUseTokenExchange(serverInfo) {
		result, err = EstablishConnectionWithTokenExchange(
			ctx, a, serverInfo, musterIssuer,
		)
		ssoMethod = "token exchange (RFC 8693)"
	} else {
		result, err = EstablishConnectionWithTokenForwarding(
			ctx, a, serverInfo, musterIssuer,
		)
		ssoMethod = "token forwarding"
	}

	if err == nil && result != nil {
		logging.Info("Aggregator", "SSO: Connected user %s to SSO server %s via %s",
			sub, serverInfo.Name, ssoMethod)
	} else {
		// Log at Warn level for visibility - SSO failures should be investigated
		logging.Warn("Aggregator", "SSO: Connection to %s failed for user %s: %v",
			serverInfo.Name, sub, err)

		// Track the SSO failure so the UI can show "SSO failed"
		if a.ssoTracker != nil {
			a.ssoTracker.MarkSSOFailed(sub, serverInfo.Name)
		}
	}
}
