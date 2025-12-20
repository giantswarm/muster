package oauth

import (
	"time"
)

// Token represents an OAuth access token with associated metadata.
type Token struct {
	// AccessToken is the bearer token used for authorization.
	AccessToken string `json:"access_token"`

	// TokenType is typically "Bearer".
	TokenType string `json:"token_type"`

	// RefreshToken is used to obtain new access tokens (optional).
	RefreshToken string `json:"refresh_token,omitempty"`

	// ExpiresIn is the token lifetime in seconds.
	ExpiresIn int `json:"expires_in,omitempty"`

	// ExpiresAt is the calculated expiration timestamp.
	ExpiresAt time.Time `json:"expires_at,omitempty"`

	// Scope is the granted scope(s).
	Scope string `json:"scope,omitempty"`

	// Issuer is the token issuer (Identity Provider URL).
	Issuer string `json:"issuer,omitempty"`
}

// IsExpired checks if the token has expired.
// Returns true if the token is expired or will expire within the given margin.
func (t *Token) IsExpired(margin time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return false // Tokens without expiration don't expire
	}
	return time.Now().Add(margin).After(t.ExpiresAt)
}

// TokenKey uniquely identifies a token in the store.
// Tokens are indexed by session ID, issuer, and scope to enable SSO.
type TokenKey struct {
	SessionID string
	Issuer    string
	Scope     string
}

// AuthChallenge represents an authentication challenge returned when
// a remote MCP server requires OAuth authentication.
type AuthChallenge struct {
	// Status indicates this is an auth required response.
	Status string `json:"status"` // "auth_required"

	// AuthURL is the OAuth authorization URL the user should visit.
	AuthURL string `json:"auth_url"`

	// ServerName is the name of the MCP server requiring authentication.
	ServerName string `json:"server_name,omitempty"`

	// Message is a human-readable description of why auth is needed.
	Message string `json:"message,omitempty"`
}

// OAuthState represents the state parameter data for OAuth flows.
// This is serialized and passed through the OAuth flow to link
// the callback to the original request.
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

// OAuthMetadata represents the OAuth 2.0 Authorization Server Metadata
// as defined in RFC 8414.
type OAuthMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JwksURI                           string   `json:"jwks_uri,omitempty"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
}

// ClientMetadata represents the OAuth 2.0 Client Metadata as defined
// in RFC 7591, used for Client ID Metadata Documents (CIMD).
type ClientMetadata struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
	LogoURI                 string   `json:"logo_uri,omitempty"`
	PolicyURI               string   `json:"policy_uri,omitempty"`
	TermsOfServiceURI       string   `json:"tos_uri,omitempty"`
	SoftwareID              string   `json:"software_id,omitempty"`
	SoftwareVersion         string   `json:"software_version,omitempty"`
}

// WWWAuthenticateParams contains parsed parameters from a WWW-Authenticate header.
type WWWAuthenticateParams struct {
	// Scheme is the authentication scheme (e.g., "Bearer").
	Scheme string

	// Realm is the authentication realm (often the issuer URL).
	Realm string

	// Scope is the required OAuth scope.
	Scope string

	// Error is an OAuth error code if present.
	Error string

	// ErrorDescription provides details about the error.
	ErrorDescription string

	// ResourceMetadataURL is the URL to fetch OAuth metadata (MCP-specific).
	ResourceMetadataURL string
}
