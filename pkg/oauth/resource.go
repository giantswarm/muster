package oauth

import (
	"fmt"
	"net/url"
	"strings"
)

// CanonicalResourceURI returns the canonical resource identifier for an MCP
// server, as required for the RFC 8707 `resource` parameter.
//
// The result is an absolute URI without a fragment, with a lowercase scheme
// and host, and without a trailing slash on the path. The most specific form
// available is preserved: a path (for example /mcp) and a query stay on the
// URI, because the authorization server compares the value verbatim against
// what the protected resource declares.
func CanonicalResourceURI(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("resource URI is empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid resource URI %q: %w", raw, err)
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return "", fmt.Errorf("resource URI %q is not an absolute URI with a host", raw)
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawPath = ""

	return parsed.String(), nil
}
