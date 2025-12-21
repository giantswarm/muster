package oauth

import (
	"regexp"
	"strings"
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

	params := &WWWAuthenticateParams{}

	// Extract the scheme (first word before space)
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 0 {
		return nil
	}

	params.Scheme = strings.TrimSpace(parts[0])

	// If there's no parameter section, return just the scheme
	if len(parts) == 1 {
		return params
	}

	// Parse key="value" pairs
	paramStr := parts[1]
	paramRegex := regexp.MustCompile(`(\w+)="([^"]*)"`)
	matches := paramRegex.FindAllStringSubmatch(paramStr, -1)

	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		key := strings.ToLower(match[1])
		value := match[2]

		switch key {
		case "realm":
			params.Realm = value
		case "scope":
			params.Scope = value
		case "error":
			params.Error = value
		case "error_description":
			params.ErrorDescription = value
		case "resource_metadata":
			params.ResourceMetadataURL = value
		}
	}

	return params
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
