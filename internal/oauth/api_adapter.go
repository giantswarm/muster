package oauth

import (
	"context"
	"net/http"

	"muster/internal/api"
	"muster/pkg/logging"
	pkgoauth "muster/pkg/oauth"
)

// Adapter implements api.OAuthHandler by wrapping the OAuth Manager.
// This follows the service locator pattern where packages communicate
// through interfaces defined in the api package.
type Adapter struct {
	manager *Manager
}

// NewAdapter creates a new OAuth API adapter wrapping the given manager.
func NewAdapter(manager *Manager) *Adapter {
	return &Adapter{
		manager: manager,
	}
}

// Register registers this adapter with the API layer.
func (a *Adapter) Register() {
	api.RegisterOAuthHandler(a)
}

// IsEnabled returns whether OAuth proxy functionality is active.
func (a *Adapter) IsEnabled() bool {
	return a.manager.IsEnabled()
}

// tokenToAPIToken converts a pkgoauth.Token to an api.OAuthToken.
// Returns nil if the input token is nil.
func tokenToAPIToken(token *pkgoauth.Token) *api.OAuthToken {
	if token == nil {
		return nil
	}
	return &api.OAuthToken{
		AccessToken: token.AccessToken,
		TokenType:   token.TokenType,
		Scope:       token.Scope,
	}
}

// GetToken retrieves a valid token for the given session and server.
func (a *Adapter) GetToken(sessionID, serverName string) *api.OAuthToken {
	return tokenToAPIToken(a.manager.GetToken(sessionID, serverName))
}

// GetTokenByIssuer retrieves a valid token for the given session and issuer.
func (a *Adapter) GetTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	return tokenToAPIToken(a.manager.GetTokenByIssuer(sessionID, issuer))
}

// ClearTokenByIssuer removes all tokens for a given session and issuer.
func (a *Adapter) ClearTokenByIssuer(sessionID, issuer string) {
	a.manager.ClearTokenByIssuer(sessionID, issuer)
}

// CreateAuthChallenge creates an authentication challenge for a 401 response.
func (a *Adapter) CreateAuthChallenge(ctx context.Context, sessionID, serverName, issuer, scope string) (*api.AuthChallenge, error) {
	challenge, err := a.manager.CreateAuthChallenge(ctx, sessionID, serverName, issuer, scope)
	if err != nil {
		return nil, err
	}

	return &api.AuthChallenge{
		Status:     challenge.Status,
		AuthURL:    challenge.AuthURL,
		ServerName: challenge.ServerName,
		Message:    challenge.Message,
	}, nil
}

// GetHTTPHandler returns the HTTP handler for OAuth callback endpoints.
func (a *Adapter) GetHTTPHandler() http.Handler {
	return a.manager.GetHTTPHandler()
}

// GetCallbackPath returns the configured callback path.
func (a *Adapter) GetCallbackPath() string {
	return a.manager.GetCallbackPath()
}

// GetCIMDPath returns the path for serving the CIMD.
func (a *Adapter) GetCIMDPath() string {
	return a.manager.GetCIMDPath()
}

// ShouldServeCIMD returns true if muster should serve its own CIMD.
func (a *Adapter) ShouldServeCIMD() bool {
	return a.manager.ShouldServeCIMD()
}

// GetCIMDHandler returns the HTTP handler for serving the CIMD.
func (a *Adapter) GetCIMDHandler() http.HandlerFunc {
	return a.manager.GetCIMDHandler()
}

// RegisterServer registers OAuth configuration for a remote MCP server.
func (a *Adapter) RegisterServer(serverName, issuer, scope string) {
	a.manager.RegisterServer(serverName, issuer, scope)
}

// SetAuthCompletionCallback sets the callback to be called after successful authentication.
func (a *Adapter) SetAuthCompletionCallback(callback api.AuthCompletionCallback) {
	// Wrap the api callback with the oauth callback type
	a.manager.SetAuthCompletionCallback(func(ctx context.Context, sessionID, serverName, accessToken string) error {
		return callback(ctx, sessionID, serverName, accessToken)
	})
}

// RefreshTokenIfNeeded checks if the token needs refresh and refreshes it if necessary.
// Returns the current (potentially refreshed) access token, or empty string if unavailable.
func (a *Adapter) RefreshTokenIfNeeded(ctx context.Context, sessionID, issuer string) string {
	token, _, err := a.manager.RefreshTokenIfNeeded(ctx, sessionID, issuer)
	if err != nil {
		logging.Debug("OAuth", "RefreshTokenIfNeeded error (session=%s, issuer=%s): %v",
			logging.TruncateSessionID(sessionID), issuer, err)
	}
	if token != nil {
		return token.AccessToken
	}
	return ""
}

// Stop stops the OAuth handler and cleans up resources.
func (a *Adapter) Stop() {
	a.manager.Stop()
}

// Ensure Adapter implements api.OAuthHandler
var _ api.OAuthHandler = (*Adapter)(nil)
