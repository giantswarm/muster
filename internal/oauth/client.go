package oauth

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

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

	// ClientIDMethodDCRFailed: the AS advertises a registration endpoint but
	// rejected muster's RFC 7591 registration request. Muster sends the CIMD
	// URL anyway, like the plain fallback, but the challenge carries the
	// rejection so front-ends can show the real reason instead of claiming
	// the AS supports neither mechanism.
	ClientIDMethodDCRFailed = "dcr-failed"
)

// ClientIDMethodPreregistered marks a client the operator registered with
// the authorization server out of band (spec.auth.authorizationServer.
// clientCredentialsSecretRef): the only way in against an authorization
// server that supports neither CIMD nor RFC 7591, GitHub being the case.
const ClientIDMethodPreregistered = "preregistered"

// IssuerPin is what an operator configured for one authorization server on an
// MCPServer (spec.auth.authorizationServer) beyond the issuer itself: a
// pre-registered client, and whether the tokens the AS issues belong to the
// person (subject) or to the login session that obtained them.
type IssuerPin struct {
	ClientID      string
	ClientSecret  string
	SubjectScoped bool
}

// subjectSessionID is the token-store session under which a subject-scoped
// grant is filed in addition to the real session: any session of the same
// person finds it there. The prefix cannot collide with a real session id,
// which mcp-oauth derives from token families or bearer hashes.
func subjectSessionID(userID string) string {
	return "subject:" + userID
}

// resolvedClient is the client identification chosen for one issuer.
type resolvedClient struct {
	ClientID     string
	ClientSecret string
	Method       string

	// RegistrationError is the RFC 7591 rejection that led to
	// ClientIDMethodDCRFailed; nil for every other method.
	RegistrationError error
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

	// registerMu serializes every change to the stored DCR credentials of
	// an issuer — registrations, and the drops that precede a
	// re-registration — so concurrent auth flows for the same issuer don't
	// register muster twice or discard each other's fresh registration.
	registerMu sync.Mutex

	// Shared OAuth client for protocol operations
	oauthClient *pkgoauth.Client

	// pins holds the operator-configured authorization servers by issuer.
	pinsMu sync.RWMutex
	pins   map[string]*IssuerPin
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
		pins:         make(map[string]*IssuerPin),
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

// PinIssuer records the operator's description of an authorization server:
// the pre-registered client to identify with, whether its grants are
// subject-scoped, and -- for an AS without a discovery document -- its
// metadata. Calling it again for the same issuer replaces the pin, which is
// how a rotated client secret takes effect.
func (c *Client) PinIssuer(issuer string, pin IssuerPin, metadata *pkgoauth.Metadata) {
	issuer = strings.TrimSuffix(issuer, "/")
	if metadata != nil {
		c.oauthClient.PinMetadata(issuer, metadata)
	}
	c.pinsMu.Lock()
	c.pins[issuer] = &pin
	c.pinsMu.Unlock()
	logging.Info("OAuth", "Pinned authorization server issuer=%s preregisteredClient=%t subjectScoped=%t pinnedMetadata=%t",
		issuer, pin.ClientID != "", pin.SubjectScoped, metadata != nil)
}

// issuerPin returns the operator pin for an issuer, or nil.
func (c *Client) issuerPin(issuer string) *IssuerPin {
	c.pinsMu.RLock()
	defer c.pinsMu.RUnlock()
	return c.pins[strings.TrimSuffix(issuer, "/")]
}

// subjectScoped reports whether grants from the issuer belong to the person.
func (c *Client) subjectScoped(issuer string) bool {
	pin := c.issuerPin(issuer)
	return pin != nil && pin.SubjectScoped
}

// IssuerSubjectScoped is the exported form of subjectScoped: whether the
// operator pinned the issuer with grantScope: subject.
func (c *Client) IssuerSubjectScoped(issuer string) bool {
	return c.subjectScoped(issuer)
}

// GetTokenForUser is GetToken plus the subject-scoped fallback: when the
// session holds nothing for a subject-scoped issuer, the grant filed under
// the user's identity by any earlier session is used.
func (c *Client) GetTokenForUser(sessionID, userID, issuer, scope string) *pkgoauth.Token {
	if token := c.GetToken(sessionID, issuer, scope); token != nil {
		return token
	}
	if userID != "" && c.subjectScoped(issuer) {
		return c.GetToken(subjectSessionID(userID), issuer, scope)
	}
	return nil
}

// GetByIssuerForUser is the issuer-only lookup with the subject-scoped
// fallback, see GetTokenForUser.
func (c *Client) GetByIssuerForUser(sessionID, userID, issuer string) *pkgoauth.Token {
	if token := c.tokenStore.GetByIssuer(sessionID, issuer); token != nil {
		return token
	}
	if userID != "" && c.subjectScoped(issuer) {
		return c.tokenStore.GetByIssuer(subjectSessionID(userID), issuer)
	}
	return nil
}

// DeleteByIssuerForUser removes the session's tokens for an issuer and, for a
// subject-scoped issuer, the person's grant as well: an explicit sign-out from
// the server disconnects the person, not just this session.
func (c *Client) DeleteByIssuerForUser(sessionID, userID, issuer string) {
	c.tokenStore.DeleteByIssuer(sessionID, issuer)
	if userID != "" && c.subjectScoped(issuer) {
		// Every session of the person filed its copy with the user id, the
		// subject grant included, so one sweep disconnects them all.
		c.tokenStore.DeleteByUserAndIssuer(userID, issuer)
	}
}

// resolveClient decides how muster identifies itself to the given issuer:
// CIMD when the AS advertises support for it, previously registered DCR
// credentials when present and still honored, a fresh RFC 7591 registration
// when the AS offers one (and allowRegistration is set), and the CIMD URL as
// a last resort. Registration failures fall back to the CIMD URL rather than
// aborting — that is never worse than the pre-DCR behavior.
//
// allowRegistration marks the start of a new flow. Only then are stored
// credentials verified against the AS before use (see registrationGone): an
// AS that lost its client registry — one with an in-memory client store that
// restarted — would otherwise refuse every sign-in with invalid_client in the
// user's browser, a failure muster never sees, and the stored registration
// would stay poisoned until an operator deleted it by hand. A flow that is
// being completed (code exchange, token refresh) uses the stored credentials
// as they are: they are the identification the flow started with.
func (c *Client) resolveClient(ctx context.Context, issuer string, metadata *pkgoauth.Metadata, allowRegistration bool) *resolvedClient {
	// An operator-registered client wins over every discovered mechanism:
	// it exists precisely because the AS accepts nothing else.
	if pin := c.issuerPin(issuer); pin != nil && pin.ClientID != "" {
		return &resolvedClient{ClientID: pin.ClientID, ClientSecret: pin.ClientSecret, Method: ClientIDMethodPreregistered}
	}

	if metadata.ClientIDMetadataDocumentSupported {
		return &resolvedClient{ClientID: c.clientID, Method: ClientIDMethodCIMD}
	}

	var stale *pkgoauth.ClientCredentials
	var staleReason string
	if creds := c.credStore.Get(issuer); creds != nil {
		if !allowRegistration {
			return dcrClient(creds)
		}
		gone, reason := c.registrationGone(ctx, issuer, metadata, creds)
		if !gone {
			return dcrClient(creds)
		}
		stale, staleReason = creds, reason
	}

	if allowRegistration && (metadata.RegistrationEndpoint != "" || stale != nil) {
		c.registerMu.Lock()
		defer c.registerMu.Unlock()

		// Re-check after acquiring the lock: a concurrent flow may have
		// registered — or already replaced the stale registration — while
		// we waited. Only the exact credentials found stale are dropped.
		if creds := c.credStore.Get(issuer); creds != nil {
			if stale == nil || creds.ClientID != stale.ClientID {
				return dcrClient(creds)
			}
			c.dropRegistrationLocked(issuer, creds.ClientID, staleReason)
		}

		if metadata.RegistrationEndpoint != "" {
			return c.register(ctx, issuer, metadata.RegistrationEndpoint)
		}
	}

	return &resolvedClient{ClientID: c.clientID, Method: ClientIDMethodCIMDFallback}
}

// GenerateAuthURL creates an OAuth authorization URL for user authentication.
// Returns the URL and the resolved client identification (its Method is one
// of the ClientIDMethod* constants). The code verifier is stored with the
// state for later retrieval.
//
// params.Resource is recorded with the state so the token request carries the
// same RFC 8707 value as the authorization request.
func (c *Client) GenerateAuthURL(ctx context.Context, params AuthChallengeParams) (string, *resolvedClient, error) {
	issuer := params.Issuer
	metadata, err := c.oauthClient.DiscoverMetadata(ctx, issuer)
	if err != nil {
		return "", nil, fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}

	// MCP 2025-11-25 §"Authorization Code Protection" requires refusing the
	// flow if the AS doesn't advertise S256 PKCE. Fail closed: a server that
	// doesn't list it may silently ignore code_challenge_method=S256 and
	// produce confusing token-endpoint errors later.
	if !metadata.SupportsS256PKCE() {
		return "", nil, fmt.Errorf("authorization server %q does not advertise S256 PKCE in code_challenge_methods_supported (MCP 2025-11-25 requires refusal)", issuer)
	}

	// Resolve the client identification for this issuer, registering via
	// RFC 7591 when the AS supports DCR but not CIMD.
	resolved := c.resolveClient(ctx, issuer, metadata, true)

	pkce, err := pkgoauth.GeneratePKCE()
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// The upstream authorization URL is stored with the state in a single
	// write; the user-facing URL is the muster-hosted start endpoint, which
	// redirects the browser there. The extra hop lets the initiating
	// front-end attach an allowlisted post-login redirect target to the flow
	// (the "redirect" query parameter on the start URL).
	stateParams := StateParams{
		SessionID:    params.SessionID,
		UserID:       params.UserID,
		ServerName:   params.ServerName,
		Issuer:       issuer,
		Resource:     params.Resource,
		CodeVerifier: pkce.CodeVerifier,
	}
	state, err := c.stateStore.GenerateState(stateParams,
		func(encodedState string) (string, error) {
			return c.oauthClient.BuildAuthorizationURL(pkgoauth.AuthorizationRequest{
				AuthorizationEndpoint: metadata.AuthorizationEndpoint,
				ClientID:              resolved.ClientID,
				RedirectURI:           c.GetRedirectURI(),
				State:                 encodedState,
				Scope:                 params.Scope,
				Resource:              params.Resource,
				PKCE:                  pkce,
			})
		})
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate state: %w", err)
	}

	logging.Debug("OAuth", "Generated auth URL for session=%s server=%s issuer=%s resource=%s clientIdMethod=%s",
		logging.TruncateIdentifier(params.SessionID), params.ServerName, issuer, params.Resource, resolved.Method)

	return c.GetStartURL(state), resolved, nil
}

// GetStartURL returns the muster-hosted start URL for an encoded state. The
// browser is redirected from there to the upstream authorization server.
func (c *Client) GetStartURL(encodedState string) string {
	return strings.TrimSuffix(c.publicURL, "/") + config.DefaultOAuthProxyStartPath +
		"?state=" + url.QueryEscape(encodedState)
}

// ExchangeCode exchanges an authorization code for tokens. resource is the
// RFC 8707 indicator recorded with the flow's state; it must match the value
// sent on the authorization request.
//
// An invalid_client answer from the token endpoint means the AS no longer
// accepts the DCR credentials the flow was started with; they are dropped so
// the next sign-in registers muster again instead of failing the same way.
func (c *Client) ExchangeCode(ctx context.Context, code, codeVerifier, issuer, resource string) (*pkgoauth.Token, error) {
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
		resource,
	)
	if err != nil {
		if resolved.Method == ClientIDMethodDCR && pkgoauth.IsInvalidClientError(err) {
			c.dropRegistration(issuer, resolved.ClientID, "token endpoint answered invalid_client")
		}
		return nil, err
	}

	// Set issuer on the token
	token.Issuer = issuer

	logging.Debug("OAuth", "Successfully exchanged code for token (issuer=%s, expires_in=%d)",
		issuer, token.ExpiresIn)

	return token, nil
}

// StoreToken stores a token in the token store. A token from a
// subject-scoped issuer is filed under the person's identity as well, so any
// later session of the same person reuses it (see GetTokenForUser).
func (c *Client) StoreToken(sessionID, userID string, token *pkgoauth.Token) {
	key := TokenKey{
		SessionID: sessionID,
		Issuer:    token.Issuer,
		Scope:     token.Scope,
	}
	c.tokenStore.Store(key, token, userID)

	if userID != "" && c.subjectScoped(token.Issuer) {
		key.SessionID = subjectSessionID(userID)
		c.tokenStore.Store(key, token, userID)
	}
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

// DiscoverMetadata fetches OAuth metadata for an issuer.
// This is exposed for external access to metadata discovery.
func (c *Client) DiscoverMetadata(ctx context.Context, issuer string) (*pkgoauth.Metadata, error) {
	return c.oauthClient.DiscoverMetadata(ctx, issuer)
}
