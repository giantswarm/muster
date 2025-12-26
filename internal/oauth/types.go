package oauth

import (
	"time"
)

// TokenKey uniquely identifies a token in the store.
// Tokens are indexed by session ID, issuer, and scope to enable SSO.
// This is server-specific as it handles session-scoped token storage.
type TokenKey struct {
	SessionID string
	Issuer    string
	Scope     string
}

// OAuthState represents the state parameter data for OAuth flows.
// This is serialized and passed through the OAuth flow to link
// the callback to the original request.
// This is server-specific as it handles CSRF protection for server-side OAuth.
type OAuthState struct {
	// SessionID links the OAuth flow to the user's session.
	SessionID string `json:"session_id"`

	// ServerName is the MCP server that requires authentication.
	ServerName string `json:"server_name"`

	// Nonce is a random value for CSRF protection.
	Nonce string `json:"nonce"`

	// CreatedAt is when the state was created (for expiration).
	CreatedAt time.Time `json:"created_at"`

	// RedirectURI is where to redirect after callback processing.
	RedirectURI string `json:"redirect_uri,omitempty"`

	// Issuer is the OAuth issuer URL for token exchange.
	Issuer string `json:"issuer,omitempty"`

	// CodeVerifier is the PKCE code verifier for this flow.
	// Stored server-side only, not transmitted in the state parameter.
	CodeVerifier string `json:"-"`
}

// AuthRequiredResponse represents an authentication challenge returned when
// a remote MCP server requires OAuth authentication.
// Note: This is different from pkgoauth.AuthChallenge which represents
// parsed WWW-Authenticate header data. This is a user-facing response.
type AuthRequiredResponse struct {
	// Status indicates this is an auth required response.
	Status string `json:"status"` // "auth_required"

	// AuthURL is the OAuth authorization URL the user should visit.
	AuthURL string `json:"auth_url"`

	// ServerName is the name of the MCP server requiring authentication.
	ServerName string `json:"server_name,omitempty"`

	// Message is a human-readable description of why auth is needed.
	Message string `json:"message,omitempty"`
}
