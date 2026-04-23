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
// IMPORTANT: Per issue #292, this handler uses the SessionAuthStore as the
// source of truth for per-user auth state, NOT the MCPServer CRD status.
//
// Status determination (per issue #292):
//   - Infrastructure availability: From aggregator's internal registry (reachable, unreachable)
//   - Per-user auth/connection: From SessionAuthStore (connected, auth_required, etc.)
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
	hasSession := sub != "" && sessionID != ""
	if !hasSession {
		logging.Warn("Aggregator", "handleAuthStatusResource: missing session context (hasSub=%t, hasSessionID=%t) — returning infrastructure-level status only",
			sub != "", sessionID != "")
	}

	servers := a.registry.GetAllServers()
	response := pkgoauth.AuthStatusResponse{Servers: make([]pkgoauth.ServerAuthStatus, 0, len(servers))}

	for name, info := range servers {
		usesTokenExchange := ShouldUseTokenExchange(info)
		usesTokenForwarding := ShouldUseTokenForwarding(info)

		ssoAttemptFailed := false
		if hasSession && a.ssoTracker != nil && (usesTokenExchange || usesTokenForwarding) {
			ssoAttemptFailed = a.ssoTracker.HasSSOFailed(sub, name)
		}

		var serverStatus pkgoauth.SessionServerStatus
		if hasSession {
			serverStatus = a.determineSessionAuthStatus(sub, sessionID, name, info)
		} else if info.RequiresSessionAuth() {
			serverStatus = pkgoauth.SessionServerStatusAuthRequired
		} else if info.IsConnected() {
			serverStatus = pkgoauth.SessionServerStatusConnected
		} else {
			serverStatus = pkgoauth.SessionServerStatusDisconnected
		}

		status := pkgoauth.ServerAuthStatus{
			Name:                   name,
			Status:                 serverStatus,
			TokenForwardingEnabled: usesTokenForwarding,
			TokenExchangeEnabled:   usesTokenExchange,
			SSOAttemptFailed:       ssoAttemptFailed,
		}

		if info.AuthInfo != nil {
			switch status.Status {
			case pkgoauth.SessionServerStatusAuthRequired, pkgoauth.SessionServerStatusReauthRequired:
				status.Issuer = info.AuthInfo.Issuer
				status.Scope = info.AuthInfo.Scope
				if status.Status == pkgoauth.SessionServerStatusReauthRequired ||
					(!status.TokenForwardingEnabled && !status.TokenExchangeEnabled) {
					status.AuthTool = "core_auth_login"
				}
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
func (a *AggregatorServer) determineSessionAuthStatus(sub, sessionID, serverName string, info *ServerInfo) pkgoauth.SessionServerStatus {
	// Handle unreachable servers first - no auth possible
	if info.GetStatus() == api.StateUnreachable {
		return pkgoauth.SessionServerStatusUnreachable
	}

	if a.authStore != nil {
		authenticated, _ := a.authStore.IsAuthenticated(context.Background(), sessionID, serverName)
		if authenticated {
			logging.Debug("Aggregator", "Session %s is authenticated to %s", logging.TruncateIdentifier(sessionID), serverName)
			return pkgoauth.SessionServerStatusConnected
		}
	}

	// No cached capabilities - check infrastructure state
	if info.RequiresSessionAuth() && info.AuthInfo != nil {
		isSSO := ShouldUseTokenExchange(info) || ShouldUseTokenForwarding(info)

		if isSSO && a.ssoTracker != nil {
			if a.ssoTracker.HasSSOFailed(sub, serverName) {
				// SSO was attempted but failed. For SSO-enabled servers this
				// typically means the upstream refresh chain is broken (e.g.
				// Dex -> GitHub returned 401). Return reauth_required so the
				// agent can prompt re-authentication rather than the generic
				// auth_required which might imply initial setup.
				return pkgoauth.SessionServerStatusReauthRequired
			}
			if a.ssoTracker.IsSSOPendingWithinTimeout(sub, serverName) {
				return pkgoauth.SessionServerStatusSSOPending
			}
		}
		return pkgoauth.SessionServerStatusAuthRequired
	}

	// Server is connected at infrastructure level (global client exists)
	if info.IsConnected() {
		return pkgoauth.SessionServerStatusConnected
	}

	return pkgoauth.SessionServerStatusDisconnected
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

// storeIDTokenForSSO persists the muster-level ID token in the OAuth proxy
// token store so that background headerFunc closures can resolve it for SSO
// token forwarding. No-op when idToken or familyID is empty.
func (a *AggregatorServer) storeIDTokenForSSO(familyID, userID, idToken string) {
	if idToken == "" || familyID == "" {
		return
	}
	musterIssuer := a.getMusterIssuer()
	if musterIssuer == "" {
		return
	}
	if oh := api.GetOAuthHandler(); oh != nil && oh.IsEnabled() {
		oh.StoreToken(familyID, userID, musterIssuer, &api.OAuthToken{IDToken: idToken})
	}
}

// handleUpstreamRefreshFailure is called when the upstream token refresh chain
// is detected as broken (e.g. Dex -> GitHub returns 401, or the refreshed token
// has no ID token). It evicts all pooled SSO connections for the session to stop
// mcp-go's infinite 1-second retry loop, and clears the session's auth store
// entries so auth://status reports reauth_required instead of connected.
//
// This is safe to call multiple times for the same session; all operations are
// idempotent (evicting an empty pool is a no-op, revoking a revoked session is
// a no-op).
func (a *AggregatorServer) handleUpstreamRefreshFailure(sessionID, userID, reason string) {
	logging.Warn("Aggregator", "SSO: Upstream refresh failure detected for session %s (user %s): %s",
		logging.TruncateIdentifier(sessionID), logging.TruncateIdentifier(userID), reason)

	if a.connPool != nil {
		a.connPool.EvictSession(sessionID)
		logging.Info("Aggregator", "SSO: Evicted pooled connections for session %s due to refresh failure",
			logging.TruncateIdentifier(sessionID))
	}

	if a.authStore != nil {
		if err := a.authStore.RevokeSession(context.Background(), sessionID); err != nil {
			logging.Warn("Aggregator", "SSO: Failed to revoke auth session %s after refresh failure: %v",
				logging.TruncateIdentifier(sessionID), err)
		}
	}

	// Clear the stored ID token so headerFunc closures don't keep resolving
	// stale tokens from the OAuth proxy store.
	musterIssuer := a.getMusterIssuer()
	if musterIssuer != "" {
		if oh := api.GetOAuthHandler(); oh != nil && oh.IsEnabled() {
			oh.ClearTokenByIssuer(sessionID, musterIssuer)
		}
	}

	// Mark all SSO servers as failed for this user so initSSOForSession
	// doesn't immediately retry with expired credentials.
	if a.ssoTracker != nil && userID != "" {
		servers := a.registry.GetAllServers()
		for _, info := range servers {
			if ShouldUseTokenExchange(info) || ShouldUseTokenForwarding(info) {
				a.ssoTracker.MarkSSOFailed(userID, info.Name)
			}
		}
	}
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

// initSSOTimeout caps how long synchronous SSO connections may block the
// login flow. Individual servers that exceed this deadline are skipped.
const initSSOTimeout = 15 * time.Second

// initSSOForSession is called synchronously during token issuance
// (SessionCreationHandler) to establish SSO connections for a new session.
//
// Because this runs inside ExchangeAuthorizationCode, the SSO servers are
// fully connected before the client receives its access token.
// Connections to individual servers run in parallel with a shared timeout
// so that a single slow server cannot block the entire login flow.
func (a *AggregatorServer) initSSOForSession(ctx context.Context, userID, sessionID, idToken string) {
	musterIssuer := a.getMusterIssuer()

	logging.Info("Aggregator", "SSO: initSSOForSession called (userID=%s, sessionID=%s, idTokenLen=%d, musterIssuer=%s)",
		logging.TruncateIdentifier(userID), logging.TruncateIdentifier(sessionID), len(idToken), musterIssuer)

	if musterIssuer == "" {
		logging.Info("Aggregator", "SSO: initSSOForSession returning early: musterIssuer is empty")
		return
	}

	// Build a detached context with a timeout -- the token-exchange request
	// context may be cancelled before SSO work finishes.
	bgCtx, cancel := context.WithTimeout(context.Background(), initSSOTimeout)
	defer cancel()
	bgCtx = api.WithSubject(bgCtx, userID)
	bgCtx = api.WithSessionID(bgCtx, sessionID)
	if idToken != "" {
		bgCtx = server.ContextWithIDToken(bgCtx, idToken)
	}

	var pending []*ServerInfo
	servers := a.registry.GetAllServers()
	var skippedNotAuthRequired, skippedNotSSO, skippedPriorFailure int
	for _, info := range servers {
		if !info.RequiresSessionAuth() {
			skippedNotAuthRequired++
			continue
		}
		if !ShouldUseTokenExchange(info) && !ShouldUseTokenForwarding(info) {
			skippedNotSSO++
			continue
		}
		if a.ssoTracker != nil && a.ssoTracker.HasSSOFailed(userID, info.Name) {
			fc := a.ssoTracker.GetFailureCount(userID, info.Name)
			logging.Debug("Aggregator", "SSO: skipping %s for user %s (failureCount=%d, backoff=%v)",
				info.Name, logging.TruncateIdentifier(userID), fc, ssoBackoffDuration(fc))
			skippedPriorFailure++
			continue
		}
		pending = append(pending, info)
	}

	logging.Info("Aggregator", "SSO: initSSOForSession filter results: total=%d, pending=%d, skippedNotAuthRequired=%d, skippedNotSSO=%d, skippedPriorFailure=%d",
		len(servers), len(pending), skippedNotAuthRequired, skippedNotSSO, skippedPriorFailure)

	if len(pending) == 0 {
		return
	}

	logging.Info("Aggregator", "SSO: Connecting %d servers for session %s",
		len(pending), logging.TruncateIdentifier(sessionID))

	var wg sync.WaitGroup
	for _, info := range pending {
		wg.Add(1)
		go func(si *ServerInfo) {
			defer wg.Done()
			a.establishSSOConnection(bgCtx, si, musterIssuer)
		}(info)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logging.Debug("Aggregator", "SSO: All %d servers connected for session %s",
			len(pending), logging.TruncateIdentifier(sessionID))
	case <-bgCtx.Done():
		logging.Warn("Aggregator", "SSO: Init timed out after %v for session %s",
			initSSOTimeout, logging.TruncateIdentifier(sessionID))
	}
}

// establishSSOConnection attempts to establish an SSO connection to a single server.
// This is called on demand when a cache miss is detected for an SSO-enabled server
// (token forwarding or token exchange). Manual-auth servers use core_auth_login instead.
//
// This method is safe against concurrent calls. It checks the AuthStore before
// attempting connection and skips if the session is already authenticated.
func (a *AggregatorServer) establishSSOConnection(
	ctx context.Context,
	serverInfo *ServerInfo,
	musterIssuer string,
) {
	sessionID := getSessionIDFromContext(ctx)
	sub := getUserSubjectFromContext(ctx)
	if sessionID == "" || sub == "" {
		logging.Warn("Aggregator", "SSO: skipping connection to %s — no session context (hasSessionID=%t, hasSub=%t)",
			serverInfo.Name, sessionID != "", sub != "")
		return
	}

	// Guard against concurrent SSO connection attempts for the same session+server.
	if a.authStore != nil {
		authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, serverInfo.Name)
		if authenticated {
			logging.Debug("Aggregator", "SSO: Session %s already authenticated to %s, skipping SSO",
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
		if result.Client != nil && a.connPool != nil {
			a.connPool.PutWithExpiry(sessionID, serverInfo.Name, result.Client, result.TokenExpiry)
			if result.ExchangedToken != "" {
				a.connPool.SetExchangedToken(sessionID, serverInfo.Name, result.ExchangedToken)
			}
		}
		logging.Info("Aggregator", "SSO: Connected user %s to SSO server %s via %s",
			sub, serverInfo.Name, ssoMethod)
	} else {
		if result != nil && result.Client != nil {
			result.Client.Close()
		}
		logging.Warn("Aggregator", "SSO: Connection to %s failed for user %s: %v",
			serverInfo.Name, sub, err)

		if a.ssoTracker != nil {
			a.ssoTracker.MarkSSOFailed(sub, serverInfo.Name)
		}
	}
}
