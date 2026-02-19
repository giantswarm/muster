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
	"errors"
	"fmt"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/client/transport"
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
//   - Rate limiting: Prevents OAuth flow abuse by limiting attempts per session
//   - Metrics: Tracks login attempts, successes, and failures for monitoring
func (p *AuthToolProvider) handleAuthLogin(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return &api.CallToolResult{
			Content: []interface{}{"Error: 'server' argument is required and must be a string"},
			IsError: true,
		}, nil
	}

	// Get session ID early for rate limiting and metrics
	sessionID := getSessionIDFromContext(ctx)

	// Check rate limit before processing
	if p.aggregator.authRateLimiter != nil && !p.aggregator.authRateLimiter.Allow(sessionID, serverName) {
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordRateLimitBlock(serverName, sessionID)
		}
		remaining := 0
		if p.aggregator.authRateLimiter != nil {
			remaining = p.aggregator.authRateLimiter.RemainingAttempts(sessionID)
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
		p.aggregator.authMetrics.RecordLoginAttempt(serverName, sessionID)
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

	// Check if this session already has a connection to this server
	// This can happen after proactive SSO or previous authentication
	if p.aggregator.sessionRegistry != nil {
		if conn, exists := p.aggregator.sessionRegistry.GetConnection(sessionID, serverName); exists && conn != nil && conn.Status == StatusSessionConnected {
			logging.Debug("AuthTools", "Session %s already has connection to server %s",
				logging.TruncateSessionID(sessionID), serverName)
			return &api.CallToolResult{
				Content: []interface{}{fmt.Sprintf("Server '%s' is already authenticated for this session.", serverName)},
				IsError: false,
			}, nil
		}
	}

	// Check if OAuth handler is available
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordLoginFailure(serverName, sessionID, "oauth_not_configured")
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

		result, err := p.tryTokenExchange(ctx, sessionID, serverInfo)
		if err != nil {
			logging.Warn("AuthTools", "Token exchange failed for server %s: %v", serverName, err)
			if p.aggregator.authMetrics != nil {
				p.aggregator.authMetrics.RecordLoginFailure(serverName, sessionID, "token_exchange_failed")
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
			p.aggregator.authMetrics.RecordLoginSuccess(serverName, sessionID)
		}
		if p.aggregator.authRateLimiter != nil {
			p.aggregator.authRateLimiter.Reset(sessionID)
		}
		return result, nil
	} else if ShouldUseTokenForwarding(serverInfo) {
		// Check if token forwarding is enabled for this server
		logging.Info("AuthTools", "Token forwarding is enabled for server %s, attempting SSO", serverName)

		// Get the muster issuer from the OAuth server configuration
		// For token forwarding, we use the same issuer that muster authenticated the user with
		result, err := p.tryTokenForwarding(ctx, sessionID, serverInfo)
		if err != nil {
			logging.Warn("AuthTools", "Token forwarding failed for server %s: %v", serverName, err)
			if p.aggregator.authMetrics != nil {
				p.aggregator.authMetrics.RecordLoginFailure(serverName, sessionID, "token_forwarding_failed")
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
			p.aggregator.authMetrics.RecordLoginSuccess(serverName, sessionID)
		}
		if p.aggregator.authRateLimiter != nil {
			p.aggregator.authRateLimiter.Reset(sessionID)
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
			p.aggregator.authMetrics.RecordLoginFailure(serverName, sessionID, "issuer_discovery_failed")
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

	// Check if this session already has a connection to THIS specific server.
	// This provides a clear message to the user that they're already connected.
	if p.aggregator.sessionRegistry != nil {
		if conn, exists := p.aggregator.sessionRegistry.GetConnection(sessionID, serverName); exists && conn != nil && conn.Status == StatusSessionConnected {
			logging.Info("AuthTools", "Session %s already has connection to server %s",
				logging.TruncateSessionID(sessionID), serverName)
			// Record success metrics since the user is already connected
			if p.aggregator.authMetrics != nil {
				p.aggregator.authMetrics.RecordLoginSuccess(serverName, sessionID)
			}
			return &api.CallToolResult{
				Content: []interface{}{fmt.Sprintf(
					api.AuthMsgAlreadyConnected+"\n\n"+
						"Server: %s\n"+
						"Status: connected\n\n"+
						"You are already authenticated to this server. Use core_auth_logout to disconnect first if you want to re-authenticate with a different account.",
					serverName,
				)},
				IsError: false,
			}, nil
		}
	}

	// Check if we already have a valid token for this server/issuer (SSO).
	// This enables single sign-on: if the user authenticated to another server
	// with the same OAuth issuer, we can reuse that token.
	token := oauthHandler.GetTokenByIssuer(sessionID, authInfo.Issuer)

	if token != nil {
		logging.Info("AuthTools", "Found existing token for server %s via SSO (issuer=%s), attempting to connect",
			serverName, authInfo.Issuer)

		// Try to establish connection using the existing token
		connectResult, connectErr := p.tryConnectWithToken(ctx, sessionID, serverName, serverInfo.URL, authInfo.Issuer, authInfo.Scope, token.AccessToken)
		if connectErr == nil {
			// Record success and reset rate limiter for this session
			if p.aggregator.authMetrics != nil {
				p.aggregator.authMetrics.RecordLoginSuccess(serverName, sessionID)
			}
			if p.aggregator.authRateLimiter != nil {
				p.aggregator.authRateLimiter.Reset(sessionID)
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
				p.aggregator.authMetrics.RecordLoginFailure(serverName, sessionID, "connection_failed")
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
	challenge, err := oauthHandler.CreateAuthChallenge(ctx, sessionID, serverName, authInfo.Issuer, authInfo.Scope)
	if err != nil {
		logging.Error("AuthTools", err, "Failed to create auth challenge for server %s", serverName)
		if p.aggregator.authMetrics != nil {
			p.aggregator.authMetrics.RecordLoginFailure(serverName, sessionID, "challenge_creation_failed")
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

// handleAuthLogout clears authentication session for a specific MCP server.
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

	// Get session ID from context
	sessionID := getSessionIDFromContext(ctx)

	// Record the logout attempt in metrics
	if p.aggregator.authMetrics != nil {
		p.aggregator.authMetrics.RecordLogoutAttempt(serverName, sessionID)
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

	// Record logout success
	if p.aggregator.authMetrics != nil {
		p.aggregator.authMetrics.RecordLogoutSuccess(serverName, sessionID)
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
// This delegates to the shared establishSessionConnection helper to avoid code duplication.
func (p *AuthToolProvider) tryConnectWithToken(ctx context.Context, sessionID, serverName, serverURL, issuer, scope, accessToken string) (*api.CallToolResult, error) {
	result, err := establishSessionConnection(ctx, p.aggregator, sessionID, serverName, serverURL, issuer, scope, accessToken)
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
func (p *AuthToolProvider) tryTokenExchange(ctx context.Context, sessionID string, serverInfo *ServerInfo) (*api.CallToolResult, error) {
	// Get the muster issuer from the configuration
	musterIssuer := p.getMusterIssuer(sessionID)
	if musterIssuer == "" {
		logging.Warn("AuthTools", "Cannot determine muster issuer for token exchange, session %s",
			logging.TruncateSessionID(sessionID))
		return nil, fmt.Errorf("cannot determine muster issuer for token exchange")
	}

	result, err := EstablishSessionConnectionWithTokenExchange(
		ctx, p.aggregator, sessionID, serverInfo, musterIssuer,
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
func (p *AuthToolProvider) tryTokenForwarding(ctx context.Context, sessionID string, serverInfo *ServerInfo) (*api.CallToolResult, error) {
	// Get the muster issuer from the configuration
	// We need to find what issuer muster used to authenticate the user
	musterIssuer := p.getMusterIssuer(sessionID)
	if musterIssuer == "" {
		logging.Warn("AuthTools", "Cannot determine muster issuer for token forwarding, session %s",
			logging.TruncateSessionID(sessionID))
		return nil, fmt.Errorf("cannot determine muster issuer for token forwarding")
	}

	result, err := EstablishSessionConnectionWithTokenForwarding(
		ctx, p.aggregator, sessionID, serverInfo, musterIssuer,
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
	// OAuth handler must be registered and enabled for token forwarding to work
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return ""
	}

	return p.aggregator.getMusterIssuerWithFallback(sessionID)
}

// is401Error checks if an error indicates a 401 Unauthorized response
// using mcp-go's typed error detection.
func is401Error(err error) bool {
	if err == nil {
		return false
	}
	var oauthErr *transport.OAuthAuthorizationRequiredError
	if errors.As(err, &oauthErr) {
		return true
	}
	return errors.Is(err, transport.ErrUnauthorized)
}
