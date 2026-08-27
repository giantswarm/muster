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

// ResourceIndicator is the outcome of resolving an RFC 8707 `resource` value
// for a target.
type ResourceIndicator struct {
	// Value is the indicator to send, or empty for a target that needs none.
	Value string

	// DeclaredErr is set when the target declared a value that was unusable.
	// Value then holds one derived from the target URL instead, so the login
	// still completes; callers log DeclaredErr.
	DeclaredErr error
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
// An empty targetURL yields an empty value and no error, for a target that
// needs no indicator.
func ResolveResourceIndicator(declared, targetURL string) (ResourceIndicator, error) {
	result := ResourceIndicator{}
	if declared != "" {
		result.DeclaredErr = ValidateResourceURI(declared)
		if result.DeclaredErr == nil {
			result.Value = declared
			return result, nil
		}
	}

	if targetURL == "" {
		return result, nil
	}

	derived, err := DeriveResourceURI(targetURL)
	if err != nil {
		return result, fmt.Errorf("cannot derive an RFC 8707 resource indicator from %q: %w", targetURL, err)
	}
	result.Value = derived
	return result, nil
}

// sameOrigin reports whether candidate carries the scheme and the host of
// serverURL. Both must be absolute: url.Parse accepts a relative reference
// without an error, and two empty values would otherwise match each other.
func sameOrigin(candidate, serverURL string) error {
	server, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil {
		return fmt.Errorf("invalid MCP server URL %q: %w", serverURL, err)
	}
	if server.Scheme == "" || server.Host == "" {
		return fmt.Errorf("MCP server URL %q is not absolute, so no origin can be compared", serverURL)
	}

	parsed, err := url.Parse(strings.TrimSpace(candidate))
	if err != nil {
		return fmt.Errorf("invalid URI %q: %w", candidate, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("URI %q is not absolute", candidate)
	}
	if !strings.EqualFold(parsed.Scheme, server.Scheme) || !strings.EqualFold(parsed.Host, server.Host) {
		return fmt.Errorf("%q is not on the origin of the MCP server %q", candidate, serverURL)
	}
	return nil
}

// ValidateAdvertisedMetadataURL reports whether a protected resource metadata
// URL taken from an untrusted source may be fetched for a given MCP server.
// The candidate must be absolute and must share the server's scheme and host.
//
// RFC 9728 §3.2 requires a protected resource to serve its own metadata. A
// resource that points a client at another origin is either misconfigured or
// hostile: the document it names states both the authorization server to use
// and the resource identifier to bind the token to, so following it lets the
// resource obtain a token for something it does not own.
func ValidateAdvertisedMetadataURL(candidate, serverURL string) error {
	if err := sameOrigin(candidate, serverURL); err != nil {
		return fmt.Errorf("advertised metadata URL: %w", err)
	}
	return nil
}

// ValidateAdvertisedResource reports whether a resource identifier declared in
// a metadata document that an untrusted pointer named may be used for a given
// MCP server. The declared value must be on the server's own origin.
//
// A document the well-known path reaches is bound to the server by the URL
// construction. One an advertised pointer named is not, so the identifier it
// declares is checked here. The rule is the origin and not the whole URI,
// because a server legitimately declares an identifier whose path differs from
// its MCP endpoint: an mcp-oauth backend serving at "<base>/mcp" declares
// "<base>". What the check does refuse is an identifier belonging to another
// party, which is the value that would turn muster's token into one minted for
// a resource the backend does not own.
func ValidateAdvertisedResource(declared, serverURL string) error {
	if err := sameOrigin(declared, serverURL); err != nil {
		return fmt.Errorf("declared resource: %w", err)
	}
	return nil
}

// ProtectedResourceMetadataURLs returns the RFC 9728 well-known URLs to probe
// for an MCP server, most specific first: the path-inserted form the MCP
// specification mandates, then the root form for a server that serves one
// document for every path. A document these URLs reach is bound to the server
// by the URL construction, which is why its declared resource needs no further
// origin check.
func ProtectedResourceMetadataURLs(serverURL string) ([]string, error) {
	parsed, err := parseResourceURI(serverURL)
	if err != nil {
		return nil, err
	}

	root := parsed.Scheme + "://" + parsed.Host
	path := strings.TrimSuffix(parsed.EscapedPath(), "/")
	if path == "" {
		return []string{root + WellKnownProtectedResource}, nil
	}
	return []string{
		root + WellKnownProtectedResource + path,
		root + WellKnownProtectedResource,
	}, nil
}
