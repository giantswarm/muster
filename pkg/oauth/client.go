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
	// pinned entries were supplied by the operator for an authorization
	// server without a discovery document; they never expire and survive
	// ClearMetadataCache.
	pinned bool
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
// Results are cached with a TTL to reduce network requests. Metadata pinned
// for the issuer (PinMetadata) answers instead of the network.
func (c *Client) DiscoverMetadata(ctx context.Context, issuer string) (*Metadata, error) {
	return c.discoverMetadata(ctx, issuer, true)
}

// DiscoverPublishedMetadata is DiscoverMetadata without the pins: it answers
// only with what the authorization server publishes itself. A caller whose
// pins are filed under another key (PinnedMetadata) uses it, so a pin made
// for the plain issuer never stands in for that caller's discovery.
func (c *Client) DiscoverPublishedMetadata(ctx context.Context, issuer string) (*Metadata, error) {
	return c.discoverMetadata(ctx, issuer, false)
}

// PinnedMetadata returns the metadata pinned under key (PinMetadata), or nil.
func (c *Client) PinnedMetadata(key string) *Metadata {
	key = strings.TrimSuffix(key, "/")
	c.metadataMu.RLock()
	defer c.metadataMu.RUnlock()
	if entry, ok := c.metadataCache[key]; ok && entry.pinned {
		return entry.metadata
	}
	return nil
}

// publishedMetadataKeyPrefix starts the cache key of metadata discovered for
// DiscoverPublishedMetadata. No issuer URL starts with it.
const publishedMetadataKeyPrefix = "published|"

func (c *Client) discoverMetadata(ctx context.Context, issuer string, usePinned bool) (*Metadata, error) {
	issuer = strings.TrimSuffix(issuer, "/")

	// Published metadata wanted next to a pin of the same issuer is cached
	// under its own key, which no pin uses: the pin cannot hide it, and it
	// cannot replace the pin.
	cacheKey := issuer
	if !usePinned {
		cacheKey = publishedMetadataKeyPrefix + issuer
	}

	cached := func() *Metadata {
		c.metadataMu.RLock()
		defer c.metadataMu.RUnlock()
		entry, ok := c.metadataCache[cacheKey]
		if !ok {
			return nil
		}
		if entry.pinned {
			if usePinned {
				return entry.metadata
			}
			return nil
		}
		if time.Since(entry.fetchedAt) < c.metadataTTL {
			return entry.metadata
		}
		return nil
	}

	// Check cache first with read lock
	if metadata := cached(); metadata != nil {
		return metadata, nil
	}

	// Use singleflight to deduplicate concurrent fetches. A flight that may
	// answer from a pin is not shared with one that may not.
	result, err, _ := c.metadataGroup.Do(cacheKey, func() (interface{}, error) {
		// Double-check cache after acquiring singleflight lock
		if metadata := cached(); metadata != nil {
			return metadata, nil
		}
		metadata, err := c.doDiscoverMetadata(ctx, issuer)
		if err != nil {
			return nil, err
		}
		c.cacheMetadata(cacheKey, metadata)
		return metadata, nil
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
// PinMetadata supplies the metadata of an authorization server that publishes
// no RFC 8414 / OIDC discovery document (GitHub). DiscoverMetadata answers
// from the pin from then on, without a network request, until PinMetadata is
// called again for the issuer. When the pinned document lists no
// code_challenge_methods_supported, S256 is assumed: the operator vouching
// for the endpoints is the only source of that fact, and MCP's refusal rule
// exists for servers that could have advertised it.
func (c *Client) PinMetadata(issuer string, metadata *Metadata) {
	issuer = strings.TrimSuffix(issuer, "/")
	pinned := *metadata
	if pinned.Issuer == "" {
		pinned.Issuer = issuer
	}
	if len(pinned.CodeChallengeMethodsSupported) == 0 {
		pinned.CodeChallengeMethodsSupported = []string{"S256"}
	}

	c.metadataMu.Lock()
	c.metadataCache[issuer] = &metadataCacheEntry{
		metadata:  &pinned,
		fetchedAt: time.Now(),
		pinned:    true,
	}
	c.metadataMu.Unlock()

	c.logger.Info("Pinned OAuth metadata",
		"issuer", issuer,
		"authorization_endpoint", pinned.AuthorizationEndpoint,
		"token_endpoint", pinned.TokenEndpoint)
}

func (c *Client) cacheMetadata(issuer string, metadata *Metadata) {
	c.metadataMu.Lock()
	if entry, ok := c.metadataCache[issuer]; ok && entry.pinned {
		// An operator pin is not overwritten by a discovery result.
		c.metadataMu.Unlock()
		return
	}
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

// RefreshToken redeems a refresh token at the token endpoint (RFC 6749 §6)
// with the client identification the grant was issued under; clientSecret is
// empty for public clients. The answer is the authorization server's new
// token set. An authorization server that rotates refresh tokens (GitHub)
// answers with a new refresh_token and invalidates the old one; one that does
// not rotate answers without a refresh_token, and the caller keeps the one it
// has. A rejected refresh token comes back as a TokenEndpointError whose Code
// names the reason (invalid_grant, or GitHub's bad_refresh_token), see
// IsRefreshTokenRejected.
func (c *Client) RefreshToken(ctx context.Context, tokenEndpoint, refreshToken, clientID, clientSecret string) (*Token, error) {
	data := url.Values{
		"grant_type":          {GrantTypeRefreshToken},
		FormFieldRefreshToken: {refreshToken},
		"client_id":           {clientID},
	}
	if clientSecret != "" {
		data.Set(FormFieldClientSecret, clientSecret)
	}
	return c.doTokenRequest(ctx, tokenEndpoint, data)
}

// RevokeToken asks the authorization server to revoke a token (RFC 7009),
// identified with the client the grant was issued under, the way RefreshToken
// identifies it; clientSecret is empty for public clients. tokenTypeHint is
// "refresh_token" or "access_token". RFC 7009 §2.2 answers 200 for a revoked
// and for an unknown token alike; any other answer is an error carrying the
// status only, since a body may echo the token.
func (c *Client) RevokeToken(ctx context.Context, revocationEndpoint, token, tokenTypeHint, clientID, clientSecret string) error {
	data := url.Values{
		"token":           {token},
		"token_type_hint": {tokenTypeHint},
		"client_id":       {clientID},
	}
	if clientSecret != "" {
		data.Set(FormFieldClientSecret, clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revocationEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create revocation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revocation request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revocation endpoint answered status %d", resp.StatusCode)
	}
	return nil
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

	// GitHub answers a rejected refresh token (and other grant errors) with
	// HTTP 200 and an RFC 6749 §5.2 error object in the body. A success body
	// never carries an error member, so its presence is the verdict.
	var bodyErr struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &bodyErr); err == nil && bodyErr.Error != "" {
		c.logger.Debug("Token request answered an error object with a success status",
			"status", resp.StatusCode,
			"error", bodyErr.Error)
		return nil, &TokenEndpointError{StatusCode: resp.StatusCode, Code: bodyErr.Error, Description: bodyErr.ErrorDescription}
	}

	var token Token
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	// Calculate expiration if not set
	token.SetExpiresAtFromExpiresIn()
	token.setExpiryFromJWTAccessToken()

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

// UnpinMetadata drops the operator-pinned metadata for an issuer, so the
// next DiscoverMetadata fetches the authorization server's own document
// again. It reports whether a pinned entry was dropped. A discovered entry is
// left alone: it came from the authorization server itself and expires with
// the cache TTL.
//
// Pinned entries never expire, so without this a pin that an MCPServer no
// longer carries -- removed, or changed to a different authorization server --
// would keep answering every flow against the issuer until the process
// restarts (giantswarm/muster#1175).
func (c *Client) UnpinMetadata(issuer string) bool {
	issuer = strings.TrimSuffix(issuer, "/")
	c.metadataMu.Lock()
	entry, ok := c.metadataCache[issuer]
	if ok && entry.pinned {
		delete(c.metadataCache, issuer)
	}
	c.metadataMu.Unlock()
	if !ok || !entry.pinned {
		return false
	}
	c.logger.Info("Dropped pinned OAuth metadata",
		"issuer", issuer,
		"authorization_endpoint", entry.metadata.AuthorizationEndpoint,
		"token_endpoint", entry.metadata.TokenEndpoint)
	return true
}

// ClearMetadataCache clears the metadata cache.
// Useful for testing or when metadata needs to be refreshed immediately.
func (c *Client) ClearMetadataCache() {
	c.metadataMu.Lock()
	kept := make(map[string]*metadataCacheEntry)
	for issuer, entry := range c.metadataCache {
		if entry.pinned {
			kept[issuer] = entry
		}
	}
	c.metadataCache = kept
	c.metadataMu.Unlock()
}
