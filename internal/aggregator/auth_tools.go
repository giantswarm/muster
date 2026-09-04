// Package aggregator provides the MCP aggregator server implementation.
//
// # SSO Authentication Mechanisms
//
// Muster supports two Single Sign-On (SSO) mechanisms for authenticating
// to downstream MCP servers:
//
// ## SSO Token Forwarding (explicit opt-in)
//
// When muster itself is protected by OAuth (via oauth_server configuration), muster
// can forward its own ID token to downstream MCP servers. The downstream server must
// be configured to trust muster's OAuth client ID in its TrustedAudiences.
//
// Flow:
//  1. User authenticates TO muster via OAuth (Google, Dex, etc.)
//  2. Muster receives and stores the user's ID token
//  3. User accesses server with forwardToken: true
//  4. Muster injects ID token as Authorization: Bearer header
//  5. Downstream server validates token, trusts muster's client ID
//
// Configuration: Requires `auth.forwardToken: true` in MCPServer spec.
//
// ## SSO Token Exchange (RFC 8693)
//
// When clusters have separate Identity Providers, muster can exchange its local
// token for one valid on the remote cluster's IdP (e.g., Dex). This enables
// cross-cluster SSO without requiring shared trust.
//
// Flow:
//  1. User authenticates TO muster via OAuth
//  2. User accesses server with tokenExchange configuration
//  3. Muster exchanges its token at the remote IdP's token endpoint
//  4. Remote IdP issues a new token valid for the remote cluster
//  5. Muster uses the exchanged token for downstream requests
//
// Configuration: Requires `auth.tokenExchange` configuration in MCPServer spec.
package aggregator

import (
	"context"
	"fmt"
	"strings"

	"github.com/giantswarm/muster/internal/api"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/pkg/logging"
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

// ExecuteTool executes an authentication tool by name.
func (p *AuthToolProvider) ExecuteTool(ctx context.Context, toolName string, args map[string]any) (*api.CallToolResult, error) {
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
//
// Security features:
//   - Rate limiting: Prevents OAuth flow abuse by limiting attempts per user
//   - Metrics: Tracks login attempts, successes, and failures for monitoring
func (p *AuthToolProvider) handleAuthLogin(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return &api.CallToolResult{
			Content: []any{"Error: 'server' argument is required and must be a string"},
			IsError: true,
		}, nil
	}

	resetClientRegistration, _ := args["reset_client_registration"].(bool)

	sessionID, sub, errResult := requireSessionContextResult(ctx)
	if errResult != nil {
		return errResult, nil
	}

	if p.aggregator.authRateLimiter != nil && !p.aggregator.authRateLimiter.Allow(sub, serverName) {
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordRateLimitBlock(serverName, sub)
		}
		remaining := 0
		if p.aggregator.authRateLimiter != nil {
			remaining = p.aggregator.authRateLimiter.RemainingAttempts(sub)
		}
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf(
				"Rate limit exceeded. Too many authentication attempts.\n\n"+
					"Please wait a moment before trying again.\n"+
					"Remaining attempts: %d",
				remaining,
			)},
			IsError: true,
		}, nil
	}

	// Record the login attempt in metrics
	if p.aggregator.authMetrics != nil {
		p.aggregator.authMetrics.RecordLoginAttempt(serverName, sub)
	}

	logging.Info("AuthTools", "Handling auth login for server: %s", serverName)

	// Get server info from registry
	serverInfo, exists := p.aggregator.registry.GetServerInfo(serverName)
	if !exists {
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf("Server '%s' not found. Use list_tools to see available servers.", serverName)},
			IsError: true,
		}, nil
	}

	if !serverInfo.RequiresSessionAuth() {
		// Server is already connected or doesn't require auth
		if serverInfo.IsConnected() {
			return &api.CallToolResult{
				Content: []any{fmt.Sprintf("Server '%s' is already authenticated and connected.", serverName)},
				IsError: false,
			}, nil
		}
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf("Server '%s' does not require authentication.", serverName)},
			IsError: false,
		}, nil
	}

	if p.aggregator.authStore != nil {
		authenticated, _ := p.aggregator.authStore.IsAuthenticated(ctx, sessionID, serverName)
		if authenticated {
			logging.Debug("AuthTools", "Session %s already authenticated to server %s", logging.TruncateIdentifier(sessionID), serverName)
			return &api.CallToolResult{
				Content: []any{fmt.Sprintf("Server '%s' is already authenticated.", serverName)},
				IsError: false,
			}, nil
		}
	}

	// Check if OAuth handler is available
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordLoginFailure(serverName, sub, "oauth_not_configured")
		}
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf(
				"OAuth is not configured. Server '%s' requires authentication but OAuth proxy is not enabled. "+
					"Enable OAuth proxy in the configuration to authenticate to remote MCP servers.",
				serverName,
			)},
			IsError: true,
		}, nil
	}

	// SSO servers (token exchange or token forwarding) are connected automatically
	// during session creation via initSSOForSession and do not support manual login.
	if ShouldUseTokenExchange(serverInfo) || ShouldUseTokenForwarding(serverInfo) {
		logging.Debug("AuthTools", "Rejecting manual auth_login for SSO server %s (session %s)",
			serverName, logging.TruncateIdentifier(sessionID))
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf(
				"Server '%s' uses SSO and is connected automatically.\n\n"+
					"Manual login via core_auth_login is not supported for SSO servers.\n"+
					"SSO connections are established when your session starts. "+
					"If the server is not connected, check your SSO configuration or re-authenticate to muster.",
				serverName,
			)},
			IsError: true,
		}, nil
	}

	// The auth info for this server: the 401-time fields plus whatever the
	// flow still lacks from the server's resource metadata or its pin
	// (spec.auth.authorizationServer -- the override branch in
	// discoverProtectedResourceMetadata bypasses PRM probing and uses the
	// operator-pinned issuer). The result is recorded on the registry entry,
	// so the tool calls of every session find the issuer as well.
	authInfo, err := p.aggregator.resolveServerAuthInfo(ctx, serverInfo)
	if err != nil {
		logging.Warn("AuthTools", "Cannot apply the authorization server pin of %s: %v", serverName, err)
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordLoginFailure(serverName, sub, "authorization_server_pin_failed")
		}
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf("Cannot authenticate to '%s': %v", serverName, err)},
			IsError: true,
		}, nil
	}

	// If still empty, we can't proceed
	if authInfo.Issuer == "" {
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordLoginFailure(serverName, sub, "issuer_discovery_failed")
		}
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf(
				"Cannot authenticate to '%s': RFC 9728 protected resource metadata not found. "+
					"On the MCPServer set spec.auth.type=oauth and spec.auth.authorizationServer.issuer "+
					"to pin the OAuth issuer URL (e.g. https://cf.mcp.atlassian.com for Atlassian's hosted MCP). "+
					"See docs/how-to/connecting-non-rfc9728-mcp-servers.md.",
				serverName,
			)},
			IsError: true,
		}, nil
	}

	// Check if we already have a valid token for this server/issuer (SSO).
	// This enables single sign-on: if the user authenticated to another server
	// with the same OAuth issuer, we can reuse that token.
	// Tokens with only an ID token (no access token) are muster-level tokens
	// stored for SSO forwarding and cannot be used for bearer authentication.
	// For a subject-scoped issuer this also finds the grant another session
	// of the same person completed, so the login connects without a browser.
	token := api.FullTokenByIssuerForUser(oauthHandler, sessionID, sub, authInfo.Issuer)

	if token != nil && token.AccessToken != "" {
		logging.Info("AuthTools", "Found existing token for server %s via SSO (issuer=%s), attempting to connect",
			serverName, authInfo.Issuer)

		// Try to establish connection using the existing token
		connectResult, connectErr := p.tryConnectWithToken(ctx, serverName, serverInfo.URL, authInfo.Issuer, authInfo.Scope, token.AccessToken)
		if connectErr == nil {
			// Record success and reset rate limiter for this user
			if p.aggregator.authMetrics != nil {
				p.aggregator.authMetrics.RecordLoginSuccess(serverName, sub)
			}
			if p.aggregator.authRateLimiter != nil {
				p.aggregator.authRateLimiter.Reset(sub)
			}
			return connectResult, nil
		}

		// Check if the error is a 401 - token is expired/invalid
		if is401Error(connectErr) {
			logging.Info("AuthTools", "Token for server %s is expired/invalid, clearing and requesting fresh auth", serverName)
			api.ClearTokenByIssuerForUser(oauthHandler, sessionID, sub, authInfo.Issuer)
		} else {
			// Some other error - report it
			logging.Error("AuthTools", connectErr, "Failed to connect to server %s with existing token", serverName)
			if p.aggregator.authMetrics != nil {
				p.aggregator.authMetrics.RecordLoginFailure(serverName, sub, "connection_failed")
			}
			return &api.CallToolResult{
				Content: []any{fmt.Sprintf(
					"Failed to connect to '%s': %v\n\nPlease try again or check the server status.",
					serverName, connectErr,
				)},
				IsError: true,
			}, nil
		}
	}

	// No token or token was cleared - need to create an auth challenge
	resource, err := resourceIndicator(authInfo.Resource, serverInfo.URL)
	if err != nil {
		logging.Error("AuthTools", err, "Cannot build an auth challenge for server %s", serverName)
		return nil, err
	}

	challenge, err := oauthHandler.CreateAuthChallenge(ctx, api.AuthChallengeParams{
		SessionID:               sessionID,
		UserID:                  sub,
		ServerName:              serverName,
		Issuer:                  authInfo.Issuer,
		Resource:                resource,
		Scope:                   authInfo.Scope,
		ResetClientRegistration: resetClientRegistration,
	})
	if err != nil {
		logging.Error("AuthTools", err, "Failed to create auth challenge for server %s", serverName)
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordLoginFailure(serverName, sub, "challenge_creation_failed")
		}
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf("Failed to create authentication challenge: %v", err)},
			IsError: true,
		}, nil
	}

	// Return the auth challenge as a tool result with the sign-in link
	return authChallengeResult(serverName, challenge), nil
}

// needsResourceMetadata reports whether an RFC 9728 probe can still add
// something to the flow. The 401 path supplies issuer and scope from the
// authorization server metadata but never the resource identifier, so the
// resource belongs in this condition: without it muster falls back to the
// configured URL even for a backend that declares a different canonical URI,
// and binds the token to an identifier the backend does not answer to.
// The 401 path never fills the resource, so a backend discovered that way is
// probed again on every login. The probe is one request against a backend the
// caller is already waiting on interactively, which is why it is not cached.
func needsResourceMetadata(authInfo *AuthInfo, serverURL string) bool {
	if serverURL == "" {
		return false
	}
	return authInfo.Issuer == "" || authInfo.Scope == "" || authInfo.Resource == ""
}

// resourceIndicator returns the RFC 8707 `resource` value for a backend, or an
// empty string when the backend needs none. An unusable configured URL is an
// error: a value muster cannot form is worse than a wrong one, because it would
// bind the token to an identifier no backend recognizes.
func resourceIndicator(declaredResource, serverURL string) (string, error) {
	resolved, err := pkgoauth.ResolveResourceIndicator(declaredResource, serverURL)
	if resolved.DeclaredErr != nil {
		logging.Warn("AuthTools", "Backend declares an unusable RFC 8707 resource %q, deriving one from the URL instead: %v", declaredResource, resolved.DeclaredErr)
	}
	return resolved.Value, err
}

// authChallengeResult builds the tool result for a pending auth challenge.
// The sign-in URL is carried both in the prose and as structuredContent.authUrl
// so clients can read it without parsing the text. structuredContent also
// carries clientIdMethod ("cimd", "dcr", "cimd-fallback", or "dcr-failed") so front-ends
// can tell users how muster identifies itself to the authorization server —
// and warn up front when the AS advertises neither mechanism.
func authChallengeResult(serverName string, challenge *api.AuthChallenge) *api.CallToolResult {
	structured := map[string]any{
		"authUrl": challenge.AuthURL,
	}
	if challenge.ClientIDMethod != "" {
		structured["clientIdMethod"] = challenge.ClientIDMethod
	}
	return &api.CallToolResult{
		Content: []any{fmt.Sprintf(
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
		IsError:           false,
		StructuredContent: structured,
	}
}

// handleAuthLogout clears authentication for a specific MCP server.
//
// Security features:
//   - Metrics: Tracks logout attempts and successes for monitoring
func (p *AuthToolProvider) handleAuthLogout(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return &api.CallToolResult{
			Content: []any{"Error: 'server' argument is required and must be a string"},
			IsError: true,
		}, nil
	}

	sessionID, sub, errResult := requireSessionContextResult(ctx)
	if errResult != nil {
		return errResult, nil
	}

	if p.aggregator.authMetrics != nil {
		p.aggregator.authMetrics.RecordLogoutAttempt(serverName, sub)
	}

	logging.Info("AuthTools", "Handling auth logout for server: %s (user=%s)", serverName, sub)

	serverInfo, exists := p.aggregator.registry.GetServerInfo(serverName)
	if !exists {
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf("Server '%s' not found.", serverName)},
			IsError: true,
		}, nil
	}

	// SSO servers are connected/disconnected automatically -- manual logout is not supported.
	if ShouldUseTokenExchange(serverInfo) || ShouldUseTokenForwarding(serverInfo) {
		logging.Debug("AuthTools", "Rejecting manual auth_logout for SSO server %s (session %s)",
			serverName, logging.TruncateIdentifier(sessionID))
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf(
				"Server '%s' uses SSO and is managed automatically.\n\n"+
					"Manual logout via core_auth_logout is not supported for SSO servers.\n"+
					"To disconnect, re-authenticate to muster or contact your administrator.",
				serverName,
			)},
			IsError: true,
		}, nil
	}

	// What the logout revokes depends on whom the token belongs to.
	//
	// A subject-scoped issuer (spec.auth.authorizationServer.grantScope:
	// subject, GitHub) issues the person's own grant: one consent serves every
	// server that verifies its tokens and every session of the person. Signing
	// out of any of those servers is the person revoking that consent, so the
	// grant goes for all of them -- otherwise nobody could revoke it from
	// muster while a second server shares the issuer, and the next login would
	// silently reconnect from the grant that was meant to be gone.
	//
	// A session-scoped issuer's token is cleared only when no other server
	// shares it and it is not muster's own upstream issuer: there the token
	// carries this session's authority to several servers (or muster's own SSO
	// forwarding), and clearing it for one server would break the others.
	//
	// The registry entry learns the issuer from the 401 probe or from a
	// login's discovery, so right after a restart it can still be empty for a
	// server the person is signed in to (the tokens outlive the process in
	// Valkey). Resolve it the way login does -- the operator's pin, then the
	// server's resource metadata -- instead of skipping every token step.
	var (
		revokedGrant bool
		siblings     []string
	)
	if issuer := p.resolveServerIssuer(ctx, serverInfo); issuer != "" {
		oauthHandler := api.GetOAuthHandler()
		oauthEnabled := oauthHandler != nil && oauthHandler.IsEnabled()
		musterIssuer := p.getMusterIssuer(sessionID)

		switch {
		case oauthEnabled && (musterIssuer == "" || !sameIssuer(musterIssuer, issuer)) && api.IssuerSubjectScoped(oauthHandler, issuer):
			api.ClearTokenByIssuerForUser(oauthHandler, sessionID, sub, issuer)
			siblings = p.aggregator.disconnectIssuerForSubject(ctx, sessionID, sub, serverName, issuer)
			revokedGrant = true
		case p.isIssuerExclusiveToServer(sessionID, serverName, issuer):
			if oauthEnabled {
				api.ClearTokenByIssuerForUser(oauthHandler, sessionID, sub, issuer)
			}
		default:
			logging.Debug("AuthTools", "Skipping issuer token clear for server %s: issuer %s is shared with other servers or muster", serverName, issuer)
		}
	}

	// Remove auth state, capabilities and the pooled connection for this
	// session+server. For a revoked grant this happened above for every
	// server of the issuer; repeating it for the named server is harmless.
	p.aggregator.disconnectSessionServer(ctx, sessionID, serverName)

	// Clear SSO failure state so re-authentication can trigger fresh SSO
	if p.aggregator.ssoTracker != nil {
		p.aggregator.ssoTracker.ClearSSOFailed(sub, serverName)
	}

	// Record logout success
	if p.aggregator.authMetrics != nil {
		p.aggregator.authMetrics.RecordLogoutSuccess(serverName, sub)
	}

	return logoutResult(serverName, revokedGrant, siblings), nil
}

// logoutResult words the outcome of core_auth_logout. When the person's grant
// was revoked the result says so and names the other servers that lost their
// connection with it, so a caller that sees a sibling server go "not
// authenticated" learns why from the same message.
func logoutResult(serverName string, revokedGrant bool, siblings []string) *api.CallToolResult {
	var b strings.Builder
	fmt.Fprintf(&b, "Successfully logged out from '%s'.\n\n", serverName)
	if revokedGrant {
		b.WriteString("The grant behind this server belonged to you rather than to this session, " +
			"so it was revoked for all your sessions")
		if len(siblings) > 0 {
			fmt.Fprintf(&b, "; the servers sharing it were disconnected as well: %s", strings.Join(siblings, ", "))
		}
		b.WriteString(".\n\n")
	}
	fmt.Fprintf(&b, "The server's tools are now hidden. Use core_auth_login with server='%s' to re-authenticate.", serverName)
	return &api.CallToolResult{
		Content: []any{b.String()},
		IsError: false,
	}
}

// tryConnectWithToken attempts to establish a connection to an MCP server using an OAuth token.
// The ctx must contain sessionID and sub (set by OAuth middleware).
func (p *AuthToolProvider) tryConnectWithToken(ctx context.Context, serverName, serverURL, issuer, scope, accessToken string) (*api.CallToolResult, error) {
	result, err := establishConnection(ctx, p.aggregator, serverName, serverURL, issuer, scope, accessToken)
	if err != nil {
		return nil, err
	}

	if result.Client != nil && p.aggregator.connPool != nil {
		sessionID := getSessionIDFromContext(ctx)
		if sessionID != "" {
			p.aggregator.connPool.Put(sessionID, serverName, result.Client)
		} else {
			logging.Warn("AuthTools", "Cannot pool client for server %s: no session ID in context", serverName)
		}
	}

	return result.FormatAsAPIResult(), nil
}

// getMusterIssuer determines the OAuth issuer that muster used to authenticate the user.
// This is needed for token forwarding - we need to get the ID token from muster's auth session.
//
// This method first checks if the OAuth handler is enabled (required for token forwarding),
// then delegates to the aggregator's getMusterIssuerWithFallback for the actual issuer lookup.
//
// Returns empty string if:
//   - No OAuth handler is registered
//   - The OAuth handler is not enabled
//   - No issuer could be determined from config or tokens
func (p *AuthToolProvider) getMusterIssuer(sessionID string) string {
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return ""
	}

	return p.aggregator.getMusterIssuerWithFallback(sessionID)
}

// isIssuerExclusiveToServer returns true if the given issuer is used ONLY by
// the specified server and is NOT muster's upstream issuer. When an issuer is
// shared, clearing it on logout of one server would break other servers (or
// muster's own token forwarding) that depend on the same token.
func (p *AuthToolProvider) isIssuerExclusiveToServer(sessionID, serverName, issuer string) bool {
	if musterIssuer := p.getMusterIssuer(sessionID); musterIssuer != "" && musterIssuer == issuer {
		return false
	}

	// Check if any other registered server uses the same issuer
	for name, info := range p.aggregator.registry.GetAllServers() {
		if name == serverName {
			continue
		}
		if other := knownServerIssuer(info); other != "" && sameIssuer(other, issuer) {
			return false
		}
	}

	return true
}

// knownServerIssuer returns the OAuth issuer the registry knows for a server
// without a network round trip: what the 401 probe or a login recorded in
// AuthInfo, else the operator's pin (spec.auth.authorizationServer.issuer).
// Empty when neither says.
func knownServerIssuer(info *ServerInfo) string {
	if info == nil {
		return ""
	}
	if info.AuthInfo != nil && info.AuthInfo.Issuer != "" {
		return info.AuthInfo.Issuer
	}
	if info.AuthConfig != nil && info.AuthConfig.AuthorizationServer != nil && info.AuthConfig.AuthorizationServer.Issuer != "" {
		return strings.TrimSuffix(info.AuthConfig.AuthorizationServer.Issuer, "/")
	}
	return ""
}

// resolveServerIssuer is knownServerIssuer plus, when the registry knows
// nothing, the issuer named by the server's RFC 9728 resource metadata -- the
// same discovery a login performs, so a logout reaches the tokens a login
// stored. Empty when the server is unreachable or publishes no metadata.
func (p *AuthToolProvider) resolveServerIssuer(ctx context.Context, info *ServerInfo) string {
	if issuer := knownServerIssuer(info); issuer != "" {
		return issuer
	}
	if info == nil || info.URL == "" {
		return ""
	}
	metadata, err := discoverProtectedResourceMetadata(ctx, info.URL, nil)
	if err != nil {
		logging.Debug("AuthTools", "Cannot resolve the issuer of %s for logout: %v", info.Name, err)
		return ""
	}
	return metadata.Issuer
}

// is401Error checks if an error indicates a 401 Unauthorized response
// using mcp-go's typed error detection.
func is401Error(err error) bool {
	return pkgoauth.IsOAuthUnauthorizedError(err)
}
