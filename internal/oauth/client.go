package oauth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"muster/pkg/logging"
	pkgoauth "muster/pkg/oauth"
)

// softwareVersion is the version string reported in the Client ID Metadata Document.
// This is informational only and helps identify the muster version during OAuth debugging.
const softwareVersion = "1.0.0"

// Client handles OAuth 2.1 flows for remote MCP server authentication.
type Client struct {
	// Configuration
	clientID     string // The CIMD URL used as client_id
	publicURL    string // The public URL of the Muster Server
	callbackPath string // The path for OAuth callbacks (e.g., "/oauth/callback")

	// Stores
	tokenStore *TokenStore
	stateStore *StateStore

	// Shared OAuth client for protocol operations
	oauthClient *pkgoauth.Client
}

// NewClient creates a new OAuth client with the given configuration.
func NewClient(clientID, publicURL, callbackPath string) *Client {
	return &Client{
		clientID:     clientID,
		publicURL:    publicURL,
		callbackPath: callbackPath,
		tokenStore:   NewTokenStore(),
		stateStore:   NewStateStore(),
		oauthClient:  pkgoauth.NewClient(),
	}
}

// GetTokenStore returns the token store for external access.
func (c *Client) GetTokenStore() *TokenStore {
	return c.tokenStore
}

// GetStateStore returns the state store for external access.
func (c *Client) GetStateStore() *StateStore {
	return c.stateStore
}

// GetRedirectURI returns the full redirect URI for OAuth callbacks.
func (c *Client) GetRedirectURI() string {
	return strings.TrimSuffix(c.publicURL, "/") + c.callbackPath
}

// GetCIMDURL returns the URL where the Client ID Metadata Document is served.
// This is derived from the clientID which is expected to be the CIMD URL.
func (c *Client) GetCIMDURL() string {
	return c.clientID
}

// GetToken retrieves a valid token for the given session and issuer.
// Returns nil if no valid token exists.
func (c *Client) GetToken(sessionID, issuer, scope string) *pkgoauth.Token {
	// First try exact match
	key := TokenKey{
		SessionID: sessionID,
		Issuer:    issuer,
		Scope:     scope,
	}
	if token := c.tokenStore.Get(key); token != nil {
		return token
	}

	// Fall back to issuer-only match for SSO
	return c.tokenStore.GetByIssuer(sessionID, issuer)
}

// GenerateAuthURL creates an OAuth authorization URL for user authentication.
// Returns the URL. The code verifier is stored with the state for later retrieval.
func (c *Client) GenerateAuthURL(ctx context.Context, sessionID, serverName, issuer, scope string) (string, error) {
	// Fetch OAuth metadata for the issuer using shared client
	metadata, err := c.oauthClient.DiscoverMetadata(ctx, issuer)
	if err != nil {
		return "", fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}

	// Generate PKCE code verifier and challenge using shared implementation
	pkce, err := pkgoauth.GeneratePKCE()
	if err != nil {
		return "", fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// Generate state parameter (includes issuer and code verifier)
	state, err := c.stateStore.GenerateState(sessionID, serverName, issuer, pkce.CodeVerifier)
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	// Build authorization URL using shared client
	authURL, err := c.oauthClient.BuildAuthorizationURL(
		metadata.AuthorizationEndpoint,
		c.clientID,
		c.GetRedirectURI(),
		state,
		scope,
		pkce,
	)
	if err != nil {
		return "", fmt.Errorf("failed to build authorization URL: %w", err)
	}

	logging.Debug("OAuth", "Generated auth URL for session=%s server=%s issuer=%s",
		logging.TruncateSessionID(sessionID), serverName, issuer)

	return authURL, nil
}

// ExchangeCode exchanges an authorization code for tokens.
func (c *Client) ExchangeCode(ctx context.Context, code, codeVerifier, issuer string) (*pkgoauth.Token, error) {
	// Fetch OAuth metadata using shared client
	metadata, err := c.oauthClient.DiscoverMetadata(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}

	// Exchange code using shared client
	token, err := c.oauthClient.ExchangeCode(
		ctx,
		metadata.TokenEndpoint,
		code,
		c.GetRedirectURI(),
		c.clientID,
		codeVerifier,
	)
	if err != nil {
		return nil, err
	}

	// Set issuer on the token
	token.Issuer = issuer

	logging.Debug("OAuth", "Successfully exchanged code for token (issuer=%s, expires_in=%d)",
		issuer, token.ExpiresIn)

	return token, nil
}

// RefreshToken refreshes an expired token using its refresh token.
// This operation is logged at INFO level for operational monitoring.
func (c *Client) RefreshToken(ctx context.Context, token *pkgoauth.Token) (*pkgoauth.Token, error) {
	if token.RefreshToken == "" {
		logging.Warn("OAuth", "Token refresh attempted without refresh token (issuer=%s)", token.Issuer)
		return nil, fmt.Errorf("no refresh token available")
	}

	logging.Info("OAuth", "Starting token refresh (issuer=%s)", token.Issuer)
	startTime := time.Now()

	metadata, err := c.oauthClient.DiscoverMetadata(ctx, token.Issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}

	// Refresh token using shared client
	newToken, err := c.oauthClient.RefreshToken(ctx, metadata.TokenEndpoint, token.RefreshToken, c.clientID)
	if err != nil {
		logging.Warn("OAuth", "Token refresh failed (issuer=%s, duration=%v)", token.Issuer, time.Since(startTime))
		return nil, err
	}

	// Set issuer on the new token
	newToken.Issuer = token.Issuer

	// Preserve refresh token if not returned
	if newToken.RefreshToken == "" {
		newToken.RefreshToken = token.RefreshToken
	}

	// Log successful refresh at INFO level for operational monitoring
	logging.Info("OAuth", "Token refresh successful (issuer=%s, expires_in=%ds, duration=%v)",
		token.Issuer, newToken.ExpiresIn, time.Since(startTime))

	return newToken, nil
}

// RefreshTokenIfNeeded checks if a token needs refreshing and refreshes it if necessary.
// Returns the token (refreshed or original), a boolean indicating if refresh occurred, and any error.
func (c *Client) RefreshTokenIfNeeded(ctx context.Context, sessionID, issuer string) (*pkgoauth.Token, bool, error) {
	// Get the token including expiring ones
	token, tokenKey := c.tokenStore.GetByIssuerIncludingExpiring(sessionID, issuer)
	if token == nil {
		return nil, false, fmt.Errorf("no token found for session=%s issuer=%s", logging.TruncateSessionID(sessionID), issuer)
	}

	// Check if token needs refresh
	if !c.tokenStore.NeedsRefresh(token) {
		return token, false, nil
	}

	// Check if we have a refresh token
	if token.RefreshToken == "" {
		logging.Debug("OAuth", "Token needs refresh but no refresh token available (session=%s, issuer=%s)",
			logging.TruncateSessionID(sessionID), issuer)
		return token, false, nil
	}

	logging.Debug("OAuth", "Token expiring soon, attempting refresh (session=%s, issuer=%s, expires_in=%v)",
		logging.TruncateSessionID(sessionID), issuer, time.Until(token.ExpiresAt))

	// Perform the refresh
	newToken, err := c.RefreshToken(ctx, token)
	if err != nil {
		return token, false, fmt.Errorf("token refresh failed: %w", err)
	}

	// Store the refreshed token with the same key
	if tokenKey != nil {
		c.tokenStore.Store(*tokenKey, newToken)
	} else {
		// Fallback: store with new key
		c.StoreToken(sessionID, newToken)
	}

	logging.Info("OAuth", "Token proactively refreshed (session=%s, issuer=%s, new_expiry=%v)",
		logging.TruncateSessionID(sessionID), issuer, newToken.ExpiresAt)

	return newToken, true, nil
}

// StoreToken stores a token in the token store.
func (c *Client) StoreToken(sessionID string, token *pkgoauth.Token) {
	key := TokenKey{
		SessionID: sessionID,
		Issuer:    token.Issuer,
		Scope:     token.Scope,
	}
	c.tokenStore.Store(key, token)
}

// Stop stops background cleanup goroutines.
func (c *Client) Stop() {
	c.tokenStore.Stop()
	c.stateStore.Stop()
}

// GetClientMetadata returns the Client ID Metadata Document for this client.
func (c *Client) GetClientMetadata() *pkgoauth.ClientMetadata {
	return &pkgoauth.ClientMetadata{
		ClientID:                c.clientID,
		ClientName:              "Muster MCP Aggregator",
		ClientURI:               "https://github.com/giantswarm/muster",
		RedirectURIs:            []string{c.GetRedirectURI()},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
		Scope:                   "openid profile email",
		SoftwareID:              "giantswarm-muster",
		SoftwareVersion:         softwareVersion,
	}
}

// DiscoverMetadata fetches OAuth metadata for an issuer.
// This is exposed for external access to metadata discovery.
func (c *Client) DiscoverMetadata(ctx context.Context, issuer string) (*pkgoauth.Metadata, error) {
	return c.oauthClient.DiscoverMetadata(ctx, issuer)
}

// SetHTTPClient sets a custom HTTP client for the OAuth client.
// This is useful for testing.
func (c *Client) SetHTTPClient(httpClient *http.Client) {
	c.oauthClient = pkgoauth.NewClient(pkgoauth.WithHTTPClient(httpClient))
}
