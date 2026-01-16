package aggregator

import (
	"context"

	"muster/internal/api"
	"muster/internal/mcpserver"
	"muster/internal/oauth"
)

// SessionTokenProvider provides OAuth access tokens for session connections.
// It implements the mcpserver.TokenProvider interface and supports automatic
// token refresh.
//
// This type is the key component for Issue #214 - automatic token refresh:
// - On each HTTP request, GetAccessToken is called
// - If the token is expiring, it's proactively refreshed
// - The refreshed token is stored back in the token store
// - All session connections sharing the same TokenKey benefit from the refresh
//
// SSO Protection:
// Multiple session connections can share the same TokenKey (via issuer matching).
// When a token is refreshed, all connections using that TokenKey automatically
// get the refreshed token because they look up from the same token store.
//
// Architecture Note:
// This type uses the api.OAuthHandler interface to follow the service locator
// pattern. It does not directly access internal OAuth types.
type SessionTokenProvider struct {
	sessionID string
	issuer    string
	scope     string

	// oauthHandler provides token operations via the API layer
	oauthHandler api.OAuthHandler
}

// NewSessionTokenProvider creates a new token provider for a session connection.
//
// Args:
//   - sessionID: The session identifier
//   - issuer: The OAuth issuer URL
//   - scope: The OAuth scope
//   - oauthHandler: The OAuth handler from the API layer
//
// Returns a new SessionTokenProvider that can be used with DynamicAuthClient.
func NewSessionTokenProvider(sessionID, issuer, scope string, oauthHandler api.OAuthHandler) *SessionTokenProvider {
	return &SessionTokenProvider{
		sessionID:    sessionID,
		issuer:       issuer,
		scope:        scope,
		oauthHandler: oauthHandler,
	}
}

// GetAccessToken returns the current access token, refreshing if needed.
// This method is called on each HTTP request to the MCP server.
//
// The refresh logic is delegated to the OAuthHandler.RefreshTokenIfNeeded method,
// which handles:
// 1. Token lookup in the token store
// 2. Checking if refresh is needed (within threshold)
// 3. Performing the refresh if needed
// 4. Returning the (potentially refreshed) access token
//
// Note: The scope field is not used here because token lookup is done by
// sessionID + issuer. The scope is only needed for GetTokenKey() which is
// used when storing tokens. The OAuth token store uses issuer as the primary
// lookup key, allowing tokens with the same issuer to be shared across scopes.
//
// Thread safety: The OAuthHandler implementation handles concurrency internally.
func (p *SessionTokenProvider) GetAccessToken(ctx context.Context) string {
	if p.oauthHandler == nil || !p.oauthHandler.IsEnabled() {
		return ""
	}

	// Use the OAuthHandler's RefreshTokenIfNeeded method which handles
	// token lookup, refresh if needed, and returns the current access token.
	// Scope is not passed here - see method documentation for rationale.
	return p.oauthHandler.RefreshTokenIfNeeded(ctx, p.sessionID, p.issuer)
}

// GetTokenKey returns the token key for this provider.
func (p *SessionTokenProvider) GetTokenKey() *oauth.TokenKey {
	return &oauth.TokenKey{
		SessionID: p.sessionID,
		Issuer:    p.issuer,
		Scope:     p.scope,
	}
}

// Ensure SessionTokenProvider implements mcpserver.TokenProvider
var _ mcpserver.TokenProvider = (*SessionTokenProvider)(nil)
