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

// DeriveResourceURI derives a resource identifier for an MCP server from a
// URL, for the case where the server declares none. Use the declared value
// unchanged whenever there is one, and reach for this only to derive one.
//
// The derivation drops the query and the fragment and changes nothing else:
// the scheme and host keep their case, an explicit port stays, and a trailing
// slash on the path stays. That is the rule mcp-go applies to the same URL
// when it derives its own indicator (client/transport/streamable_http.go
// clears RawQuery and Fragment, and client/transport/oauth.go falls back to
// that value when the metadata omits the field). mcp-go owns token refresh on
// both the agent path and the backend path, so a derivation that normalized
// anything further would bind the initial token and the refreshed token to
// different audiences.
func DeriveResourceURI(raw string) (string, error) {
	parsed, err := parseResourceURI(raw)
	if err != nil {
		return "", err
	}

	derived := *parsed
	derived.RawQuery = ""
	derived.ForceQuery = false
	derived.Fragment = ""
	derived.RawFragment = ""
	return derived.String(), nil
}

// ResolveResourceIndicator picks the RFC 8707 `resource` value for a target:
// the URI the target declares in its RFC 9728 metadata, sent exactly as
// declared, or a value derived from targetURL when the target declares none.
//
// A declared value is never rewritten. The authorization server compares it
// against the identifier registered for that target, and mcp-go sends the
// declared value verbatim on token refresh, so any normalization here would
// bind the initial token and the refreshed token to different audiences.
//
// declaredErr is non-nil when a declared value existed but was unusable. The
// result is then derived from targetURL instead, so a target that publishes a
// malformed identifier still completes a login; callers log declaredErr. An
// empty targetURL yields an empty value and no error, for a target that needs
// no indicator.
func ResolveResourceIndicator(declared, targetURL string) (resource string, declaredErr error, err error) {
	if declared != "" {
		declaredErr = ValidateResourceURI(declared)
		if declaredErr == nil {
			return declared, nil, nil
		}
	}

	if targetURL == "" {
		return "", declaredErr, nil
	}

	derived, err := DeriveResourceURI(targetURL)
	if err != nil {
		return "", declaredErr, fmt.Errorf("cannot derive an RFC 8707 resource indicator from %q: %w", targetURL, err)
	}
	return derived, declaredErr, nil
}
