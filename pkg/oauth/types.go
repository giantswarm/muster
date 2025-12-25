package oauth

import (
	"strings"
	"time"
)

// DefaultExpiryMargin is the default margin when checking token expiry.
// This accounts for clock skew and network latency.
const DefaultExpiryMargin = 30 * time.Second

// Token represents an OAuth access token with associated metadata.
type Token struct {
	// AccessToken is the bearer token used for authorization.
	AccessToken string `json:"access_token"`

	// TokenType is typically "Bearer".
	TokenType string `json:"token_type,omitempty"`

	// RefreshToken is used to obtain new access tokens (optional).
	RefreshToken string `json:"refresh_token,omitempty"`

	// ExpiresIn is the token lifetime in seconds (from token response).
	ExpiresIn int `json:"expires_in,omitempty"`

	// ExpiresAt is the calculated expiration timestamp.
	ExpiresAt time.Time `json:"expires_at,omitempty"`

	// Scope is the granted scope(s), space-separated.
	Scope string `json:"scope,omitempty"`

	// Issuer is the token issuer (Identity Provider URL).
	Issuer string `json:"issuer,omitempty"`

	// IDToken is the OIDC ID token (if available).
	IDToken string `json:"id_token,omitempty"`
}

// IsExpired checks if the token has expired.
// Returns true if the token is expired or will expire within the given margin.
func (t *Token) IsExpired() bool {
	return t.IsExpiredWithMargin(DefaultExpiryMargin)
}

// IsExpiredWithMargin checks if the token has expired or will expire within the margin.
func (t *Token) IsExpiredWithMargin(margin time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return false // Tokens without expiration don't expire
	}
	return time.Now().Add(margin).After(t.ExpiresAt)
}

// SetExpiresAtFromExpiresIn calculates and sets ExpiresAt from ExpiresIn.
func (t *Token) SetExpiresAtFromExpiresIn() {
	if t.ExpiresIn > 0 && t.ExpiresAt.IsZero() {
		t.ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
}

// Scopes returns the scope as a slice of individual scopes.
func (t *Token) Scopes() []string {
	if t.Scope == "" {
		return nil
	}
	return strings.Fields(t.Scope)
}

// Metadata represents OAuth 2.0 Authorization Server Metadata as defined in RFC 8414.
type Metadata struct {
	// Issuer is the authorization server's issuer identifier.
	Issuer string `json:"issuer"`

	// AuthorizationEndpoint is the URL of the authorization endpoint.
	AuthorizationEndpoint string `json:"authorization_endpoint"`

	// TokenEndpoint is the URL of the token endpoint.
	TokenEndpoint string `json:"token_endpoint"`

	// UserinfoEndpoint is the URL of the userinfo endpoint (OIDC).
	UserinfoEndpoint string `json:"userinfo_endpoint,omitempty"`

	// JwksURI is the URL of the JSON Web Key Set.
	JwksURI string `json:"jwks_uri,omitempty"`

	// RegistrationEndpoint is the URL for dynamic client registration.
	RegistrationEndpoint string `json:"registration_endpoint,omitempty"`

	// ScopesSupported lists the OAuth 2.0 scope values supported.
	ScopesSupported []string `json:"scopes_supported,omitempty"`

	// ResponseTypesSupported lists the response_type values supported.
	ResponseTypesSupported []string `json:"response_types_supported,omitempty"`

	// GrantTypesSupported lists the grant types supported.
	GrantTypesSupported []string `json:"grant_types_supported,omitempty"`

	// TokenEndpointAuthMethodsSupported lists the client authentication methods.
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`

	// CodeChallengeMethodsSupported lists the PKCE code challenge methods.
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
}

// SupportsPKCE returns true if the server supports S256 PKCE.
func (m *Metadata) SupportsPKCE() bool {
	for _, method := range m.CodeChallengeMethodsSupported {
		if method == "S256" {
			return true
		}
	}
	// If not specified, assume S256 is supported (OAuth 2.1 requirement)
	return len(m.CodeChallengeMethodsSupported) == 0
}

// AuthChallenge represents parsed information from a WWW-Authenticate header.
// This contains the OAuth server metadata needed to initiate the auth flow.
type AuthChallenge struct {
	// Scheme is the authentication scheme (typically "Bearer" for OAuth 2.0).
	Scheme string

	// Realm is the protection realm (often the authorization server name or URL).
	Realm string

	// Issuer is the OAuth/OIDC issuer URL.
	// This may be derived from the Realm if it's a URL.
	Issuer string

	// ResourceMetadataURL is the URL to the protected resource metadata.
	// This follows RFC 9728 for OAuth 2.0 Protected Resource Metadata.
	ResourceMetadataURL string

	// Scope is the space-separated list of required OAuth scopes.
	Scope string

	// Error is the error code from the WWW-Authenticate header (if any).
	Error string

	// ErrorDescription is a human-readable error description (if any).
	ErrorDescription string
}

// IsOAuthChallenge returns true if this represents an OAuth authentication challenge.
func (c *AuthChallenge) IsOAuthChallenge() bool {
	if c == nil {
		return false
	}
	// Must be Bearer scheme
	if !strings.EqualFold(c.Scheme, "Bearer") {
		return false
	}
	// Should have a realm (issuer) or resource_metadata URL
	return c.Realm != "" || c.ResourceMetadataURL != "" || c.Issuer != ""
}

// GetIssuer returns the OAuth issuer URL.
// It prefers the explicit Issuer field, falls back to Realm if it's a URL.
func (c *AuthChallenge) GetIssuer() string {
	if c == nil {
		return ""
	}
	if c.Issuer != "" {
		return c.Issuer
	}
	// The realm is often the issuer URL
	if strings.HasPrefix(c.Realm, "http://") || strings.HasPrefix(c.Realm, "https://") {
		return c.Realm
	}
	return ""
}

// PKCEChallenge represents a PKCE (Proof Key for Code Exchange) challenge.
// PKCE is required for OAuth 2.1 public clients to prevent authorization code interception.
type PKCEChallenge struct {
	// CodeVerifier is the cryptographically random string (32-96 bytes, base64url-encoded).
	// This is kept secret and never transmitted to the authorization server.
	CodeVerifier string

	// CodeChallenge is the SHA256 hash of the verifier (base64url-encoded).
	// This is sent in the authorization request.
	CodeChallenge string

	// CodeChallengeMethod is always "S256" for security (plain is not allowed in OAuth 2.1).
	CodeChallengeMethod string
}

// ClientMetadata represents OAuth 2.0 Client Metadata as defined in RFC 7591.
// Used for Client ID Metadata Documents (CIMD) in MCP OAuth.
type ClientMetadata struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	LogoURI                 string   `json:"logo_uri,omitempty"`
	PolicyURI               string   `json:"policy_uri,omitempty"`
	TermsOfServiceURI       string   `json:"tos_uri,omitempty"`
	SoftwareID              string   `json:"software_id,omitempty"`
	SoftwareVersion         string   `json:"software_version,omitempty"`
}
