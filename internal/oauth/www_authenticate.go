package oauth

import (
	"strings"

	pkgoauth "muster/pkg/oauth"
)

// ParseWWWAuthenticate parses a WWW-Authenticate header value.
// It supports the Bearer scheme with OAuth 2.0 and MCP-specific parameters.
//
// Example header:
//
//	Bearer realm="https://auth.example.com",
//	       scope="openid profile",
//	       resource_metadata="https://mcp.example.com/.well-known/oauth-authorization-server"
func ParseWWWAuthenticate(header string) *WWWAuthenticateParams {
	if header == "" {
		return nil
	}

	// Delegate to shared implementation
	challenge, err := pkgoauth.ParseWWWAuthenticate(header)
	if err != nil {
		return nil
	}

	// Convert to internal type
	return &WWWAuthenticateParams{
		Scheme:              challenge.Scheme,
		Realm:               challenge.Realm,
		Scope:               challenge.Scope,
		Error:               challenge.Error,
		ErrorDescription:    challenge.ErrorDescription,
		ResourceMetadataURL: challenge.ResourceMetadataURL,
	}
}

// IsOAuthChallenge checks if the WWW-Authenticate parameters indicate
// an OAuth authentication challenge (as opposed to other auth types).
func (p *WWWAuthenticateParams) IsOAuthChallenge() bool {
	if p == nil {
		return false
	}

	// Must be Bearer scheme
	if !strings.EqualFold(p.Scheme, "Bearer") {
		return false
	}

	// Should have a realm (issuer) or resource_metadata URL
	return p.Realm != "" || p.ResourceMetadataURL != ""
}

// GetIssuer returns the OAuth issuer from the parameters.
// It prefers the realm but may derive from resource_metadata if needed.
func (p *WWWAuthenticateParams) GetIssuer() string {
	if p == nil {
		return ""
	}
	// The realm is typically the issuer URL
	return p.Realm
}

// ToAuthChallenge converts WWWAuthenticateParams to the shared AuthChallenge type.
func (p *WWWAuthenticateParams) ToAuthChallenge() *pkgoauth.AuthChallenge {
	if p == nil {
		return nil
	}
	return &pkgoauth.AuthChallenge{
		Scheme:              p.Scheme,
		Realm:               p.Realm,
		Issuer:              p.GetIssuer(),
		ResourceMetadataURL: p.ResourceMetadataURL,
		Scope:               p.Scope,
		Error:               p.Error,
		ErrorDescription:    p.ErrorDescription,
	}
}

// FromAuthChallenge creates WWWAuthenticateParams from the shared AuthChallenge type.
func FromAuthChallenge(c *pkgoauth.AuthChallenge) *WWWAuthenticateParams {
	if c == nil {
		return nil
	}
	return &WWWAuthenticateParams{
		Scheme:              c.Scheme,
		Realm:               c.Realm,
		Scope:               c.Scope,
		Error:               c.Error,
		ErrorDescription:    c.ErrorDescription,
		ResourceMetadataURL: c.ResourceMetadataURL,
	}
}
