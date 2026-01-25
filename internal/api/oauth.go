package api

import (
	"context"
	"net/http"
	"sync"

	"muster/pkg/logging"
)

// AuthCompletionCallback is called after successful OAuth authentication.
// The aggregator registers this callback to establish session connections
// when users authenticate to MCP servers via the browser OAuth flow.
//
// Args:
//   - ctx: Context for the operation
//   - sessionID: The MCP session ID that authenticated
//   - serverName: The name of the MCP server that was authenticated to
//   - accessToken: The access token to use for the connection
//
// Returns an error if the connection could not be established.
type AuthCompletionCallback func(ctx context.Context, sessionID, serverName, accessToken string) error

// SessionInitCallback is called when a new session is first seen with a valid muster token.
// The aggregator registers this callback to trigger proactive SSO connections to
// all SSO-enabled servers (forwardToken: true) using muster's ID token.
//
// This callback is triggered on the first authenticated MCP request for a session,
// enabling seamless SSO: users authenticate once to muster (via `muster auth login`)
// and automatically gain access to all SSO-enabled MCP servers.
//
// Args:
//   - ctx: Context containing the ID token for forwarding
//   - sessionID: The MCP session ID
//
// The callback should not return an error - SSO connection failures are logged
// but don't prevent the request from proceeding.
type SessionInitCallback func(ctx context.Context, sessionID string)

// OAuthHandler defines the interface for OAuth proxy functionality.
// This handler manages OAuth authentication flows for remote MCP servers,
// including token storage, authentication challenges, and callback handling.
//
// The OAuth handler acts as a proxy, managing OAuth flows on behalf of users
// without exposing sensitive tokens to the Muster Agent.
type OAuthHandler interface {
	// IsEnabled returns whether OAuth proxy functionality is active.
	IsEnabled() bool

	// GetToken retrieves a valid token for the given session and server.
	// Returns nil if no valid token exists.
	GetToken(sessionID, serverName string) *OAuthToken

	// GetTokenByIssuer retrieves a valid token for the given session and issuer.
	// This is used for SSO when we have the issuer from a 401 response.
	GetTokenByIssuer(sessionID, issuer string) *OAuthToken

	// GetFullTokenByIssuer retrieves the full token (including ID token) for the given session and issuer.
	// This is used for SSO token forwarding to downstream MCP servers.
	// Returns nil if no valid token exists or if the token doesn't have an ID token.
	GetFullTokenByIssuer(sessionID, issuer string) *OAuthToken

	// FindTokenWithIDToken searches for any token in the session that has an ID token.
	// This is used as a fallback when the muster issuer is not explicitly configured.
	// Returns the first token found with an ID token, or nil if none exists.
	FindTokenWithIDToken(sessionID string) *OAuthToken

	// ClearTokenByIssuer removes all tokens for a given session and issuer.
	// This is used to clear invalid/expired tokens before requesting fresh authentication.
	ClearTokenByIssuer(sessionID, issuer string)

	// CreateAuthChallenge creates an authentication challenge for a 401 response.
	// Returns the challenge containing the auth URL for the user to visit.
	CreateAuthChallenge(ctx context.Context, sessionID, serverName, issuer, scope string) (*AuthChallenge, error)

	// GetHTTPHandler returns the HTTP handler for OAuth callback endpoints.
	GetHTTPHandler() http.Handler

	// GetCallbackPath returns the configured callback path (e.g., "/oauth/proxy/callback").
	GetCallbackPath() string

	// GetCIMDPath returns the path for serving the CIMD (e.g., "/.well-known/oauth-client.json").
	GetCIMDPath() string

	// ShouldServeCIMD returns true if muster should serve its own CIMD.
	ShouldServeCIMD() bool

	// GetCIMDHandler returns the HTTP handler for serving the CIMD.
	GetCIMDHandler() http.HandlerFunc

	// RegisterServer registers OAuth configuration for a remote MCP server.
	RegisterServer(serverName, issuer, scope string)

	// SetAuthCompletionCallback sets the callback to be called after successful authentication.
	// The aggregator uses this to establish session connections after browser OAuth completes.
	SetAuthCompletionCallback(callback AuthCompletionCallback)

	// RefreshTokenIfNeeded checks if the token for the given session and issuer needs refresh,
	// and refreshes it if necessary. Returns the current (potentially refreshed) access token.
	// Returns an empty string if no token exists or refresh failed without a fallback.
	// This method is used for automatic token refresh in long-running sessions.
	RefreshTokenIfNeeded(ctx context.Context, sessionID, issuer string) string

	// ExchangeTokenForRemoteCluster exchanges a local token for one valid on a remote cluster.
	// This implements RFC 8693 Token Exchange for cross-cluster SSO scenarios.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - localToken: The local ID token to exchange
	//   - userID: The user's unique identifier (from validated JWT 'sub' claim)
	//   - config: Token exchange configuration for the remote cluster
	//
	// Returns the exchanged access token, or an error if exchange fails.
	ExchangeTokenForRemoteCluster(ctx context.Context, localToken, userID string, config *TokenExchangeConfig) (string, error)

	// ExchangeTokenForRemoteClusterWithClient exchanges a local token for one valid on a remote cluster
	// using a custom HTTP client. This is used when the token exchange endpoint is accessed via
	// Teleport Application Access, which requires mutual TLS authentication.
	//
	// The httpClient parameter should be configured with the appropriate TLS certificates
	// (e.g., Teleport Machine ID certificates). If nil, uses the default HTTP client.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - localToken: The local ID token to exchange
	//   - userID: The user's unique identifier (from validated JWT 'sub' claim)
	//   - config: Token exchange configuration for the remote cluster
	//   - httpClient: Custom HTTP client with Teleport TLS certificates (or nil for default)
	//
	// Returns the exchanged access token, or an error if exchange fails.
	ExchangeTokenForRemoteClusterWithClient(ctx context.Context, localToken, userID string, config *TokenExchangeConfig, httpClient *http.Client) (string, error)

	// Stop stops the OAuth handler and cleans up resources.
	Stop()
}

// oauthHandler stores the registered OAuth handler implementation.
var oauthHandler OAuthHandler
var oauthMutex sync.RWMutex

// sessionInitCallback stores the registered session initialization callback.
var sessionInitCallback SessionInitCallback
var sessionInitMutex sync.RWMutex

// RegisterSessionInitCallback registers a callback for session initialization.
// This callback is triggered on the first authenticated MCP request for a session,
// enabling proactive SSO connections to be established.
//
// Thread-safe: Yes, protected by sessionInitMutex.
func RegisterSessionInitCallback(cb SessionInitCallback) {
	sessionInitMutex.Lock()
	defer sessionInitMutex.Unlock()
	logging.Debug("API", "Registering session init callback: %v", cb != nil)
	sessionInitCallback = cb
}

// GetSessionInitCallback returns the registered session initialization callback.
// Returns nil if no callback has been registered.
//
// Thread-safe: Yes, protected by sessionInitMutex read lock.
func GetSessionInitCallback() SessionInitCallback {
	sessionInitMutex.RLock()
	defer sessionInitMutex.RUnlock()
	return sessionInitCallback
}

// RegisterOAuthHandler registers the OAuth handler implementation.
// This handler provides OAuth proxy functionality for remote MCP server authentication.
//
// The registration is thread-safe and should be called during system initialization.
// Only one OAuth handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: OAuthHandler implementation that manages OAuth operations
//
// Thread-safe: Yes, protected by oauthMutex.
func RegisterOAuthHandler(h OAuthHandler) {
	oauthMutex.Lock()
	defer oauthMutex.Unlock()
	logging.Debug("API", "Registering OAuth handler: %v", h != nil)
	oauthHandler = h
}

// GetOAuthHandler returns the registered OAuth handler.
// This provides access to OAuth proxy functionality for remote MCP server authentication.
//
// Returns nil if no handler has been registered yet or if OAuth is disabled.
// Callers should always check for nil before using the returned handler.
//
// Returns:
//   - OAuthHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by oauthMutex read lock.
func GetOAuthHandler() OAuthHandler {
	oauthMutex.RLock()
	defer oauthMutex.RUnlock()
	return oauthHandler
}
