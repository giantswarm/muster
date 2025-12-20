package oauth

import (
	"context"
	"net/http"

	"muster/internal/api"
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

// GetToken retrieves a valid token for the given session and server.
func (a *Adapter) GetToken(sessionID, serverName string) *api.OAuthToken {
	token := a.manager.GetToken(sessionID, serverName)
	if token == nil {
		return nil
	}
	return &api.OAuthToken{
		AccessToken: token.AccessToken,
		TokenType:   token.TokenType,
		Scope:       token.Scope,
	}
}

// GetTokenByIssuer retrieves a valid token for the given session and issuer.
func (a *Adapter) GetTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	token := a.manager.GetTokenByIssuer(sessionID, issuer)
	if token == nil {
		return nil
	}
	return &api.OAuthToken{
		AccessToken: token.AccessToken,
		TokenType:   token.TokenType,
		Scope:       token.Scope,
	}
}

// CreateAuthChallenge creates an authentication challenge for a 401 response.
func (a *Adapter) CreateAuthChallenge(ctx context.Context, sessionID, serverName, issuer, scope string) (*api.AuthChallenge, error) {
	// Create WWW-Authenticate params from the issuer and scope
	authParams := &WWWAuthenticateParams{
		Scheme: "Bearer",
		Realm:  issuer,
		Scope:  scope,
	}

	challenge, err := a.manager.CreateAuthChallenge(ctx, sessionID, serverName, authParams)
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

// RegisterServer registers OAuth configuration for a remote MCP server.
func (a *Adapter) RegisterServer(serverName, issuer, scope string) {
	a.manager.RegisterServer(serverName, issuer, scope)
}

// Stop stops the OAuth handler and cleans up resources.
func (a *Adapter) Stop() {
	a.manager.Stop()
}

// Ensure Adapter implements api.OAuthHandler
var _ api.OAuthHandler = (*Adapter)(nil)
