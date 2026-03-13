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
//
// Security features:
//   - Rate limiting: Prevents OAuth flow abuse by limiting attempts per user
//   - Metrics: Tracks login attempts, successes, and failures for monitoring
func (p *AuthToolProvider) handleAuthLogin(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return &api.CallToolResult{
			Content: []interface{}{"Error: 'server' argument is required and must be a string"},
			IsError: true,
		}, nil
	}

	// Get user subject for identity-based operations
	sub := getUserSubjectFromContext(ctx)
	sessionID := getSessionIDFromContext(ctx)

	// Check rate limit before processing (rate limiting is user-scoped)
	if p.aggregator.authRateLimiter != nil && !p.aggregator.authRateLimiter.Allow(sub, serverName) {
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordRateLimitBlock(serverName, sub)
		}
		remaining := 0
		if p.aggregator.authRateLimiter != nil {
			remaining = p.aggregator.authRateLimiter.RemainingAttempts(sub)
		}
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf(
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

	// Check if this session already has cached capabilities for this server.
	if p.aggregator.capabilityCache != nil {
		if _, ok := p.aggregator.capabilityCache.Get(sessionID, serverName); ok {
			logging.Debug("AuthTools", "Session %s already has capabilities for server %s", logging.TruncateIdentifier(sessionID), serverName)
			return &api.CallToolResult{
				Content: []interface{}{fmt.Sprintf("Server '%s' is already authenticated.", serverName)},
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
			Content: []interface{}{fmt.Sprintf(
				"OAuth is not configured. Server '%s' requires authentication but OAuth proxy is not enabled. "+
					"Enable OAuth proxy in the configuration to authenticate to remote MCP servers.",
				serverName,
			)},
			IsError: true,
		}, nil
	}

	// Check if RFC 8693 token exchange is enabled for this server (takes precedence over forwarding)
	if ShouldUseTokenExchange(serverInfo) {
		logging.Info("AuthTools", "Token exchange (RFC 8693) is enabled for server %s, attempting cross-cluster SSO", serverName)

		result, err := p.tryTokenExchange(ctx, sub, serverInfo)
		if err != nil {
			logging.Warn("AuthTools", "Token exchange failed for server %s: %v", serverName, err)
			if p.aggregator.authMetrics != nil {
				p.aggregator.authMetrics.RecordLoginFailure(serverName, sub, "token_exchange_failed")
			}
			return &api.CallToolResult{
				Content: []interface{}{fmt.Sprintf(
					"RFC 8693 token exchange failed for '%s'.\n\n"+
						"Error: %v\n\n"+
						"Please check that the remote Dex is configured with an OIDC connector for your cluster.",
					serverName, err,
				)},
				IsError: true,
			}, nil
		}

		// Token exchange succeeded
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordLoginSuccess(serverName, sub)
		}
		if p.aggregator.authRateLimiter != nil {
			p.aggregator.authRateLimiter.Reset(sub)
		}
		return result, nil
	} else if ShouldUseTokenForwarding(serverInfo) {
		// Check if token forwarding is enabled for this server
		logging.Info("AuthTools", "Token forwarding is enabled for server %s, attempting SSO", serverName)

		// Get the muster issuer from the OAuth server configuration
		// For token forwarding, we use the same issuer that muster authenticated the user with
		result, err := p.tryTokenForwarding(ctx, sub, serverInfo)
		if err != nil {
			logging.Warn("AuthTools", "Token forwarding failed for server %s: %v", serverName, err)
			if p.aggregator.authMetrics != nil {
				p.aggregator.authMetrics.RecordLoginFailure(serverName, sub, "token_forwarding_failed")
			}
			return &api.CallToolResult{
				Content: []interface{}{fmt.Sprintf(
					"SSO token forwarding failed for '%s'.\n\n"+
						"Error: %v\n\n"+
						"Please check that the downstream server is configured to trust muster's client ID in its TrustedAudiences.",
					serverName, err,
				)},
				IsError: true,
			}, nil
		}

		// Token forwarding succeeded
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordLoginSuccess(serverName, sub)
		}
		if p.aggregator.authRateLimiter != nil {
			p.aggregator.authRateLimiter.Reset(sub)
		}
		return result, nil
	}

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
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordLoginFailure(serverName, sub, "issuer_discovery_failed")
		}
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf(
				"Cannot authenticate to '%s': unable to discover OAuth authorization server. "+
					"The server's /.well-known/oauth-protected-resource endpoint may not be available.",
				serverName,
			)},
			IsError: true,
		}, nil
	}

	// Check if we already have a valid token for this server/issuer (SSO).
	// This enables single sign-on: if the user authenticated to another server
	// with the same OAuth issuer, we can reuse that token.
	token := oauthHandler.GetTokenByIssuer(sessionID, authInfo.Issuer)

	if token != nil {
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
			oauthHandler.ClearTokenByIssuer(sessionID, authInfo.Issuer)
		} else {
			// Some other error - report it
			logging.Error("AuthTools", connectErr, "Failed to connect to server %s with existing token", serverName)
			if p.aggregator.authMetrics != nil {
				p.aggregator.authMetrics.RecordLoginFailure(serverName, sub, "connection_failed")
			}
			return &api.CallToolResult{
				Content: []interface{}{fmt.Sprintf(
					"Failed to connect to '%s': %v\n\nPlease try again or check the server status.",
					serverName, connectErr,
				)},
				IsError: true,
			}, nil
		}
	}

	// No token or token was cleared - need to create an auth challenge
	challenge, err := oauthHandler.CreateAuthChallenge(ctx, sessionID, sub, serverName, authInfo.Issuer, authInfo.Scope)
	if err != nil {
		logging.Error("AuthTools", err, "Failed to create auth challenge for server %s", serverName)
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordLoginFailure(serverName, sub, "challenge_creation_failed")
		}
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

// handleAuthLogout clears authentication for a specific MCP server.
//
// Security features:
//   - Metrics: Tracks logout attempts and successes for monitoring
func (p *AuthToolProvider) handleAuthLogout(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return &api.CallToolResult{
			Content: []interface{}{"Error: 'server' argument is required and must be a string"},
			IsError: true,
		}, nil
	}

	sub := getUserSubjectFromContext(ctx)
	sessionID := getSessionIDFromContext(ctx)

	if p.aggregator.authMetrics != nil {
		p.aggregator.authMetrics.RecordLogoutAttempt(serverName, sub)
	}

	logging.Info("AuthTools", "Handling auth logout for server: %s (user=%s)", serverName, sub)

	serverInfo, exists := p.aggregator.registry.GetServerInfo(serverName)
	if !exists {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Server '%s' not found.", serverName)},
			IsError: true,
		}, nil
	}

	// Clear tokens for this server's issuer ONLY if no other server shares it
	// and it is not muster's upstream issuer. Clearing a shared issuer token
	// would break other servers (or muster itself) that rely on the same token.
	if serverInfo.AuthInfo != nil && serverInfo.AuthInfo.Issuer != "" {
		if p.isIssuerExclusiveToServer(sessionID, serverName, serverInfo.AuthInfo.Issuer) {
			oauthHandler := api.GetOAuthHandler()
			if oauthHandler != nil && oauthHandler.IsEnabled() {
				oauthHandler.ClearTokenByIssuer(sessionID, serverInfo.AuthInfo.Issuer)
			}
		} else {
			logging.Debug("AuthTools", "Skipping issuer token clear for server %s: issuer %s is shared with other servers or muster", serverName, serverInfo.AuthInfo.Issuer)
		}
	}

	// Invalidate CapabilityCache for this session+server after logout
	if p.aggregator.capabilityCache != nil {
		p.aggregator.capabilityCache.Invalidate(sessionID, serverName)
	}

	// Clear SSO failure state so re-authentication can trigger fresh SSO
	if p.aggregator.ssoTracker != nil {
		p.aggregator.ssoTracker.ClearSSOFailed(sub, serverName)
	}

	// NOTE: We intentionally do NOT clear the sessionInitTracker entry here.
	// After logout, the user must explicitly re-authenticate via core_auth_login.
	// If we cleared the tracker, the next MCP request would trigger proactive SSO
	// reconnect, which defeats the purpose of logging out (see #423, #440).
	// Proactive SSO will naturally re-trigger when the muster access token is
	// refreshed (new token hash detected by triggerSessionInitIfNeeded).

	// Record logout success
	if p.aggregator.authMetrics != nil {
		p.aggregator.authMetrics.RecordLogoutSuccess(serverName, sub)
	}

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
// The ctx must contain sessionID and sub (set by OAuth middleware).
func (p *AuthToolProvider) tryConnectWithToken(ctx context.Context, serverName, serverURL, issuer, scope, accessToken string) (*api.CallToolResult, error) {
	result, err := establishConnection(ctx, p.aggregator, serverName, serverURL, issuer, scope, accessToken)
	if err != nil {
		return nil, err
	}
	return result.FormatAsAPIResult(), nil
}

// tryTokenExchange attempts to establish a connection using RFC 8693 token exchange.
// This is used when an MCPServer has tokenExchange configured for cross-cluster SSO.
//
// Returns:
//   - *api.CallToolResult: The connection result if successful
//   - error: The error if token exchange failed
func (p *AuthToolProvider) tryTokenExchange(ctx context.Context, sub string, serverInfo *ServerInfo) (*api.CallToolResult, error) {
	sessionID := getSessionIDFromContext(ctx)
	musterIssuer := p.getMusterIssuer(sessionID)
	if musterIssuer == "" {
		logging.Warn("AuthTools", "Cannot determine muster issuer for token exchange, user %s", sub)
		return nil, fmt.Errorf("cannot determine muster issuer for token exchange")
	}

	result, err := EstablishConnectionWithTokenExchange(
		ctx, p.aggregator, sub, serverInfo, musterIssuer,
	)
	if err != nil {
		return nil, err
	}

	return result.FormatAsAPIResult(), nil
}

// tryTokenForwarding attempts to establish a connection using ID token forwarding.
// This is used when an MCPServer has forwardToken: true configured.
//
// Returns:
//   - *api.CallToolResult: The connection result if successful
//   - error: The error if token forwarding failed
func (p *AuthToolProvider) tryTokenForwarding(ctx context.Context, sub string, serverInfo *ServerInfo) (*api.CallToolResult, error) {
	sessionID := getSessionIDFromContext(ctx)
	musterIssuer := p.getMusterIssuer(sessionID)
	if musterIssuer == "" {
		logging.Warn("AuthTools", "Cannot determine muster issuer for token forwarding, user %s", sub)
		return nil, fmt.Errorf("cannot determine muster issuer for token forwarding")
	}

	result, err := EstablishConnectionWithTokenForwarding(
		ctx, p.aggregator, sub, serverInfo, musterIssuer,
	)
	if err != nil {
		return nil, err
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
		if info.AuthInfo != nil && info.AuthInfo.Issuer == issuer {
			return false
		}
	}

	return true
}

// is401Error checks if an error indicates a 401 Unauthorized response
// using mcp-go's typed error detection.
func is401Error(err error) bool {
	return pkgoauth.IsOAuthUnauthorizedError(err)
}
