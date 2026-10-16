package oauth

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// discoveredAuth carries what a discovery probe learned about the server: the
// authentication challenge and, when the server publishes RFC 9728 metadata,
// the canonical resource identifier it declares for itself.
type discoveredAuth struct {
	challenge *pkgoauth.AuthChallenge
	resource  string
}

// discoverAuthChallenge probes the server to discover OAuth auth requirements.
// It first sends a HEAD request to the endpoint to check for a 401 + WWW-Authenticate
// header. If that yields an issuer, it's used directly. Otherwise it falls back to
// fetching /.well-known/oauth-protected-resource (RFC 9728).
//
// The error is set only when the server could not be reached at all (TLS
// verification, connection, DNS); the well-known documents live on the same
// host, so no fallback is tried then. A response without a challenge returns
// nil and no error.
func (m *AuthManager) discoverAuthChallenge(ctx context.Context, serverURL string) (*discoveredAuth, error) {
	httpClient := m.client.GetHTTPClient()

	// Try a HEAD request to the server endpoint to check for 401
	discovered, err := probeEndpoint(ctx, httpClient, serverURL)
	if err != nil || discovered != nil {
		return discovered, err
	}

	// Fall back to RFC 9728 protected resource metadata discovery
	return discoverFromResourceMetadata(ctx, httpClient, serverURL), nil
}

// probeEndpoint sends a HEAD request to the server and parses any 401
// WWW-Authenticate header. It returns an error only when the request got no
// response.
func probeEndpoint(ctx context.Context, httpClient *http.Client, serverURL string) (*discoveredAuth, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, serverURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusUnauthorized {
		return nil, nil
	}

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if wwwAuth == "" {
		return nil, nil
	}

	challenge, err := pkgoauth.ParseWWWAuthenticate(wwwAuth)
	if err != nil {
		return nil, nil
	}

	// The resource metadata is the only place the server states its own
	// canonical resource identifier, so it is fetched even when the
	// challenge already names the issuer. The pointer comes from the
	// server's own header, so it is untrusted input: a document on another
	// origin can name any authorization server and any resource identifier.
	var metadata *protectedResourceMetadata
	if challenge.ResourceMetadataURL != "" {
		if err := pkgoauth.ValidateAdvertisedMetadataURL(challenge.ResourceMetadataURL, serverURL); err != nil {
			slog.Warn("Ignoring the resource_metadata pointer of an MCP server",
				"server_url", serverURL,
				"error", err,
			)
		} else {
			metadata = fetchResourceMetadata(ctx, httpClient, challenge.ResourceMetadataURL)
			metadata.dropForeignResource(serverURL)
		}
	}

	// If we have a direct issuer, use it
	if challenge.GetIssuer() != "" {
		if metadata == nil {
			// The header named the issuer, so discovery is over, but it
			// never carries the resource identifier. mcp-go reads that from
			// the well-known document whatever the header said, and puts it
			// on the refresh request, so muster reads the same document to
			// keep both requests on one audience.
			metadata = fetchWellKnownResourceMetadata(ctx, httpClient, serverURL)
		}
		return &discoveredAuth{challenge: challenge, resource: metadata.resourceIdentifier()}, nil
	}

	if metadata != nil && metadata.issuer() != "" {
		challenge.Issuer = metadata.issuer()
		return &discoveredAuth{challenge: challenge, resource: metadata.resourceIdentifier()}, nil
	}

	return nil, nil
}

// discoverFromResourceMetadata reads the server's own RFC 9728 document from
// the well-known paths and extracts the authorization server issuer.
func discoverFromResourceMetadata(ctx context.Context, httpClient *http.Client, serverURL string) *discoveredAuth {
	metadata := fetchWellKnownResourceMetadata(ctx, httpClient, serverURL)
	if metadata == nil || metadata.issuer() == "" {
		return nil
	}

	return &discoveredAuth{
		challenge: &pkgoauth.AuthChallenge{
			Scheme: "Bearer",
			Issuer: metadata.issuer(),
		},
		resource: metadata.resourceIdentifier(),
	}
}

// protectedResourceMetadata is the JSON structure returned by RFC 9728 endpoints.
type protectedResourceMetadata struct {
	// Resource is the canonical URI the server declares for itself
	// (RFC 9728 §3.2). It is the RFC 8707 `resource` value a client must
	// send when it asks for a token for this server.
	Resource string `json:"resource"`

	AuthorizationServers []string `json:"authorization_servers"`
}

// issuer returns the first authorization server, or an empty string.
func (m *protectedResourceMetadata) issuer() string {
	if m == nil || len(m.AuthorizationServers) == 0 {
		return ""
	}
	return m.AuthorizationServers[0]
}

// resourceIdentifier returns the declared resource URI, or an empty string
// when the document is absent or omits the field.
func (m *protectedResourceMetadata) resourceIdentifier() string {
	if m == nil {
		return ""
	}
	return m.Resource
}

// dropForeignResource clears a declared resource that does not identify the
// server the document was fetched for. A document the well-known path reaches
// is bound to the server by the URL construction; one an advertised pointer
// named is not, so it must still say it belongs to that server. An omitted
// field is not a failure: the caller then derives the indicator from the
// server URL.
func (m *protectedResourceMetadata) dropForeignResource(serverURL string) {
	if m == nil || m.Resource == "" {
		return
	}
	if err := pkgoauth.ValidateAdvertisedResource(m.Resource, serverURL); err != nil {
		slog.Warn("Dropping the resource declared by advertised MCP server metadata",
			"server_url", serverURL,
			"error", err,
		)
		m.Resource = ""
	}
}

// fetchWellKnownResourceMetadata fetches the server's own RFC 9728 document
// from the well-known paths, the path-inserted form first. A document these
// URLs reach is bound to the server by the URL construction, so what it
// declares needs no further origin check. Returns nil when no path serves a
// readable document.
func fetchWellKnownResourceMetadata(ctx context.Context, httpClient *http.Client, serverURL string) *protectedResourceMetadata {
	candidates, err := pkgoauth.ProtectedResourceMetadataURLs(serverURL)
	if err != nil {
		slog.Debug("Cannot build RFC 9728 well-known URLs",
			"server_url", serverURL,
			"error", err,
		)
		return nil
	}
	for _, candidate := range candidates {
		if metadata := fetchResourceMetadata(ctx, httpClient, candidate); metadata != nil {
			return metadata
		}
	}
	return nil
}

// fetchResourceMetadata fetches an RFC 9728 protected resource metadata
// document. Returns nil when the document cannot be read or parsed.
func fetchResourceMetadata(ctx context.Context, httpClient *http.Client, metadataURL string) *protectedResourceMetadata {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil
	}

	var meta protectedResourceMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil
	}
	return &meta
}
