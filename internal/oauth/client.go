package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"muster/pkg/logging"
)

// metadataCacheTTL is the time-to-live for cached OAuth metadata.
// After this duration, metadata will be re-fetched from the issuer.
// A 30-minute TTL balances caching efficiency with timely key rotation updates.
const metadataCacheTTL = 30 * time.Minute

// metadataCacheEntry holds cached OAuth metadata with its timestamp.
type metadataCacheEntry struct {
	metadata  *OAuthMetadata
	fetchedAt time.Time
}

// Client handles OAuth 2.1 flows for remote MCP server authentication.
type Client struct {
	// Configuration
	clientID     string // The CIMD URL used as client_id
	publicURL    string // The public URL of the Muster Server
	callbackPath string // The path for OAuth callbacks (e.g., "/oauth/callback")

	// Stores
	tokenStore *TokenStore
	stateStore *StateStore

	// HTTP client for token exchange
	httpClient *http.Client

	// Metadata cache (issuer URL -> metadata entry) with mutex for thread safety
	metadataMu    sync.RWMutex
	metadataCache map[string]*metadataCacheEntry

	// singleflight group to deduplicate concurrent metadata fetches
	metadataGroup singleflight.Group
}

// NewClient creates a new OAuth client with the given configuration.
func NewClient(clientID, publicURL, callbackPath string) *Client {
	return &Client{
		clientID:      clientID,
		publicURL:     publicURL,
		callbackPath:  callbackPath,
		tokenStore:    NewTokenStore(),
		stateStore:    NewStateStore(),
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		metadataCache: make(map[string]*metadataCacheEntry),
	}
}

// GetTokenStore returns the token store for external access.
func (c *Client) GetTokenStore() *TokenStore {
	return c.tokenStore
}

// GetStateStore returns the state store for external access.
func (c *Client) GetStateStore() *StateStore {
	return c.stateStore
}

// GetRedirectURI returns the full redirect URI for OAuth callbacks.
func (c *Client) GetRedirectURI() string {
	return strings.TrimSuffix(c.publicURL, "/") + c.callbackPath
}

// GetToken retrieves a valid token for the given session and issuer.
// Returns nil if no valid token exists.
func (c *Client) GetToken(sessionID, issuer, scope string) *Token {
	// First try exact match
	key := TokenKey{
		SessionID: sessionID,
		Issuer:    issuer,
		Scope:     scope,
	}
	if token := c.tokenStore.Get(key); token != nil {
		return token
	}

	// Fall back to issuer-only match for SSO
	return c.tokenStore.GetByIssuer(sessionID, issuer)
}

// GenerateAuthURL creates an OAuth authorization URL for user authentication.
// Returns the URL. The code verifier is stored with the state for later retrieval.
func (c *Client) GenerateAuthURL(ctx context.Context, sessionID, serverName, issuer, scope string) (string, error) {
	// Fetch OAuth metadata for the issuer
	metadata, err := c.fetchMetadata(ctx, issuer)
	if err != nil {
		return "", fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}

	// Generate PKCE code verifier and challenge
	codeVerifier, codeChallenge, err := generatePKCE()
	if err != nil {
		return "", fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// Generate state parameter (includes issuer and code verifier)
	state, err := c.stateStore.GenerateState(sessionID, serverName, issuer, codeVerifier)
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	// Build authorization URL
	authURL, err := url.Parse(metadata.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid authorization endpoint: %w", err)
	}

	query := authURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", c.clientID)
	query.Set("redirect_uri", c.GetRedirectURI())
	query.Set("state", state)
	query.Set("code_challenge", codeChallenge)
	query.Set("code_challenge_method", "S256")

	if scope != "" {
		query.Set("scope", scope)
	}

	authURL.RawQuery = query.Encode()

	logging.Debug("OAuth", "Generated auth URL for session=%s server=%s issuer=%s",
		sessionID, serverName, issuer)

	return authURL.String(), nil
}

// ExchangeCode exchanges an authorization code for tokens.
func (c *Client) ExchangeCode(ctx context.Context, code, codeVerifier, issuer string) (*Token, error) {
	// Fetch OAuth metadata
	metadata, err := c.fetchMetadata(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}

	// Prepare token request
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", c.GetRedirectURI())
	data.Set("client_id", c.clientID)
	data.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, "POST", metadata.TokenEndpoint, strings.NewReader(data.Encode()))
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
		// Log full response for debugging but don't expose in error message
		// Response body may contain sensitive information (error descriptions, hints)
		logging.Debug("OAuth", "Token exchange failed: status=%d body=%s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("token exchange failed with status %d", resp.StatusCode)
	}

	var token Token
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	// Set issuer and calculate expiration
	token.Issuer = issuer
	if token.ExpiresIn > 0 && token.ExpiresAt.IsZero() {
		token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}

	logging.Debug("OAuth", "Successfully exchanged code for token (issuer=%s, expires_in=%d)",
		issuer, token.ExpiresIn)

	return &token, nil
}

// RefreshToken refreshes an expired token using its refresh token.
func (c *Client) RefreshToken(ctx context.Context, token *Token) (*Token, error) {
	if token.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	metadata, err := c.fetchMetadata(ctx, token.Issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", token.RefreshToken)
	data.Set("client_id", c.clientID)

	req, err := http.NewRequestWithContext(ctx, "POST", metadata.TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Log full response for debugging but don't expose in error message
		// Response body may contain sensitive information (error descriptions, hints)
		logging.Debug("OAuth", "Token refresh failed: status=%d body=%s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("token refresh failed with status %d", resp.StatusCode)
	}

	var newToken Token
	if err := json.Unmarshal(body, &newToken); err != nil {
		return nil, fmt.Errorf("failed to parse refresh response: %w", err)
	}

	// Preserve issuer and calculate expiration
	newToken.Issuer = token.Issuer
	if newToken.ExpiresIn > 0 && newToken.ExpiresAt.IsZero() {
		newToken.ExpiresAt = time.Now().Add(time.Duration(newToken.ExpiresIn) * time.Second)
	}

	// Preserve refresh token if not returned
	if newToken.RefreshToken == "" {
		newToken.RefreshToken = token.RefreshToken
	}

	logging.Debug("OAuth", "Successfully refreshed token (issuer=%s, expires_in=%d)",
		token.Issuer, newToken.ExpiresIn)

	return &newToken, nil
}

// StoreToken stores a token in the token store.
func (c *Client) StoreToken(sessionID string, token *Token) {
	key := TokenKey{
		SessionID: sessionID,
		Issuer:    token.Issuer,
		Scope:     token.Scope,
	}
	c.tokenStore.Store(key, token)
}

// Stop stops background cleanup goroutines.
func (c *Client) Stop() {
	c.tokenStore.Stop()
	c.stateStore.Stop()
}

// fetchMetadata fetches OAuth metadata from the issuer's well-known endpoint.
// Uses singleflight to deduplicate concurrent requests for the same issuer.
func (c *Client) fetchMetadata(ctx context.Context, issuer string) (*OAuthMetadata, error) {
	// Check cache first with read lock
	c.metadataMu.RLock()
	if entry, ok := c.metadataCache[issuer]; ok {
		// Check if cache entry is still valid (not expired)
		if time.Since(entry.fetchedAt) < metadataCacheTTL {
			c.metadataMu.RUnlock()
			return entry.metadata, nil
		}
		// Cache expired, need to refresh
		logging.Debug("OAuth", "Metadata cache expired for issuer=%s, refreshing", issuer)
	}
	c.metadataMu.RUnlock()

	// Use singleflight to deduplicate concurrent fetches for the same issuer
	result, err, _ := c.metadataGroup.Do(issuer, func() (interface{}, error) {
		// Double-check cache after acquiring the singleflight lock
		c.metadataMu.RLock()
		if entry, ok := c.metadataCache[issuer]; ok {
			if time.Since(entry.fetchedAt) < metadataCacheTTL {
				c.metadataMu.RUnlock()
				return entry.metadata, nil
			}
		}
		c.metadataMu.RUnlock()

		return c.doFetchMetadata(ctx, issuer)
	})

	if err != nil {
		return nil, err
	}

	return result.(*OAuthMetadata), nil
}

// doFetchMetadata performs the actual HTTP fetch for OAuth metadata.
func (c *Client) doFetchMetadata(ctx context.Context, issuer string) (*OAuthMetadata, error) {
	// Build well-known URL
	wellKnownURL := strings.TrimSuffix(issuer, "/") + "/.well-known/oauth-authorization-server"

	req, err := http.NewRequestWithContext(ctx, "GET", wellKnownURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try OpenID Connect discovery endpoint as fallback
		wellKnownURL = strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
		req, err = http.NewRequestWithContext(ctx, "GET", wellKnownURL, nil)
		if err != nil {
			return nil, err
		}

		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to fetch OAuth metadata: status=%d", resp.StatusCode)
		}
	}

	var metadata OAuthMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to parse OAuth metadata: %w", err)
	}

	// Cache the metadata with write lock (includes timestamp for TTL)
	c.metadataMu.Lock()
	c.metadataCache[issuer] = &metadataCacheEntry{
		metadata:  &metadata,
		fetchedAt: time.Now(),
	}
	c.metadataMu.Unlock()

	logging.Debug("OAuth", "Fetched OAuth metadata for issuer=%s (auth=%s, token=%s)",
		issuer, metadata.AuthorizationEndpoint, metadata.TokenEndpoint)

	return &metadata, nil
}

// generatePKCE generates a PKCE code verifier and challenge.
func generatePKCE() (verifier, challenge string, err error) {
	// Generate 32 random bytes for the verifier
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return "", "", err
	}

	verifier = base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Generate S256 challenge
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])

	return verifier, challenge, nil
}
