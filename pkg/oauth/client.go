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

// WithLogger sets a custom logger.
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
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
func (c *Client) doDiscoverMetadata(ctx context.Context, issuer string) (*Metadata, error) {
	// Try RFC 8414 first
	wellKnownURL := issuer + "/.well-known/oauth-authorization-server"
	metadata, err := c.fetchMetadata(ctx, wellKnownURL)
	if err == nil {
		c.cacheMetadata(issuer, metadata)
		return metadata, nil
	}

	c.logger.Debug("RFC 8414 metadata fetch failed, trying OIDC",
		"issuer", issuer,
		"error", err)

	// Fall back to OpenID Connect discovery
	wellKnownURL = issuer + "/.well-known/openid-configuration"
	metadata, err = c.fetchMetadata(ctx, wellKnownURL)
	if err == nil {
		c.cacheMetadata(issuer, metadata)
		return metadata, nil
	}

	return nil, fmt.Errorf("failed to discover OAuth metadata for %s: %w", issuer, err)
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
	defer resp.Body.Close()

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
func (c *Client) ExchangeCode(ctx context.Context, tokenEndpoint, code, redirectURI, clientID, codeVerifier string) (*Token, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {codeVerifier},
	}

	return c.doTokenRequest(ctx, tokenEndpoint, data)
}

// RefreshToken obtains a new access token using a refresh token.
func (c *Client) RefreshToken(ctx context.Context, tokenEndpoint, refreshToken, clientID string) (*Token, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}

	return c.doTokenRequest(ctx, tokenEndpoint, data)
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
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.Debug("Token request failed",
			"status", resp.StatusCode,
			"body", string(body))
		return nil, fmt.Errorf("token request failed with status %d", resp.StatusCode)
	}

	var token Token
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	// Calculate expiration if not set
	token.SetExpiresAtFromExpiresIn()

	return &token, nil
}

// BuildAuthorizationURL constructs an OAuth authorization URL.
func (c *Client) BuildAuthorizationURL(authEndpoint, clientID, redirectURI, state, scope string, pkce *PKCEChallenge) (string, error) {
	authURL, err := url.Parse(authEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid authorization endpoint: %w", err)
	}

	query := authURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", state)

	if scope != "" {
		query.Set("scope", scope)
	}

	if pkce != nil {
		query.Set("code_challenge", pkce.CodeChallenge)
		query.Set("code_challenge_method", pkce.CodeChallengeMethod)
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
