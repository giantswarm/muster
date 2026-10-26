package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"

	"github.com/giantswarm/muster/v5/pkg/logging"
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

// ClientIDMethodNone marks a flow of a server outside muster's namespace that
// has no client: no pin with a client and no registration of its own. muster
// does not lend it its CIMD client, so the flow is refused.
const ClientIDMethodNone = "none"

var (
	errTenantNoClient  = errors.New("a server outside muster's namespace signs in with its own pin (spec.auth.authorizationServer.clientCredentialsSecretRef) or with a dynamic client registration, and this authorization server offers neither")
	errTenantPinIsCIMD = errors.New("a server outside muster's namespace may not pin muster's own client ID metadata document URL as its client")

	// errPinChanged refuses the callback of a flow whose issuer pin changed
	// after the flow started: the code and its PKCE verifier would go to a
	// token endpoint and a client the code was not issued for.
	errPinChanged = errors.New("the authorization server's description changed after the sign-in started; sign in again")
)

// ClientIDMethodPreregistered marks a client the operator registered with
// the authorization server out of band (spec.auth.authorizationServer.
// clientCredentialsSecretRef): the only way in against an authorization
// server that supports neither CIMD nor RFC 7591, GitHub being the case.
const ClientIDMethodPreregistered = "preregistered"

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

	// pins holds the operator-configured authorization servers by issuer
	// key. A stored pin is never modified: a reader that took one sees its
	// endpoints and its client from the same description.
	pinsMu sync.RWMutex
	pins   map[string]*IssuerPin

	// refreshGroup single-flights the refresh of a subject-scoped grant per
	// issuer and person, see SubjectGrant.
	refreshGroup refreshGroup
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

// GetRedirectURI returns the full redirect URI for OAuth callbacks of the
// servers in muster's namespace.
func (c *Client) GetRedirectURI() string {
	return strings.TrimSuffix(c.publicURL, "/") + c.callbackPath
}

// redirectURIFor returns the redirect URI of the flows filed under an issuer
// key (api.IssuerKey): the plain callback for muster's namespace, and the
// namespace's own callback for a server outside it. The callback accepts a
// flow only on the redirect URI of the flow's namespace.
func (c *Client) redirectURIFor(issuerKey string) string {
	namespace, _ := api.SplitIssuerKey(issuerKey)
	if namespace == "" {
		return c.GetRedirectURI()
	}
	return strings.TrimSuffix(c.publicURL, "/") + config.OAuthProxyNamespaceCallbackPath(namespace)
}

// callbackPathFor returns the path of redirectURIFor.
func (c *Client) callbackPathFor(issuerKey string) string {
	namespace, _ := api.SplitIssuerKey(issuerKey)
	if namespace == "" {
		return c.callbackPath
	}
	return config.OAuthProxyNamespaceCallbackPath(namespace)
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

// IssuerSubjectScoped is the exported form of subjectScoped: whether the
// operator pinned the issuer with grantScope: subject.
func (c *Client) IssuerSubjectScoped(issuer string) bool {
	return c.subjectScoped(issuer)
}

// GetTokenForUser is GetToken with subject-scoped grants: for an issuer
// pinned with grantScope: subject the person's grant is the canonical token
// -- refreshed on the way when it is due (see SubjectGrant) -- and the
// session's own copy is consulted only when the person holds no grant. For
// every other issuer the session's token is returned as before.
func (c *Client) GetTokenForUser(sessionID, userID, issuer, scope string) *pkgoauth.Token {
	if userID != "" && c.subjectScoped(issuer) {
		if grant := c.SubjectGrant(context.Background(), userID, issuer); grant != nil {
			return grant
		}
	}
	return c.GetToken(sessionID, issuer, scope)
}

// GetByIssuerForUser is the issuer-only lookup with subject-scoped grants,
// see GetTokenForUser.
func (c *Client) GetByIssuerForUser(sessionID, userID, issuer string) *pkgoauth.Token {
	if userID != "" && c.subjectScoped(issuer) {
		if grant := c.SubjectGrant(context.Background(), userID, issuer); grant != nil {
			return grant
		}
	}
	return c.tokenStore.GetByIssuer(sessionID, issuer)
}

// DeleteByIssuerForUser removes the session's tokens for an issuer and, for a
// subject-scoped issuer, the person's grant as well: an explicit sign-out from
// the server disconnects the person, not just this session.
//
// Before the person's grant goes, it is revoked at the authorization server
// (see revokeSubjectGrant), so the provider stops honouring the tokens and
// the next sign-in asks for consent again.
func (c *Client) DeleteByIssuerForUser(sessionID, userID, issuer string) {
	if userID != "" && c.subjectScoped(issuer) {
		c.revokeSubjectGrant(userID, issuer)
	}
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
func (c *Client) resolveClient(ctx context.Context, issuer string, pin *IssuerPin, metadata *pkgoauth.Metadata, allowRegistration bool) *resolvedClient {
	tenant := api.IsTenantIssuerKey(issuer)

	// An operator-registered client wins over every discovered mechanism:
	// it exists precisely because the AS accepts nothing else.
	if pin != nil && pin.ClientID != "" {
		if tenant && pin.ClientID == c.clientID {
			return &resolvedClient{Method: ClientIDMethodNone, RegistrationError: errTenantPinIsCIMD}
		}
		return &resolvedClient{ClientID: pin.ClientID, ClientSecret: pin.ClientSecret, Method: ClientIDMethodPreregistered}
	}

	// muster's one CIMD client is for the servers of muster's namespace. A
	// server outside it signs in with its own pin or its own registration.
	if metadata.ClientIDMetadataDocumentSupported && !tenant {
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

	if tenant {
		return &resolvedClient{Method: ClientIDMethodNone, RegistrationError: errTenantNoClient}
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
	pin := c.issuerPin(issuer)
	metadata, err := c.metadataFor(ctx, issuer, pin)
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
	resolved := c.resolveClient(ctx, issuer, pin, metadata, true)
	if resolved.Method == ClientIDMethodNone {
		return "", nil, fmt.Errorf("authorization server %q: %w", api.IssuerURL(issuer), resolved.RegistrationError)
	}

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
		Scope:        params.Scope,
		CodeVerifier: pkce.CodeVerifier,
		PinDigest:    pin.stateDigest(),
	}
	state, err := c.stateStore.GenerateState(stateParams,
		func(encodedState string) (string, error) {
			return c.oauthClient.BuildAuthorizationURL(pkgoauth.AuthorizationRequest{
				AuthorizationEndpoint: metadata.AuthorizationEndpoint,
				ClientID:              resolved.ClientID,
				RedirectURI:           c.redirectURIFor(issuer),
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

// ExchangeCode exchanges the authorization code of the flow behind state for
// tokens. The token request carries the RFC 8707 resource recorded with the
// flow, the value sent on the authorization request. The scope recorded with
// the flow is the one the authorization request asked for: an authorization
// server that omits `scope` from its token response granted exactly that
// (RFC 6749 §5.1), so the token is recorded with it. Dex omits the
// parameter; without this every Dex grant was filed under an empty scope --
// the token-store key of the session's own login token when the pinned
// issuer is muster's IdP (giantswarm/muster#1174).
//
// The code was issued to the client of the description the flow started
// under. When the issuer's pin has changed since (another description took
// over, or the pin went), nothing is sent and errPinChanged is returned.
//
// An invalid_client answer from the token endpoint means the AS no longer
// accepts the DCR credentials the flow was started with; they are dropped so
// the next sign-in registers muster again instead of failing the same way.
func (c *Client) ExchangeCode(ctx context.Context, state *OAuthState, code string) (*pkgoauth.Token, error) {
	issuer := state.Issuer
	pin, err := c.flowPin(state)
	if err != nil {
		return nil, err
	}
	metadata, err := c.metadataFor(ctx, issuer, pin)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}

	// Resolve the same client identification the authorization request used.
	// Registration is not attempted here: any DCR registration happened in
	// GenerateAuthURL, and its credentials are read back from the store.
	resolved := c.resolveClient(ctx, issuer, pin, metadata, false)
	if resolved.Method == ClientIDMethodNone {
		return nil, fmt.Errorf("authorization server %q: %w", api.IssuerURL(issuer), resolved.RegistrationError)
	}

	// Exchange code using shared client. RFC 6749 §4.1.3: the same
	// redirect_uri as the authorization request.
	token, err := c.oauthClient.ExchangeCode(
		ctx,
		metadata.TokenEndpoint,
		code,
		c.redirectURIFor(issuer),
		resolved.ClientID,
		resolved.ClientSecret,
		state.CodeVerifier,
		state.Resource,
	)
	if err != nil {
		if resolved.Method == ClientIDMethodDCR && pkgoauth.IsInvalidClientError(err) {
			c.dropRegistration(issuer, resolved.ClientID, "token endpoint answered invalid_client")
		}
		return nil, err
	}

	// Set issuer on the token
	token.Issuer = issuer
	if token.Scope == "" {
		token.Scope = state.Scope
	}

	logging.Debug("OAuth", "Successfully exchanged code for token (issuer=%s, scope=%q, expires_in=%d)",
		issuer, token.Scope, token.ExpiresIn)

	return token, nil
}

// StoreToken stores a token in the token store. A token from a
// subject-scoped issuer is filed under the person's identity as well, so any
// later session of the same person reuses it (see GetTokenForUser).
func (c *Client) StoreToken(sessionID, userID string, token *pkgoauth.Token) {
	c.storeTokenUnder(sessionID, userID, token.Issuer, token)
}

// storeTokenUnder is StoreToken with the issuer the token is filed under
// named apart from the token's own.
func (c *Client) storeTokenUnder(sessionID, userID, issuer string, token *pkgoauth.Token) {
	key := TokenKey{
		SessionID: sessionID,
		Issuer:    issuer,
		Scope:     token.Scope,
	}
	c.tokenStore.Store(key, token, userID)

	if userID != "" && c.subjectScoped(issuer) {
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

// DiscoverMetadata returns the metadata of the authorization server behind an
// issuer key (api.IssuerKey), see metadataFor.
func (c *Client) DiscoverMetadata(ctx context.Context, issuer string) (*pkgoauth.Metadata, error) {
	return c.metadataFor(ctx, issuer, c.issuerPin(issuer))
}
