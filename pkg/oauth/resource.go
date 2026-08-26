package oauth

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateResourceURI reports whether raw is usable as an RFC 8707 `resource`
// value: an absolute URI with a host, without userinfo and without a fragment.
//
// It does not rewrite the value. A resource identifier a protected resource
// declares for itself must be sent exactly as declared, because the
// authorization server compares it against the identifier it registered for
// that resource.
func ValidateResourceURI(raw string) error {
	parsed, err := parseResourceURI(raw)
	if err != nil {
		return err
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("resource URI %q carries a fragment, which RFC 8707 §2 forbids", raw)
	}
	return nil
}

// parseResourceURI applies the checks that hold for a resource identifier
// however it was obtained. The fragment rule is not among them: a declared
// value that carries one is unusable, while a derived one simply loses it.
func parseResourceURI(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("resource URI is empty")
	}

	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid resource URI %q: %w", raw, err)
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return nil, fmt.Errorf("resource URI %q is not an absolute URI with a host", raw)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("resource URI %q carries userinfo, which cannot be part of a resource identifier", raw)
	}
	return parsed, nil
}

// CanonicalResourceURI derives a resource identifier for an MCP server from a
// URL, for the case where the server declares none. Use the declared value
// unchanged whenever there is one, and reach for this only to derive one.
//
// The result is an absolute URI without a fragment, with a lowercase scheme
// and host, without the default port for the scheme, and without a trailing
// slash on the path. The most specific form available is preserved: a path
// (for example /mcp) and a query stay on the URI, and percent-encoded
// characters keep their encoding.
func CanonicalResourceURI(raw string) (string, error) {
	parsed, err := parseResourceURI(raw)
	if err != nil {
		return "", err
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
