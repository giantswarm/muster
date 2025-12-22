package oauth

import (
	"fmt"
	"net/http"
	"strings"
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
	if header == "" {
		return nil, fmt.Errorf("empty WWW-Authenticate header")
	}

	// Split into scheme and parameters
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) == 0 {
		return nil, fmt.Errorf("invalid WWW-Authenticate header format")
	}

	challenge := &AuthChallenge{
		Scheme: parts[0],
	}

	// If there are parameters, parse them
	if len(parts) > 1 {
		params := parseAuthParams(parts[1])

		if realm, ok := params["realm"]; ok {
			challenge.Realm = realm
			// If realm looks like a URL, use it as the issuer
			if strings.HasPrefix(realm, "http://") || strings.HasPrefix(realm, "https://") {
				challenge.Issuer = realm
			}
		}

		if resourceMeta, ok := params["resource_metadata"]; ok {
			challenge.ResourceMetadataURL = resourceMeta
		}

		if scope, ok := params["scope"]; ok {
			challenge.Scope = scope
		}

		if errCode, ok := params["error"]; ok {
			challenge.Error = errCode
		}

		if errDesc, ok := params["error_description"]; ok {
			challenge.ErrorDescription = errDesc
		}
	}

	return challenge, nil
}

// parseAuthParams parses the parameter portion of a WWW-Authenticate header.
// Parameters are in the format: key1="value1", key2="value2"
func parseAuthParams(paramStr string) map[string]string {
	params := make(map[string]string)

	// Split by comma (but handle quoted values containing commas)
	var currentKey string
	var currentValue strings.Builder
	var inQuotes bool
	var waitingForValue bool

	for i := 0; i < len(paramStr); i++ {
		char := paramStr[i]

		switch {
		case char == '"':
			inQuotes = !inQuotes

		case char == '=' && !inQuotes && currentKey == "":
			// End of key
			currentKey = strings.TrimSpace(currentValue.String())
			currentValue.Reset()
			waitingForValue = true

		case char == ',' && !inQuotes:
			// End of value
			if currentKey != "" {
				value := strings.TrimSpace(currentValue.String())
				// Remove surrounding quotes if present
				value = strings.Trim(value, "\"")
				params[currentKey] = value
			}
			currentKey = ""
			currentValue.Reset()
			waitingForValue = false

		case (char == ' ' || char == '\t') && !inQuotes && !waitingForValue:
			// Skip whitespace outside quotes when not in value
			continue

		default:
			currentValue.WriteByte(char)
		}
	}

	// Handle last parameter
	if currentKey != "" {
		value := strings.TrimSpace(currentValue.String())
		value = strings.Trim(value, "\"")
		params[currentKey] = value
	}

	return params
}

// ExtractAuthChallengeFromResponse extracts auth challenge information from a 401 response.
// It parses the WWW-Authenticate header and returns the parsed challenge.
//
// Returns nil if no WWW-Authenticate header is present or if parsing fails.
func ExtractAuthChallengeFromResponse(resp *http.Response) *AuthChallenge {
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		return nil
	}

	header := resp.Header.Get("WWW-Authenticate")
	if header == "" {
		return nil
	}

	challenge, err := ParseWWWAuthenticate(header)
	if err != nil {
		return nil
	}

	return challenge
}

// Is401Unauthorized checks if an error or response indicates a 401 Unauthorized.
// This is used to detect when OAuth authentication is required.
func Is401Unauthorized(resp *http.Response) bool {
	return resp != nil && resp.StatusCode == http.StatusUnauthorized
}
