package oauth

import (
	"context"
	"fmt"
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
// This function includes basic token fields; use fullTokenToAPIToken to include IDToken.
func tokenToAPIToken(token *pkgoauth.Token) *api.OAuthToken {
	if token == nil {
		return nil
	}
	return &api.OAuthToken{
		AccessToken: token.AccessToken,
		TokenType:   token.TokenType,
		Scope:       token.Scope,
		Issuer:      token.Issuer,
	}
}

// fullTokenToAPIToken converts a pkgoauth.Token to an api.OAuthToken including the IDToken.
// Returns nil if the input token is nil.
// This is used for SSO token forwarding where the ID token is required.
func fullTokenToAPIToken(token *pkgoauth.Token) *api.OAuthToken {
	if token == nil {
		return nil
	}
	return &api.OAuthToken{
		AccessToken: token.AccessToken,
		TokenType:   token.TokenType,
		Scope:       token.Scope,
		IDToken:     token.IDToken,
		Issuer:      token.Issuer,
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

// GetFullTokenByIssuer retrieves the full token (including ID token) for the given session and issuer.
// This is used for SSO token forwarding to downstream MCP servers.
// Returns nil if no valid token exists or if the token doesn't have an ID token.
func (a *Adapter) GetFullTokenByIssuer(sessionID, issuer string) *api.OAuthToken {
	token := a.manager.GetTokenByIssuer(sessionID, issuer)
	if token == nil || token.IDToken == "" {
		return nil
	}
	return fullTokenToAPIToken(token)
}

// FindTokenWithIDToken searches for any token in the session that has an ID token.
// This is used as a fallback when the muster issuer is not explicitly configured.
// Returns the first token found with an ID token, or nil if none exists.
func (a *Adapter) FindTokenWithIDToken(sessionID string) *api.OAuthToken {
	if a.manager == nil || a.manager.client == nil || a.manager.client.tokenStore == nil {
		return nil
	}

	// Get all tokens for the session and find one with an ID token
	allTokens := a.manager.client.tokenStore.GetAllForSession(sessionID)
	for _, token := range allTokens {
		if token != nil && token.IDToken != "" {
			return fullTokenToAPIToken(token)
		}
	}
	return nil
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

// ExchangeTokenForRemoteCluster exchanges a local token for one valid on a remote cluster.
// This implements RFC 8693 Token Exchange for cross-cluster SSO scenarios.
func (a *Adapter) ExchangeTokenForRemoteCluster(ctx context.Context, localToken, userID string, config *api.TokenExchangeConfig) (string, error) {
	if config == nil {
		return "", fmt.Errorf("token exchange config is nil")
	}

	// Pass API config directly - no conversion needed (DRY principle)
	return a.manager.ExchangeTokenForRemoteCluster(ctx, localToken, userID, config)
}

// ExchangeTokenForRemoteClusterWithClient exchanges a local token for one valid on a remote cluster
// using a custom HTTP client. This is used when the token exchange endpoint is accessed via
// Teleport Application Access, which requires mutual TLS authentication.
func (a *Adapter) ExchangeTokenForRemoteClusterWithClient(ctx context.Context, localToken, userID string, config *api.TokenExchangeConfig, httpClient *http.Client) (string, error) {
	if config == nil {
		return "", fmt.Errorf("token exchange config is nil")
	}

	return a.manager.ExchangeTokenForRemoteClusterWithClient(ctx, localToken, userID, config, httpClient)
}

// Stop stops the OAuth handler and cleans up resources.
func (a *Adapter) Stop() {
	a.manager.Stop()
}

// Ensure Adapter implements api.OAuthHandler
var _ api.OAuthHandler = (*Adapter)(nil)
