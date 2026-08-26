package oauth

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/giantswarm/muster/internal/config"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/pkg/logging"
)

// softwareVersion is the version string reported in the Client ID Metadata Document.
// This is informational only and helps identify the muster version during OAuth debugging.
const softwareVersion = "1.0.0"

// Client identification methods, reported on auth challenges so front-ends
// can tell users how muster identifies itself to the authorization server.
const (
	// ClientIDMethodCIMD: the AS advertises
	// client_id_metadata_document_supported, so muster's self-hosted CIMD URL
	// is used as client_id (stateless, spec-preferred).
	ClientIDMethodCIMD = "cimd"

	// ClientIDMethodDCR: the AS does not advertise CIMD support but offers a
	// registration_endpoint; muster registered itself via RFC 7591 and uses
	// the issued, issuer-bound credentials.
	ClientIDMethodDCR = "dcr"

	// ClientIDMethodCIMDFallback: the AS advertises neither CIMD support nor
	// a registration endpoint. Muster sends the CIMD URL anyway (some ASes
	// resolve CIMDs without advertising the flag), but the AS may reject the
	// flow with a client-not-registered error.
	ClientIDMethodCIMDFallback = "cimd-fallback"
)

// resolvedClient is the client identification chosen for one issuer.
type resolvedClient struct {
	ClientID     string
	ClientSecret string
	Method       string
}

// Client handles OAuth 2.1 flows for remote MCP server authentication.
type Client struct {
	// Configuration
	clientID     string // The CIMD URL used as client_id
	publicURL    string // The public URL of the Muster Server
	callbackPath string // The path for OAuth callbacks (e.g., "/oauth/callback")
	cimdScopes   string // The OAuth scopes to advertise in the CIMD

	// Stores (interface-backed; defaults to in-memory, can be swapped to Valkey)
	tokenStore TokenStorer
	stateStore StateStorer
	credStore  ClientCredentialStorer

	// registerMu serializes DCR registrations so concurrent auth flows for
	// the same issuer don't register muster twice.
	registerMu sync.Mutex

	// Shared OAuth client for protocol operations
	oauthClient *pkgoauth.Client
}

// ClientOption configures optional Client parameters.
type ClientOption func(*Client)

// WithTokenStorer sets a custom TokenStorer implementation (e.g., Valkey-backed).
func WithTokenStorer(ts TokenStorer) ClientOption {
	return func(c *Client) {
		c.tokenStore = ts
	}
}

// WithStateStorer sets a custom StateStorer implementation (e.g., Valkey-backed).
func WithStateStorer(ss StateStorer) ClientOption {
	return func(c *Client) {
		c.stateStore = ss
	}
}

// WithClientCredentialStorer sets a custom ClientCredentialStorer
// implementation (e.g., Valkey-backed) for DCR-issued client credentials.
func WithClientCredentialStorer(cs ClientCredentialStorer) ClientOption {
	return func(c *Client) {
		c.credStore = cs
	}
}

// NewClient creates a new OAuth client with the given configuration.
// By default, in-memory stores are used. Use WithTokenStorer / WithStateStorer
// to inject Valkey-backed implementations.
func NewClient(clientID, publicURL, callbackPath, cimdScopes string, opts ...ClientOption) *Client {
	c := &Client{
		clientID:     clientID,
		publicURL:    publicURL,
		callbackPath: callbackPath,
		cimdScopes:   cimdScopes,
		tokenStore:   NewTokenStore(),
		stateStore:   NewStateStore(),
		credStore:    NewClientCredentialStore(),
		oauthClient:  pkgoauth.NewClient(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// GetTokenStore returns the token store for external access.
func (c *Client) GetTokenStore() TokenStorer {
	return c.tokenStore
}

// GetStateStore returns the state store for external access.
func (c *Client) GetStateStore() StateStorer {
	return c.stateStore
}

// GetRedirectURI returns the full redirect URI for OAuth callbacks.
func (c *Client) GetRedirectURI() string {
	return strings.TrimSuffix(c.publicURL, "/") + c.callbackPath
}

// GetCIMDURL returns the URL where the Client ID Metadata Document is served.
// This is derived from the clientID which is expected to be the CIMD URL.
func (c *Client) GetCIMDURL() string {
	return c.clientID
}

// GetToken retrieves a valid token for the given session and issuer.
// Returns nil if no valid token exists.
func (c *Client) GetToken(sessionID, issuer, scope string) *pkgoauth.Token {
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

// resolveClient decides how muster identifies itself to the given issuer:
// CIMD when the AS advertises support for it, previously registered
// DCR credentials when present, a fresh RFC 7591 registration when the AS
// offers one (and allowRegistration is set), and the CIMD URL as a last
// resort. Registration failures fall back to the CIMD URL rather than
// aborting — that is never worse than the pre-DCR behavior.
func (c *Client) resolveClient(ctx context.Context, issuer string, metadata *pkgoauth.Metadata, allowRegistration bool) *resolvedClient {
	if metadata.ClientIDMetadataDocumentSupported {
		return &resolvedClient{ClientID: c.clientID, Method: ClientIDMethodCIMD}
	}

	if creds := c.credStore.Get(issuer); creds != nil {
		return &resolvedClient{ClientID: creds.ClientID, ClientSecret: creds.ClientSecret, Method: ClientIDMethodDCR}
	}

	if allowRegistration && metadata.RegistrationEndpoint != "" {
		c.registerMu.Lock()
		defer c.registerMu.Unlock()

		// Re-check after acquiring the lock: a concurrent flow may have
		// registered while we waited.
		if creds := c.credStore.Get(issuer); creds != nil {
			return &resolvedClient{ClientID: creds.ClientID, ClientSecret: creds.ClientSecret, Method: ClientIDMethodDCR}
		}

		registration, err := c.oauthClient.RegisterClient(ctx, metadata.RegistrationEndpoint, c.GetRegistrationMetadata())
		if err != nil {
			logging.Warn("OAuth", "Dynamic client registration with %s failed, falling back to CIMD URL as client_id: %v",
				issuer, err)
			return &resolvedClient{ClientID: c.clientID, Method: ClientIDMethodCIMDFallback}
		}

		creds := &pkgoauth.ClientCredentials{
			Issuer:                  issuer,
			ClientID:                registration.ClientID,
			ClientSecret:            registration.ClientSecret,
			RegistrationAccessToken: registration.RegistrationAccessToken,
			RegistrationClientURI:   registration.RegistrationClientURI,
			CreatedAt:               time.Now(),
		}
		if registration.ClientSecretExpiresAt > 0 {
			creds.ClientSecretExpiresAt = time.Unix(registration.ClientSecretExpiresAt, 0)
		}
		c.credStore.Store(issuer, creds)

		logging.Info("OAuth", "Registered muster as an OAuth client with %s via RFC 7591 (client_id=%s)",
			issuer, registration.ClientID)
		return &resolvedClient{ClientID: creds.ClientID, ClientSecret: creds.ClientSecret, Method: ClientIDMethodDCR}
	}

	return &resolvedClient{ClientID: c.clientID, Method: ClientIDMethodCIMDFallback}
}

// GenerateAuthURL creates an OAuth authorization URL for user authentication.
// Returns the URL and the client identification method used (one of the
// ClientIDMethod* constants). The code verifier is stored with the state for
// later retrieval.
func (c *Client) GenerateAuthURL(ctx context.Context, sessionID, userID, serverName, issuer, scope string) (string, string, error) {
	metadata, err := c.oauthClient.DiscoverMetadata(ctx, issuer)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}

	// MCP 2025-11-25 §"Authorization Code Protection" requires refusing the
	// flow if the AS doesn't advertise S256 PKCE. Fail closed: a server that
	// doesn't list it may silently ignore code_challenge_method=S256 and
	// produce confusing token-endpoint errors later.
	if !metadata.SupportsS256PKCE() {
		return "", "", fmt.Errorf("authorization server %q does not advertise S256 PKCE in code_challenge_methods_supported (MCP 2025-11-25 requires refusal)", issuer)
	}

	// Resolve the client identification for this issuer, registering via
	// RFC 7591 when the AS supports DCR but not CIMD.
	resolved := c.resolveClient(ctx, issuer, metadata, true)

	pkce, err := pkgoauth.GeneratePKCE()
	if err != nil {
		return "", "", fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// The upstream authorization URL is stored with the state in a single
	// write; the user-facing URL is the muster-hosted start endpoint, which
	// redirects the browser there. The extra hop lets the initiating
	// front-end attach an allowlisted post-login redirect target to the flow
	// (the "redirect" query parameter on the start URL).
	state, err := c.stateStore.GenerateState(sessionID, userID, serverName, issuer, pkce.CodeVerifier,
		func(encodedState string) (string, error) {
			return c.oauthClient.BuildAuthorizationURL(
				metadata.AuthorizationEndpoint,
				resolved.ClientID,
				c.GetRedirectURI(),
				encodedState,
				scope,
				pkce,
			)
		})
	if err != nil {
		return "", "", fmt.Errorf("failed to generate state: %w", err)
	}

	logging.Debug("OAuth", "Generated auth URL for session=%s server=%s issuer=%s clientIdMethod=%s",
		logging.TruncateIdentifier(sessionID), serverName, issuer, resolved.Method)

	return c.GetStartURL(state), resolved.Method, nil
}

// GetStartURL returns the muster-hosted start URL for an encoded state. The
// browser is redirected from there to the upstream authorization server.
func (c *Client) GetStartURL(encodedState string) string {
	return strings.TrimSuffix(c.publicURL, "/") + config.DefaultOAuthProxyStartPath +
		"?state=" + url.QueryEscape(encodedState)
}

// ExchangeCode exchanges an authorization code for tokens.
func (c *Client) ExchangeCode(ctx context.Context, code, codeVerifier, issuer string) (*pkgoauth.Token, error) {
	// Fetch OAuth metadata using shared client
	metadata, err := c.oauthClient.DiscoverMetadata(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}

	// Resolve the same client identification the authorization request used.
	// Registration is not attempted here: any DCR registration happened in
	// GenerateAuthURL, and its credentials are read back from the store.
	resolved := c.resolveClient(ctx, issuer, metadata, false)

	// Exchange code using shared client
	token, err := c.oauthClient.ExchangeCode(
		ctx,
		metadata.TokenEndpoint,
		code,
		c.GetRedirectURI(),
		resolved.ClientID,
		resolved.ClientSecret,
		codeVerifier,
	)
	if err != nil {
		return nil, err
	}

	// Set issuer on the token
	token.Issuer = issuer

	logging.Debug("OAuth", "Successfully exchanged code for token (issuer=%s, expires_in=%d)",
		issuer, token.ExpiresIn)

	return token, nil
}

// StoreToken stores a token in the token store.
func (c *Client) StoreToken(sessionID, userID string, token *pkgoauth.Token) {
	key := TokenKey{
		SessionID: sessionID,
		Issuer:    token.Issuer,
		Scope:     token.Scope,
	}
	c.tokenStore.Store(key, token, userID)
}

// Stop stops background cleanup goroutines.
func (c *Client) Stop() {
	c.tokenStore.Stop()
	c.stateStore.Stop()
	c.credStore.Stop()
}

// GetClientMetadata returns the Client ID Metadata Document for this client.
// ApplicationType "web" is included per SEP-837: authorization servers that
// resolve the CIMD and synthesize a registration would otherwise apply an
// OIDC default that can reject non-localhost redirect URIs.
func (c *Client) GetClientMetadata() *pkgoauth.ClientMetadata {
	return &pkgoauth.ClientMetadata{
		ClientID:                c.clientID,
		ClientName:              "Muster MCP Aggregator",
		ClientURI:               "https://github.com/giantswarm/muster",
		RedirectURIs:            []string{c.GetRedirectURI()},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
		Scope:                   c.cimdScopes,
		SoftwareID:              "giantswarm-muster",
		SoftwareVersion:         softwareVersion,
		ApplicationType:         "web",
	}
}

// GetRegistrationMetadata returns the RFC 7591 client metadata muster sends
// on Dynamic Client Registration requests. It mirrors the CIMD content but
// omits client_id (forbidden on registration requests) and requests
// token_endpoint_auth_method "none" — muster is a public client protected by
// PKCE, and staying secret-free keeps the DCR path as close to the CIMD path
// as the AS allows. ASes that insist on issuing a secret still can; the
// response's client_secret is honored either way.
func (c *Client) GetRegistrationMetadata() *pkgoauth.ClientMetadata {
	metadata := c.GetClientMetadata()
	metadata.ClientID = ""
	return metadata
}

// GetClientCredentialsForIssuer returns the client_id and client_secret the
// OAuth flows use against the given issuer, without triggering a new DCR
// registration. This backs mcp-go's transport-level token refresh, which
// must present the same client identification the token was issued under.
func (c *Client) GetClientCredentialsForIssuer(ctx context.Context, issuer string) (clientID, clientSecret string) {
	metadata, err := c.oauthClient.DiscoverMetadata(ctx, issuer)
	if err != nil {
		// Metadata should be cached from the original flow; on a cold cache
		// with an unreachable AS the CIMD URL is the only sensible answer.
		logging.Debug("OAuth", "GetClientCredentialsForIssuer: metadata discovery for %s failed, using CIMD URL: %v", issuer, err)
		return c.clientID, ""
	}
	resolved := c.resolveClient(ctx, issuer, metadata, false)
	return resolved.ClientID, resolved.ClientSecret
}

// DiscoverMetadata fetches OAuth metadata for an issuer.
// This is exposed for external access to metadata discovery.
func (c *Client) DiscoverMetadata(ctx context.Context, issuer string) (*pkgoauth.Metadata, error) {
	return c.oauthClient.DiscoverMetadata(ctx, issuer)
}
