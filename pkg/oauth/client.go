package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// DefaultHTTPTimeout is the default timeout for HTTP requests.
	DefaultHTTPTimeout = 30 * time.Second

	// DefaultMetadataCacheTTL is the default TTL for cached OAuth metadata.
	DefaultMetadataCacheTTL = 30 * time.Minute
)

// metadataCacheEntry holds cached OAuth metadata with its timestamp.
type metadataCacheEntry struct {
	metadata  *Metadata
	fetchedAt time.Time
}

// Client handles OAuth 2.1 protocol operations.
// It provides metadata discovery, token exchange, and token refresh.
type Client struct {
	httpClient *http.Client
	logger     *slog.Logger

	// Metadata cache with mutex for thread safety
	metadataMu    sync.RWMutex
	metadataCache map[string]*metadataCacheEntry
	metadataTTL   time.Duration

	// singleflight group to deduplicate concurrent metadata fetches
	metadataGroup singleflight.Group
}

// ClientOption configures the OAuth client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithMetadataCacheTTL sets the metadata cache TTL.
func WithMetadataCacheTTL(ttl time.Duration) ClientOption {
	return func(c *Client) {
		c.metadataTTL = ttl
	}
}

// NewClient creates a new OAuth client.
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		httpClient:    &http.Client{Timeout: DefaultHTTPTimeout},
		logger:        slog.Default(),
		metadataCache: make(map[string]*metadataCacheEntry),
		metadataTTL:   DefaultMetadataCacheTTL,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// DiscoverMetadata fetches OAuth metadata from the issuer's well-known endpoint.
// It tries RFC 8414 (/.well-known/oauth-authorization-server) first,
// then falls back to OpenID Connect (/.well-known/openid-configuration).
//
// Results are cached with a TTL to reduce network requests.
func (c *Client) DiscoverMetadata(ctx context.Context, issuer string) (*Metadata, error) {
	issuer = strings.TrimSuffix(issuer, "/")

	// Check cache first with read lock
	c.metadataMu.RLock()
	if entry, ok := c.metadataCache[issuer]; ok {
		if time.Since(entry.fetchedAt) < c.metadataTTL {
			c.metadataMu.RUnlock()
			return entry.metadata, nil
		}
	}
	c.metadataMu.RUnlock()

	// Use singleflight to deduplicate concurrent fetches
	result, err, _ := c.metadataGroup.Do(issuer, func() (interface{}, error) {
		// Double-check cache after acquiring singleflight lock
		c.metadataMu.RLock()
		if entry, ok := c.metadataCache[issuer]; ok {
			if time.Since(entry.fetchedAt) < c.metadataTTL {
				c.metadataMu.RUnlock()
				return entry.metadata, nil
			}
		}
		c.metadataMu.RUnlock()

		return c.doDiscoverMetadata(ctx, issuer)
	})

	if err != nil {
		return nil, err
	}

	return result.(*Metadata), nil
}

// doDiscoverMetadata performs the actual HTTP fetch for OAuth metadata.
//
// For issuers without a path component the spec orders are RFC 8414 first
// (.well-known/oauth-authorization-server), then OIDC Discovery 1.0
// (.well-known/openid-configuration). For issuers with a path component
// (e.g. https://login.microsoftonline.com/<tenant>/v2.0), MCP 2025-11-25
// §"Authorization Server Metadata Discovery" mandates three URL forms in
// order: RFC 8414 path-insertion, OIDC path-insertion, OIDC append.
func (c *Client) doDiscoverMetadata(ctx context.Context, issuer string) (*Metadata, error) {
	parsed, err := url.Parse(issuer)
	if err != nil {
		return nil, fmt.Errorf("invalid issuer %q: %w", issuer, err)
	}
	host := parsed.Scheme + "://" + parsed.Host
	path := strings.TrimRight(parsed.Path, "/")

	var candidates []string
	if path == "" {
		candidates = []string{
			issuer + WellKnownAuthorizationServer,
			issuer + WellKnownOpenIDConfiguration,
		}
	} else {
		// Path-bearing issuer — three spec-mandated forms.
		candidates = []string{
			host + WellKnownAuthorizationServer + path,
			host + WellKnownOpenIDConfiguration + path,
			host + path + WellKnownOpenIDConfiguration,
		}
	}

	var lastErr, identityErr error
	for _, wellKnownURL := range candidates {
		metadata, err := c.fetchMetadata(ctx, wellKnownURL)
		if err == nil {
			// RFC 8414 §3.3: the issuer in the document must equal the
			// issuer the document was fetched for. Without this check a
			// server can name any issuer it likes, and every later
			// comparison against metadata.Issuer (the RFC 9207 `iss`
			// check, audience binding) inherits that claim.
			if err := verifyIssuerIdentity(issuer, metadata.Issuer); err != nil {
				c.logger.Warn("AS metadata rejected: issuer identity mismatch",
					"issuer", issuer,
					"url", wellKnownURL,
					"error", err)
				if identityErr == nil {
					identityErr = err
				}
				continue
			}
			c.cacheMetadata(issuer, metadata)
			return metadata, nil
		}
		c.logger.Debug("AS metadata fetch failed, trying next form",
			"issuer", issuer,
			"url", wellKnownURL,
			"error", err)
		lastErr = err
	}
	// A served document that fails the identity check is the more specific
	// diagnosis than a 404 on a well-known form the server does not use, so
	// it wins when both happened.
	if identityErr != nil {
		return nil, identityErr
	}
	return nil, fmt.Errorf("failed to discover OAuth metadata for %s: %w", issuer, lastErr)
}

// verifyIssuerIdentity applies the RFC 8414 §3.3 self-verification: the
// `issuer` value in the metadata document must identify the authorization
// server the document was retrieved for. A trailing slash on either side is
// not a difference; nothing else is normalized.
//
// The trailing slash is tolerated here and not on the RFC 9207 `iss`
// comparison because the two sides differ in origin. The requested issuer is
// an operator-configured string, and an operator who writes the trailing
// slash means the same server. The `iss` value comes from the authorization
// server itself, which must send the identifier it publishes.
func verifyIssuerIdentity(requested, advertised string) error {
	if advertised == "" {
		return fmt.Errorf("AS metadata for %q carries no issuer (RFC 8414 §3.3)", requested)
	}
	if strings.TrimSuffix(advertised, "/") != strings.TrimSuffix(requested, "/") {
		return fmt.Errorf("AS metadata issuer mismatch: fetched for %q but the document reports %q (RFC 8414 §3.3)", requested, advertised)
	}
	return nil
}

// fetchMetadata fetches metadata from a specific URL.
func (c *Client) fetchMetadata(ctx context.Context, metadataURL string) (*Metadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metadata request failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var metadata Metadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	return &metadata, nil
}

// cacheMetadata stores metadata in the cache.
func (c *Client) cacheMetadata(issuer string, metadata *Metadata) {
	c.metadataMu.Lock()
	c.metadataCache[issuer] = &metadataCacheEntry{
		metadata:  metadata,
		fetchedAt: time.Now(),
	}
	c.metadataMu.Unlock()

	c.logger.Debug("Cached OAuth metadata",
		"issuer", issuer,
		"authorization_endpoint", metadata.AuthorizationEndpoint,
		"token_endpoint", metadata.TokenEndpoint)
}

// ExchangeCode exchanges an authorization code for tokens.
//
// clientSecret is empty for public clients (CIMD, or DCR registrations with
// token_endpoint_auth_method "none"); when set it is sent as
// client_secret_post, which every AS that issues secrets via DCR accepts.
//
// resource is the canonical URI of the MCP server the token is for. MCP
// 2026-07-28 requires the RFC 8707 `resource` parameter on the token request
// regardless of whether the authorization server supports it, so it is sent
// whenever the caller supplies one.
func (c *Client) ExchangeCode(ctx context.Context, tokenEndpoint, code, redirectURI, clientID, clientSecret, codeVerifier, resource string) (*Token, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {codeVerifier},
	}
	if clientSecret != "" {
		data.Set(FormFieldClientSecret, clientSecret)
	}
	if resource != "" {
		data.Set("resource", resource)
	}

	return c.doTokenRequest(ctx, tokenEndpoint, data)
}

// RegisterClient performs OAuth 2.0 Dynamic Client Registration (RFC 7591)
// against the given registration endpoint. The metadata must not carry a
// client_id — the authorization server assigns one in the response.
func (c *Client) RegisterClient(ctx context.Context, registrationEndpoint string, metadata *ClientMetadata) (*ClientRegistrationResponse, error) {
	body, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal client metadata: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to create registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registration request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read registration response: %w", err)
	}

	// RFC 7591 §3.2.1 specifies 201 Created; accept any 2xx for
	// interoperability with servers that answer 200.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// RFC 7591 §3.2.2 error response.
		var regErr struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if err := json.Unmarshal(respBody, &regErr); err == nil && regErr.Error != "" {
			if regErr.ErrorDescription != "" {
				return nil, fmt.Errorf("client registration failed: %s - %s", regErr.Error, regErr.ErrorDescription)
			}
			return nil, fmt.Errorf("client registration failed: %s", regErr.Error)
		}
		return nil, fmt.Errorf("client registration failed with status %d", resp.StatusCode)
	}

	var registration ClientRegistrationResponse
	if err := json.Unmarshal(respBody, &registration); err != nil {
		return nil, fmt.Errorf("failed to parse registration response: %w", err)
	}
	if registration.ClientID == "" {
		return nil, fmt.Errorf("registration response is missing client_id")
	}

	return &registration, nil
}

// doTokenRequest performs a token endpoint request.
func (c *Client) doTokenRequest(ctx context.Context, tokenEndpoint string, data url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.Debug("Token request failed",
			"status", resp.StatusCode,
			"body", string(body))

		// The RFC 6749 §5.2 error object, when present, is preserved on the
		// returned error so callers can act on the code: invalid_client means
		// the client identification itself is no longer accepted, which is
		// different from a rejected grant.
		tokenErr := &TokenEndpointError{StatusCode: resp.StatusCode}
		var oauthErr struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if err := json.Unmarshal(body, &oauthErr); err == nil {
			tokenErr.Code = oauthErr.Error
			tokenErr.Description = oauthErr.ErrorDescription
		}
		return nil, tokenErr
	}

	var token Token
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	// Calculate expiration if not set
	token.SetExpiresAtFromExpiresIn()

	return &token, nil
}

// AuthorizationRequest carries the parameters of an OAuth authorization
// request. The fields are grouped in a struct because several of them are
// URLs that are easy to transpose in a positional argument list.
type AuthorizationRequest struct {
	// AuthorizationEndpoint is the authorization endpoint of the AS.
	AuthorizationEndpoint string

	// ClientID identifies the client. Muster uses its CIMD URL.
	ClientID string

	// RedirectURI is where the AS returns the authorization response.
	RedirectURI string

	// State is the opaque CSRF value returned on the authorization response.
	State string

	// Scope is the space-separated scope list, optional.
	Scope string

	// Resource is the canonical URI of the MCP server the token is for
	// (RFC 8707). Empty omits the parameter.
	Resource string

	// PKCE holds the code challenge, optional.
	PKCE *PKCEChallenge
}

// BuildAuthorizationURL constructs an OAuth authorization URL.
//
// MCP 2026-07-28 requires the RFC 8707 `resource` parameter on the
// authorization request regardless of whether the authorization server
// supports it, so it is sent whenever the caller supplies one.
func (c *Client) BuildAuthorizationURL(request AuthorizationRequest) (string, error) {
	authURL, err := url.Parse(request.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid authorization endpoint: %w", err)
	}

	query := authURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", request.ClientID)
	query.Set("redirect_uri", request.RedirectURI)
	query.Set("state", request.State)

	if request.Scope != "" {
		query.Set("scope", request.Scope)
	}

	if request.Resource != "" {
		query.Set("resource", request.Resource)
	}

	if request.PKCE != nil {
		query.Set("code_challenge", request.PKCE.CodeChallenge)
		query.Set("code_challenge_method", request.PKCE.CodeChallengeMethod)
	}

	authURL.RawQuery = query.Encode()
	return authURL.String(), nil
}

// ClearMetadataCache clears the metadata cache.
// Useful for testing or when metadata needs to be refreshed immediately.
func (c *Client) ClearMetadataCache() {
	c.metadataMu.Lock()
	c.metadataCache = make(map[string]*metadataCacheEntry)
	c.metadataMu.Unlock()
}
