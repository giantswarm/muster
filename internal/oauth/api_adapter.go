package oauth

import (
	"context"
	"fmt"
	"net/http"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/api"
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
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.ExpiresAt,
		Scope:        token.Scope,
		Issuer:       token.Issuer,
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
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.ExpiresAt,
		Scope:        token.Scope,
		IDToken:      token.IDToken,
		Issuer:       token.Issuer,
	}
}

// apiTokenToPkgToken converts an api.OAuthToken back to a pkgoauth.Token.
// Returns nil if the input token is nil.
func apiTokenToPkgToken(token *api.OAuthToken) *pkgoauth.Token {
	if token == nil {
		return nil
	}
	return &pkgoauth.Token{
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.ExpiresAt,
		Scope:        token.Scope,
		IDToken:      token.IDToken,
		Issuer:       token.Issuer,
	}
}

// GetToken retrieves a valid token for the given subject and server.
func (a *Adapter) GetToken(subject, serverName string) *api.OAuthToken {
	return tokenToAPIToken(a.manager.GetToken(subject, serverName))
}

// GetTokenByIssuer retrieves a valid token for the given subject and issuer.
func (a *Adapter) GetTokenByIssuer(subject, issuer string) *api.OAuthToken {
	return tokenToAPIToken(a.manager.GetTokenByIssuer(subject, issuer))
}

// GetFullTokenByIssuer retrieves the full token (including ID token if available)
// for the given subject and issuer. Returns nil if no valid token exists.
// The IDToken field may be empty if the token was obtained without an ID token.
func (a *Adapter) GetFullTokenByIssuer(subject, issuer string) *api.OAuthToken {
	token := a.manager.GetTokenByIssuer(subject, issuer)
	return fullTokenToAPIToken(token)
}

// FindTokenWithIDToken searches for any token for the subject that has an ID token.
// This is used as a fallback when the muster issuer is not explicitly configured.
// Returns the first token found with an ID token, or nil if none exists.
func (a *Adapter) FindTokenWithIDToken(subject string) *api.OAuthToken {
	if a.manager == nil || a.manager.client == nil || a.manager.client.tokenStore == nil {
		return nil
	}

	// Get all tokens for the subject and find one with an ID token
	allTokens := a.manager.client.tokenStore.GetAllForUser(subject)
	for _, token := range allTokens {
		if token != nil && token.IDToken != "" {
			return fullTokenToAPIToken(token)
		}
	}
	return nil
}

// StoreToken persists a token for the given subject and issuer.
// This converts the API token to a pkg/oauth token and delegates to the manager's single backing store.
func (a *Adapter) StoreToken(subject, issuer string, token *api.OAuthToken) {
	a.manager.StoreToken(subject, issuer, apiTokenToPkgToken(token))
}

// ClearTokenByIssuer removes all tokens for a given subject and issuer.
func (a *Adapter) ClearTokenByIssuer(subject, issuer string) {
	a.manager.ClearTokenByIssuer(subject, issuer)
}

// DeleteTokensByUser removes all downstream tokens for a given subject.
func (a *Adapter) DeleteTokensByUser(subject string) {
	a.manager.DeleteTokensByUser(subject)
}

// CreateAuthChallenge creates an authentication challenge for a 401 response.
func (a *Adapter) CreateAuthChallenge(ctx context.Context, subject, serverName, issuer, scope string) (*api.AuthChallenge, error) {
	challenge, err := a.manager.CreateAuthChallenge(ctx, subject, serverName, issuer, scope)
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
	a.manager.SetAuthCompletionCallback(func(ctx context.Context, subject, serverName, accessToken string) error {
		return callback(ctx, subject, serverName, accessToken)
	})
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
