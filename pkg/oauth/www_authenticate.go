package oauth

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// ParseWWWAuthenticate parses a WWW-Authenticate header value.
// It supports the Bearer scheme with OAuth 2.0 and MCP-specific parameters.
//
// Example headers:
//
//	Bearer realm="https://auth.example.com"
//	Bearer realm="https://auth.example.com", scope="openid profile"
//	Bearer realm="https://auth.example.com", resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"
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

	// Use regex for simple key="value" extraction
	paramRegex := regexp.MustCompile(`(\w+)="([^"]*)"`)
	matches := paramRegex.FindAllStringSubmatch(paramStr, -1)

	for _, match := range matches {
		if len(match) == 3 {
			key := strings.ToLower(match[1])
			value := match[2]
			params[key] = value
		}
	}

	return params
}

// ParseWWWAuthenticateFromResponse extracts auth challenge from a 401 response.
// Returns nil if no WWW-Authenticate header is present or if parsing fails.
func ParseWWWAuthenticateFromResponse(resp *http.Response) *AuthChallenge {
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

// ParseWWWAuthenticateFromError attempts to extract auth challenge from an error.
// This is a best-effort fallback when the HTTP response is not directly available.
//
// It looks for patterns like:
//   - "401" or "Unauthorized" in the error message
//   - Bearer realm="..." patterns
//
// Returns nil if no auth challenge can be extracted.
func ParseWWWAuthenticateFromError(err error) *AuthChallenge {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Check if this looks like a 401 error
	if !strings.Contains(errStr, "401") &&
		!strings.Contains(strings.ToLower(errStr), "unauthorized") {
		return nil
	}

	// Try to find and parse Bearer challenge in error
	if idx := strings.Index(errStr, "Bearer"); idx >= 0 {
		// Extract from Bearer to end of line or string
		remaining := errStr[idx:]
		if endIdx := strings.IndexAny(remaining, "\n\r"); endIdx > 0 {
			remaining = remaining[:endIdx]
		}

		challenge, parseErr := ParseWWWAuthenticate(remaining)
		if parseErr == nil {
			return challenge
		}
	}

	// Return a basic challenge indicating auth is required
	return &AuthChallenge{
		Scheme: "Bearer",
	}
}

// Is401Error checks if an error message indicates a 401 Unauthorized response.
func Is401Error(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "401") ||
		strings.Contains(strings.ToLower(errStr), "unauthorized")
}
