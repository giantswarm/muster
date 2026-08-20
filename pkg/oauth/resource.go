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
// and host, without the default port for the scheme, and without a trailing
// slash on the path. The most specific form available is preserved: a path
// (for example /mcp) and a query stay on the URI, and percent-encoded
// characters keep their encoding, because the authorization server compares
// the value byte for byte against the identifier the protected resource
// declares.
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
	if parsed.User != nil {
		return "", fmt.Errorf("resource URI %q carries userinfo, which cannot be part of a resource identifier", raw)
	}

	scheme := strings.ToLower(parsed.Scheme)
	host := stripDefaultPort(strings.ToLower(parsed.Host), scheme)

	// EscapedPath preserves the encoding the caller sent, so a path segment
	// that contains an encoded delimiter (%2F) stays distinct from one that
	// contains the delimiter itself.
	path := strings.TrimSuffix(parsed.EscapedPath(), "/")

	canonical := scheme + "://" + host + path
	if parsed.RawQuery != "" {
		canonical += "?" + parsed.RawQuery
	}
	return canonical, nil
}

// stripDefaultPort removes the port from a host when it is the default port
// for the scheme. RFC 3986 §6.2.3 treats the two forms as equivalent, and the
// authorization servers muster talks to normalize them the same way.
func stripDefaultPort(host, scheme string) string {
	switch {
	case scheme == "https" && strings.HasSuffix(host, ":443"):
		return strings.TrimSuffix(host, ":443")
	case scheme == "http" && strings.HasSuffix(host, ":80"):
		return strings.TrimSuffix(host, ":80")
	default:
		return host
	}
}
