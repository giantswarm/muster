package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// ErrAuthRequired is returned when OAuth authentication is required.
var ErrAuthRequired = errors.New("authentication required")

// DefaultHTTPTimeout is the default timeout for HTTP requests.
const DefaultHTTPTimeout = 30 * time.Second

// OAuthMetadata represents OAuth/OIDC server metadata.
// This is discovered from .well-known endpoints.
type OAuthMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	UserinfoEndpoint              string   `json:"userinfo_endpoint,omitempty"`
	JwksURI                       string   `json:"jwks_uri,omitempty"`
	ScopesSupported               []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported        []string `json:"response_types_supported,omitempty"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
}

// AuthFlow represents an in-progress OAuth authorization flow.
type AuthFlow struct {
	// ServerURL is the URL of the Muster server we're authenticating to.
	ServerURL string

	// IssuerURL is the OAuth issuer URL.
	IssuerURL string

	// PKCE holds the PKCE challenge parameters.
	PKCE *PKCEChallenge

	// State is the OAuth state parameter.
	State string

	// CallbackServer is the local HTTP server waiting for the callback.
	CallbackServer *CallbackServer

	// Metadata is the discovered OAuth metadata.
	Metadata *OAuthMetadata

	// StartedAt is when the flow was initiated.
	StartedAt time.Time
}

// Client is the OAuth client for the Muster Agent.
// It manages OAuth authentication flows for connecting to protected Muster servers.
type Client struct {
	mu            sync.RWMutex
	tokenStore    *TokenStore
	httpClient    *http.Client
	callbackPort  int
	currentFlow   *AuthFlow
	metadataCache map[string]*OAuthMetadata
}

// ClientConfig configures the OAuth client.
type ClientConfig struct {
	// CallbackPort is the port for the local OAuth callback server.
	// Defaults to 3000 if not specified.
	CallbackPort int

	// TokenStoreConfig configures token storage.
	TokenStoreConfig TokenStoreConfig

	// HTTPClient is an optional custom HTTP client.
	HTTPClient *http.Client
}

// NewClient creates a new OAuth client with the specified configuration.
func NewClient(cfg ClientConfig) (*Client, error) {
	tokenStore, err := NewTokenStore(cfg.TokenStoreConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create token store: %w", err)
	}

	callbackPort := cfg.CallbackPort
	if callbackPort == 0 {
		callbackPort = DefaultCallbackPort
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: DefaultHTTPTimeout,
		}
	}

	return &Client{
		tokenStore:    tokenStore,
		httpClient:    httpClient,
		callbackPort:  callbackPort,
		metadataCache: make(map[string]*OAuthMetadata),
	}, nil
}

// GetToken retrieves a valid OAuth token for the specified server.
// Returns ErrAuthRequired if no valid token exists and authentication is needed.
func (c *Client) GetToken(serverURL string) (*oauth2.Token, error) {
	storedToken := c.tokenStore.GetToken(serverURL)
	if storedToken == nil {
		return nil, ErrAuthRequired
	}

	return storedToken.ToOAuth2Token(), nil
}

// HasValidToken checks if a valid token exists for the specified server.
func (c *Client) HasValidToken(serverURL string) bool {
	return c.tokenStore.HasValidToken(serverURL)
}

// StartAuthFlow initiates an OAuth authorization flow for the specified server.
// Returns the authorization URL that the user should open in their browser.
//
// The flow uses Authorization Code Grant with PKCE for maximum security.
// A local callback server is started to receive the OAuth callback.
func (c *Client) StartAuthFlow(ctx context.Context, serverURL, issuerURL string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Cancel any existing flow
	c.cancelCurrentFlow()

	// Discover OAuth metadata
	metadata, err := c.discoverOAuthMetadata(ctx, issuerURL)
	if err != nil {
		return "", fmt.Errorf("failed to discover OAuth metadata: %w", err)
	}

	// Generate PKCE challenge
	pkce, err := GeneratePKCE()
	if err != nil {
		return "", fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// Generate state
	state, err := GenerateState()
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	// Start callback server
	callbackServer := NewCallbackServer(c.callbackPort)
	redirectURI, err := callbackServer.Start(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to start callback server: %w", err)
	}

	// Store the flow
	c.currentFlow = &AuthFlow{
		ServerURL:      serverURL,
		IssuerURL:      issuerURL,
		PKCE:           pkce,
		State:          state,
		CallbackServer: callbackServer,
		Metadata:       metadata,
		StartedAt:      time.Now(),
	}

	// Build authorization URL
	authURL, err := c.buildAuthorizationURL(metadata, redirectURI, state, pkce)
	if err != nil {
		c.cancelCurrentFlow()
		return "", fmt.Errorf("failed to build authorization URL: %w", err)
	}

	return authURL, nil
}

// WaitForCallback waits for the OAuth callback and exchanges the code for tokens.
// This should be called after StartAuthFlow and after the user has authenticated.
func (c *Client) WaitForCallback(ctx context.Context) (*oauth2.Token, error) {
	c.mu.RLock()
	flow := c.currentFlow
	c.mu.RUnlock()

	if flow == nil {
		return nil, errors.New("no auth flow in progress")
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, CallbackTimeout)
	defer cancel()

	// Wait for callback
	result, err := flow.CallbackServer.WaitForCallback(timeoutCtx)
	if err != nil {
		c.mu.Lock()
		c.cancelCurrentFlow()
		c.mu.Unlock()
		return nil, fmt.Errorf("callback failed: %w", err)
	}

	// Verify state
	if result.State != flow.State {
		c.mu.Lock()
		c.cancelCurrentFlow()
		c.mu.Unlock()
		return nil, errors.New("state mismatch - possible CSRF attack")
	}

	// Check for error
	if result.IsError() {
		c.mu.Lock()
		c.cancelCurrentFlow()
		c.mu.Unlock()
		if result.ErrorDescription != "" {
			return nil, fmt.Errorf("authorization failed: %s - %s", result.Error, result.ErrorDescription)
		}
		return nil, fmt.Errorf("authorization failed: %s", result.Error)
	}

	// Exchange code for tokens
	token, err := c.exchangeCode(ctx, flow, result.Code)
	if err != nil {
		c.mu.Lock()
		c.cancelCurrentFlow()
		c.mu.Unlock()
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	// Store token
	if err := c.tokenStore.StoreToken(flow.ServerURL, flow.IssuerURL, token); err != nil {
		// Log warning but continue - token is still valid for this session
		slog.Warn("failed to persist token", "error", err)
	}

	// Clean up flow
	c.mu.Lock()
	c.cancelCurrentFlow()
	c.mu.Unlock()

	return token, nil
}

// CompleteAuthFlow is a convenience method that combines StartAuthFlow and WaitForCallback.
// It returns the authorization URL and a callback function to wait for completion.
func (c *Client) CompleteAuthFlow(ctx context.Context, serverURL, issuerURL string) (authURL string, waitFn func() (*oauth2.Token, error), err error) {
	authURL, err = c.StartAuthFlow(ctx, serverURL, issuerURL)
	if err != nil {
		return "", nil, err
	}

	waitFn = func() (*oauth2.Token, error) {
		return c.WaitForCallback(ctx)
	}

	return authURL, waitFn, nil
}

// cancelCurrentFlow cancels and cleans up the current auth flow.
// Must be called with c.mu held.
func (c *Client) cancelCurrentFlow() {
	if c.currentFlow != nil {
		if c.currentFlow.CallbackServer != nil {
			c.currentFlow.CallbackServer.Stop()
		}
		c.currentFlow = nil
	}
}

// discoverOAuthMetadata fetches OAuth metadata from the issuer.
// Tries RFC 8414 first, then falls back to OpenID Connect discovery.
func (c *Client) discoverOAuthMetadata(ctx context.Context, issuerURL string) (*OAuthMetadata, error) {
	// Check cache first
	if metadata, ok := c.metadataCache[issuerURL]; ok {
		return metadata, nil
	}

	// Remove trailing slash from issuer URL
	issuerURL = strings.TrimSuffix(issuerURL, "/")

	// Try RFC 8414 first
	metadata, err := c.fetchMetadata(ctx, issuerURL+"/.well-known/oauth-authorization-server")
	if err == nil {
		c.metadataCache[issuerURL] = metadata
		return metadata, nil
	}

	// Fall back to OpenID Connect discovery
	metadata, err = c.fetchMetadata(ctx, issuerURL+"/.well-known/openid-configuration")
	if err == nil {
		c.metadataCache[issuerURL] = metadata
		return metadata, nil
	}

	return nil, fmt.Errorf("failed to discover OAuth metadata for %s", issuerURL)
}

// fetchMetadata fetches OAuth metadata from a URL.
func (c *Client) fetchMetadata(ctx context.Context, metadataURL string) (*OAuthMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, err
	}

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

	var metadata OAuthMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

// DefaultAgentClientID is the CIMD URL for the Muster Agent.
// This is hosted on GitHub Pages and serves as the client_id for OAuth.
const DefaultAgentClientID = "https://giantswarm.github.io/muster/muster-agent.json"

// buildAuthorizationURL constructs the OAuth authorization URL.
func (c *Client) buildAuthorizationURL(metadata *OAuthMetadata, redirectURI, state string, pkce *PKCEChallenge) (string, error) {
	authURL, err := url.Parse(metadata.AuthorizationEndpoint)
	if err != nil {
		return "", err
	}

	params := url.Values{
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {pkce.CodeChallenge},
		"code_challenge_method": {pkce.CodeChallengeMethod},
		"scope":                 {"openid profile email offline_access"},
	}

	// Use the CIMD URL as the client_id per MCP OAuth 2.1 spec
	params.Set("client_id", DefaultAgentClientID)

	authURL.RawQuery = params.Encode()
	return authURL.String(), nil
}

// exchangeCode exchanges an authorization code for tokens.
func (c *Client) exchangeCode(ctx context.Context, flow *AuthFlow, code string) (*oauth2.Token, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {flow.CallbackServer.GetRedirectURI()},
		"code_verifier": {flow.PKCE.CodeVerifier},
		"client_id":     {DefaultAgentClientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, flow.Metadata.TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		Scope        string `json:"scope"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}

	token := &oauth2.Token{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		RefreshToken: tokenResp.RefreshToken,
	}

	if tokenResp.ExpiresIn > 0 {
		token.Expiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	// Store ID token in extra data
	if tokenResp.IDToken != "" {
		token = token.WithExtra(map[string]interface{}{
			"id_token": tokenResp.IDToken,
		})
	}

	return token, nil
}

// ClearToken removes the stored token for a server.
func (c *Client) ClearToken(serverURL string) error {
	return c.tokenStore.DeleteToken(serverURL)
}

// Close cleans up the OAuth client resources.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cancelCurrentFlow()
	return nil
}

// IsFlowInProgress returns true if an auth flow is currently in progress.
func (c *Client) IsFlowInProgress() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentFlow != nil
}

// GetCurrentFlowServerURL returns the server URL of the current auth flow, if any.
func (c *Client) GetCurrentFlowServerURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.currentFlow != nil {
		return c.currentFlow.ServerURL
	}
	return ""
}
