package oauth

import (
	"net/http"

	pkgoauth "muster/pkg/oauth"
)

// AuthChallenge represents parsed information from a WWW-Authenticate header.
// This contains the OAuth/OIDC server metadata needed to initiate the auth flow.
type AuthChallenge struct {
	// Scheme is the authentication scheme (typically "Bearer" for OAuth 2.0).
	Scheme string

	// Realm is the protection realm (often the authorization server name).
	Realm string

	// Issuer is the OAuth/OIDC issuer URL (from realm or resource_metadata).
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

// ParseWWWAuthenticate parses a WWW-Authenticate header value.
// It extracts OAuth 2.0 and OIDC parameters needed to initiate the auth flow.
//
// Example WWW-Authenticate header:
//
//	Bearer realm="https://dex.example.com",
//	       resource_metadata="https://muster.example.com/.well-known/oauth-protected-resource"
//
// Returns an AuthChallenge with the parsed parameters, or an error if parsing fails.
func ParseWWWAuthenticate(header string) (*AuthChallenge, error) {
	// Delegate to shared implementation
	sharedChallenge, err := pkgoauth.ParseWWWAuthenticate(header)
	if err != nil {
		return nil, err
	}

	// Convert to internal type
	return &AuthChallenge{
		Scheme:              sharedChallenge.Scheme,
		Realm:               sharedChallenge.Realm,
		Issuer:              sharedChallenge.GetIssuer(),
		ResourceMetadataURL: sharedChallenge.ResourceMetadataURL,
		Scope:               sharedChallenge.Scope,
		Error:               sharedChallenge.Error,
		ErrorDescription:    sharedChallenge.ErrorDescription,
	}, nil
}

// ExtractAuthChallengeFromResponse extracts auth challenge information from a 401 response.
// It parses the WWW-Authenticate header and returns the parsed challenge.
//
// Returns nil if no WWW-Authenticate header is present or if parsing fails.
func ExtractAuthChallengeFromResponse(resp *http.Response) *AuthChallenge {
	sharedChallenge := pkgoauth.ParseWWWAuthenticateFromResponse(resp)
	if sharedChallenge == nil {
		return nil
	}

	return &AuthChallenge{
		Scheme:              sharedChallenge.Scheme,
		Realm:               sharedChallenge.Realm,
		Issuer:              sharedChallenge.GetIssuer(),
		ResourceMetadataURL: sharedChallenge.ResourceMetadataURL,
		Scope:               sharedChallenge.Scope,
		Error:               sharedChallenge.Error,
		ErrorDescription:    sharedChallenge.ErrorDescription,
	}
}

// Is401Unauthorized checks if an error or response indicates a 401 Unauthorized.
// This is used to detect when OAuth authentication is required.
func Is401Unauthorized(resp *http.Response) bool {
	return resp != nil && resp.StatusCode == http.StatusUnauthorized
}

// ToSharedChallenge converts to the shared AuthChallenge type.
func (c *AuthChallenge) ToSharedChallenge() *pkgoauth.AuthChallenge {
	if c == nil {
		return nil
	}
	return &pkgoauth.AuthChallenge{
		Scheme:              c.Scheme,
		Realm:               c.Realm,
		Issuer:              c.Issuer,
		ResourceMetadataURL: c.ResourceMetadataURL,
		Scope:               c.Scope,
		Error:               c.Error,
		ErrorDescription:    c.ErrorDescription,
	}
}
