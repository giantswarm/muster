package oauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	mcpoauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/providers"
	"golang.org/x/oauth2"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// ErrAuthRequired is returned when OAuth authentication is required.
var ErrAuthRequired = errors.New("authentication required")

// agentOAuthScopes are the OAuth scopes requested for agent CLI authentication.
// The "groups" scope is included to ensure group claims are available in ID tokens
// for RBAC decisions in downstream services like mcp-kubernetes.
var agentOAuthScopes = []string{"openid", "profile", "email", "groups", "offline_access"}

// DefaultHTTPTimeout is the default timeout for HTTP requests.
const DefaultHTTPTimeout = 30 * time.Second

// SilentAuthTimeout is the timeout for silent re-authentication attempts.
// This is shorter than the interactive timeout since silent auth should complete
// within seconds (it's just a browser redirect, no user interaction).
const SilentAuthTimeout = 15 * time.Second

// MetadataCacheTTL is the TTL for cached OAuth metadata.
// This allows the cache to refresh periodically in case server configuration changes.
const MetadataCacheTTL = 1 * time.Hour

// OAuthMetadata is an alias for pkgoauth.Metadata for use in the agent.
type OAuthMetadata = pkgoauth.Metadata

// AuthFlowOptions configures the authorization flow behavior.
// These options control OIDC parameters per OpenID Connect Core 1.0 Section 3.1.2.1.
type AuthFlowOptions struct {
	// Silent enables prompt=none for silent re-authentication.
	// The IdP will not show any UI; if the user doesn't have an active session,
	// an error is returned instead of showing a login page.
	Silent bool

	// LoginHint pre-fills the email/username field at the IdP.
	// Useful for re-authentication when the user's identity is already known.
	LoginHint string

	// IDTokenHint is a previously issued ID token as a hint about the user's session.
	// Used with Silent=true to identify the user for silent re-authentication.
	IDTokenHint string
}

// AuthFlow represents an in-progress OAuth authorization flow.
type AuthFlow struct {
	// ServerURL is the URL of the Muster server we're authenticating to.
	ServerURL string

	// IssuerURL is the OAuth issuer URL.
	IssuerURL string

	// PKCE holds the PKCE challenge parameters.
	PKCE *pkgoauth.PKCEChallenge

	// State is the OAuth state parameter.
	State string

	// CallbackServer is the local HTTP server waiting for the callback.
	CallbackServer *CallbackServer

	// Metadata is the discovered OAuth metadata.
	Metadata *OAuthMetadata

	// StartedAt is when the flow was initiated.
	StartedAt time.Time

	// Options contains the flow options (silent mode, login hint, etc.)
	Options *AuthFlowOptions
}

// cachedMetadata holds OAuth metadata with its cache timestamp.
type cachedMetadata struct {
	metadata *OAuthMetadata
	cachedAt time.Time
}

// Client is the OAuth client for the Muster Agent.
// It manages OAuth authentication flows for connecting to protected Muster servers.
type Client struct {
	mu            sync.RWMutex
	tokenStore    *TokenStore
	httpClient    *http.Client
	callbackPort  int
	currentFlow   *AuthFlow
	metadataCache map[string]*cachedMetadata
	oauthClient   *pkgoauth.Client // Shared OAuth client for protocol operations
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

	// Create shared OAuth client with the same HTTP client
	oauthClient := pkgoauth.NewClient(
		pkgoauth.WithHTTPClient(httpClient),
		pkgoauth.WithMetadataCacheTTL(MetadataCacheTTL),
	)

	return &Client{
		tokenStore:    tokenStore,
		httpClient:    httpClient,
		callbackPort:  callbackPort,
		metadataCache: make(map[string]*cachedMetadata),
		oauthClient:   oauthClient,
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
	return c.StartAuthFlowWithOptions(ctx, serverURL, issuerURL, nil)
}

// StartAuthFlowWithOptions initiates an OAuth authorization flow with configurable options.
// The options parameter controls OIDC-specific behavior like silent re-authentication.
//
// When opts.Silent is true, the flow uses prompt=none which attempts silent re-authentication.
// If the user doesn't have an active IdP session, the callback will contain an error
// (login_required, consent_required, or interaction_required) instead of showing a login page.
//
// Returns the authorization URL that should be opened in the browser.
func (c *Client) StartAuthFlowWithOptions(ctx context.Context, serverURL, issuerURL string, opts *AuthFlowOptions) (string, error) {
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
	pkce, err := pkgoauth.GeneratePKCE()
	if err != nil {
		return "", fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// Generate state
	state, err := pkgoauth.GenerateState()
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
		Options:        opts,
	}

	// Build authorization URL with options
	authURL, err := c.buildAuthorizationURLWithOptions(metadata, redirectURI, state, pkce, opts)
	if err != nil {
		c.cancelCurrentFlow()
		return "", fmt.Errorf("failed to build authorization URL: %w", err)
	}

	return authURL, nil
}

// WaitForCallback waits for the OAuth callback and exchanges the code for tokens.
// This should be called after StartAuthFlow and after the user has authenticated.
//
// For silent auth flows (prompt=none), if the IdP returns an error like login_required,
// consent_required, or interaction_required, this method returns a *mcpoauth.SilentAuthError.
// Callers can check for this using mcpoauth.IsSilentAuthError(err) and fall back to
// interactive authentication.
func (c *Client) WaitForCallback(ctx context.Context) (*oauth2.Token, error) {
	c.mu.RLock()
	flow := c.currentFlow
	c.mu.RUnlock()

	if flow == nil {
		return nil, errors.New("no auth flow in progress")
	}

	// Create timeout context - use shorter timeout for silent auth
	timeout := CallbackTimeout
	if flow.Options != nil && flow.Options.Silent {
		timeout = SilentAuthTimeout
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wait for callback
	result, err := flow.CallbackServer.WaitForCallback(timeoutCtx)
	if err != nil {
		c.mu.Lock()
		c.cancelCurrentFlow()
		c.mu.Unlock()
		return nil, fmt.Errorf("callback failed: %w", err)
	}

	// Verify state - critical security check to prevent CSRF attacks
	if result.State != flow.State {
		slog.Debug("OAuth state mismatch detected - possible CSRF attack",
			"server_url", flow.ServerURL,
			"expected_state_len", len(flow.State),
			"received_state_len", len(result.State),
		)
		c.mu.Lock()
		c.cancelCurrentFlow()
		c.mu.Unlock()
		return nil, errors.New("state mismatch - possible CSRF attack")
	}

	// Check for error from authorization server using mcp-oauth error parsing
	if result.IsError() {
		slog.Debug("OAuth authorization failed",
			"server_url", flow.ServerURL,
			"error", result.Error,
			"error_description", result.ErrorDescription,
			"silent_mode", flow.Options != nil && flow.Options.Silent,
		)
		c.mu.Lock()
		c.cancelCurrentFlow()
		c.mu.Unlock()

		// Use mcp-oauth to parse the error - this returns SilentAuthError for
		// login_required, consent_required, and interaction_required errors
		return nil, mcpoauth.ParseOAuthError(result.Error, result.ErrorDescription)
	}

	// Exchange code for tokens
	token, err := c.exchangeCode(ctx, flow, result.Code)
	if err != nil {
		slog.Debug("OAuth token exchange failed",
			"server_url", flow.ServerURL,
			"issuer_url", flow.IssuerURL,
			"error", err.Error(),
		)
		c.mu.Lock()
		c.cancelCurrentFlow()
		c.mu.Unlock()
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	slog.Debug("OAuth authentication successful",
		"server_url", flow.ServerURL,
		"issuer_url", flow.IssuerURL,
	)

	// Store token
	if err := c.tokenStore.StoreToken(flow.ServerURL, flow.IssuerURL, token); err != nil {
		// Log but continue - token is still valid for this session
		slog.Debug("failed to persist OAuth token to storage",
			"server_url", flow.ServerURL,
			"error", err.Error(),
		)
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
	return c.CompleteAuthFlowWithOptions(ctx, serverURL, issuerURL, nil)
}

// CompleteAuthFlowWithOptions is a convenience method that combines StartAuthFlowWithOptions and WaitForCallback.
// It returns the authorization URL and a callback function to wait for completion.
//
// When opts.Silent is true, the flow uses prompt=none for silent re-authentication.
// If silent auth fails, the wait function returns an error detectable with mcpoauth.IsSilentAuthError().
func (c *Client) CompleteAuthFlowWithOptions(ctx context.Context, serverURL, issuerURL string, opts *AuthFlowOptions) (authURL string, waitFn func() (*oauth2.Token, error), err error) {
	authURL, err = c.StartAuthFlowWithOptions(ctx, serverURL, issuerURL, opts)
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
// Uses the shared OAuth client which handles caching and fallback discovery.
func (c *Client) discoverOAuthMetadata(ctx context.Context, issuerURL string) (*OAuthMetadata, error) {
	// Use shared client for metadata discovery (handles caching internally)
	sharedMeta, err := c.oauthClient.DiscoverMetadata(ctx, issuerURL)
	if err != nil {
		return nil, err
	}

	// Convert to internal type
	return &OAuthMetadata{
		Issuer:                        sharedMeta.Issuer,
		AuthorizationEndpoint:         sharedMeta.AuthorizationEndpoint,
		TokenEndpoint:                 sharedMeta.TokenEndpoint,
		UserinfoEndpoint:              sharedMeta.UserinfoEndpoint,
		JwksURI:                       sharedMeta.JwksURI,
		ScopesSupported:               sharedMeta.ScopesSupported,
		ResponseTypesSupported:        sharedMeta.ResponseTypesSupported,
		CodeChallengeMethodsSupported: sharedMeta.CodeChallengeMethodsSupported,
	}, nil
}

// DefaultAgentClientID is the CIMD URL for the Muster Agent.
// This is hosted on GitHub Pages and serves as the client_id for OAuth.
const DefaultAgentClientID = "https://giantswarm.github.io/muster/muster-agent.json"

// buildAuthorizationURLWithOptions constructs the OAuth authorization URL with optional OIDC parameters.
// The opts parameter allows setting prompt, login_hint, id_token_hint, and other OIDC parameters.
func (c *Client) buildAuthorizationURLWithOptions(metadata *OAuthMetadata, redirectURI, state string, pkce *pkgoauth.PKCEChallenge, opts *AuthFlowOptions) (string, error) {
	// Use golang.org/x/oauth2 Config for constructing the authorization URL
	cfg := &oauth2.Config{
		ClientID:    DefaultAgentClientID,
		RedirectURL: redirectURI,
		Endpoint: oauth2.Endpoint{
			AuthURL:  metadata.AuthorizationEndpoint,
			TokenURL: metadata.TokenEndpoint,
		},
		Scopes: agentOAuthScopes,
	}

	// Build auth code options
	authOpts := []oauth2.AuthCodeOption{
		oauth2.S256ChallengeOption(pkce.CodeVerifier),
	}

	// Apply optional OIDC parameters using mcp-oauth's helper
	if opts != nil {
		providerOpts := &providers.AuthorizationURLOptions{}
		if opts.Silent {
			providerOpts.Prompt = "none"
		}
		if opts.LoginHint != "" {
			providerOpts.LoginHint = opts.LoginHint
		}
		if opts.IDTokenHint != "" {
			providerOpts.IDTokenHint = opts.IDTokenHint
		}
		// Convert to oauth2 options
		authOpts = append(authOpts, providers.ApplyAuthorizationURLOptions(providerOpts)...)
	}

	return cfg.AuthCodeURL(state, authOpts...), nil
}

// exchangeCode exchanges an authorization code for tokens using the standard library.
// This uses golang.org/x/oauth2.Config.Exchange() with PKCE VerifierOption.
func (c *Client) exchangeCode(ctx context.Context, flow *AuthFlow, code string) (*oauth2.Token, error) {
	// Create OAuth2 config for token exchange
	cfg := &oauth2.Config{
		ClientID:    DefaultAgentClientID,
		RedirectURL: flow.CallbackServer.GetRedirectURI(),
		Endpoint: oauth2.Endpoint{
			AuthURL:   flow.Metadata.AuthorizationEndpoint,
			TokenURL:  flow.Metadata.TokenEndpoint,
			AuthStyle: oauth2.AuthStyleInParams, // Use form params, not basic auth
		},
		Scopes: agentOAuthScopes,
	}

	// Use a custom HTTP context to inject our configured client
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.httpClient)

	// Exchange the code using the standard library with PKCE verifier
	token, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(flow.PKCE.CodeVerifier))
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
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

// GetHTTPClient returns the underlying HTTP client for reuse.
// This allows other components (like AuthManager) to reuse the same client
// for connection pooling and consistent timeout behavior.
func (c *Client) GetHTTPClient() *http.Client {
	return c.httpClient
}
