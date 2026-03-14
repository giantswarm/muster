package aggregator

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/internal/server"
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
		// SSO-enabled servers are connected on demand via triggerOnDemandSSO.
		// Return sso_pending while the connection attempt is in flight, but
		// fall back to auth_required if the pending timeout has elapsed.
		// This prevents the client from being stuck in sso_pending forever
		// when an SSO attempt silently fails.
		if (ShouldUseTokenExchange(info) || ShouldUseTokenForwarding(info)) &&
			a.ssoTracker != nil &&
			!a.ssoTracker.HasSSOFailed(sub, serverName) &&
			a.ssoTracker.IsSSOPendingWithinTimeout(sub, serverName) {
			return pkgoauth.ServerStatusSSOPending
		}
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

// onDemandSSOTimeout caps how long the first synchronous SSO round can block.
const onDemandSSOTimeout = 15 * time.Second

// triggerOnDemandSSO extends the session TTL and connects to any SSO-enabled
// servers that are missing from the capability store.
//
// Behaviour depends on whether the session already has cached capabilities:
//   - Empty cache (first request or after inactivity): SSO connections run
//     synchronously so the response includes SSO server tools immediately.
//   - Populated cache (active session with some servers missing): SSO
//     connections run in the background for eventual consistency.
func (a *AggregatorServer) triggerOnDemandSSO(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}

	// Extend the session TTL on every authenticated request so that
	// capabilities remain available as long as the user is active.
	sessionExists := false
	if a.capabilityStore != nil {
		sessionExists, _ = a.capabilityStore.Touch(ctx, sessionID)
	}

	musterIssuer := a.getMusterIssuer()
	if musterIssuer == "" {
		return
	}

	sub := getUserSubjectFromContext(ctx)

	// Build a detached context for SSO goroutines. The incoming ctx is the
	// HTTP request context and will be cancelled when the handler returns.
	bgCtx := context.Background()
	bgCtx = api.WithSubject(bgCtx, sub)
	bgCtx = api.WithSessionID(bgCtx, sessionID)
	if idToken, ok := server.GetIDTokenFromContext(ctx); ok {
		bgCtx = server.ContextWithIDToken(bgCtx, idToken)
	}

	// Collect SSO servers that need connections.
	var pending []*ServerInfo
	servers := a.registry.GetAllServers()
	for _, info := range servers {
		if info.Status != StatusAuthRequired {
			continue
		}
		if !ShouldUseTokenExchange(info) && !ShouldUseTokenForwarding(info) {
			continue
		}
		if a.capabilityStore != nil {
			exists, _ := a.capabilityStore.Exists(bgCtx, sessionID, info.Name)
			if exists {
				continue
			}
		}
		if a.ssoTracker != nil {
			if a.ssoTracker.HasSSOFailed(sub, info.Name) {
				continue
			}
			if a.ssoTracker.IsSSOSuppressed(sub, info.Name) {
				continue
			}
		}
		pending = append(pending, info)
	}

	if len(pending) == 0 {
		return
	}

	// Mark all pending servers so auth://status can report sso_pending.
	if a.ssoTracker != nil {
		for _, info := range pending {
			a.ssoTracker.MarkSSOPending(sub, info.Name)
		}
	}

	if !sessionExists {
		// Cache is empty -- block until all SSO connections complete (or timeout).
		a.connectSSOServersSynchronously(bgCtx, pending, musterIssuer)
		return
	}

	// Cache exists but some servers are missing -- connect in background.
	for _, info := range pending {
		serverInfo := info
		go a.establishSSOConnection(bgCtx, serverInfo, musterIssuer)
	}
}

// connectSSOServersSynchronously establishes SSO connections to all given
// servers in parallel and blocks until they all complete or the timeout fires.
func (a *AggregatorServer) connectSSOServersSynchronously(
	ctx context.Context,
	servers []*ServerInfo,
	musterIssuer string,
) {
	logging.Info("Aggregator", "SSO: Synchronous init for %d servers", len(servers))

	ctx, cancel := context.WithTimeout(ctx, onDemandSSOTimeout)
	defer cancel()

	var wg sync.WaitGroup
	for _, info := range servers {
		wg.Add(1)
		go func(si *ServerInfo) {
			defer wg.Done()
			a.establishSSOConnection(ctx, si, musterIssuer)
		}(info)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logging.Debug("Aggregator", "SSO: Synchronous init completed for %d servers", len(servers))
	case <-ctx.Done():
		logging.Warn("Aggregator", "SSO: Synchronous init timed out after %v", onDemandSSOTimeout)
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

	// Clear pending state regardless of outcome.
	if a.ssoTracker != nil {
		a.ssoTracker.ClearSSOPending(sub, serverInfo.Name)
	}

	if err == nil && result != nil {
		logging.Info("Aggregator", "SSO: Connected user %s to SSO server %s via %s",
			sub, serverInfo.Name, ssoMethod)
	} else {
		logging.Warn("Aggregator", "SSO: Connection to %s failed for user %s: %v",
			serverInfo.Name, sub, err)

		if a.ssoTracker != nil {
			a.ssoTracker.MarkSSOFailed(sub, serverInfo.Name)
		}
	}
}
