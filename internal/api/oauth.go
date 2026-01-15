package api

import (
	"context"
	"net/http"
	"sync"

	"muster/pkg/logging"
)

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

	// Stop stops the OAuth handler and cleans up resources.
	Stop()
}

// oauthHandler stores the registered OAuth handler implementation.
var oauthHandler OAuthHandler
var oauthMutex sync.RWMutex

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
