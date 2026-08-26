package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// AuthState represents the current authentication state of the agent.
type AuthState int

const (
	// AuthStateUnknown means auth state hasn't been determined yet.
	AuthStateUnknown AuthState = iota

	// AuthStateAuthenticated means we have a valid token.
	AuthStateAuthenticated

	// AuthStatePendingAuth means we received 401 and are waiting for user to authenticate.
	AuthStatePendingAuth

	// AuthStateError means authentication failed.
	AuthStateError
)

// String returns the string representation of the auth state.
func (s AuthState) String() string {
	switch s {
	case AuthStateUnknown:
		return "unknown"
	case AuthStateAuthenticated:
		return "authenticated"
	case AuthStatePendingAuth:
		return "pending_auth"
	case AuthStateError:
		return "error"
	default:
		return "unknown"
	}
}

// normalizeServerURL normalizes a server URL for consistent token storage.
// This is a thin wrapper around pkgoauth.NormalizeServerURL for local use.
func normalizeServerURL(serverURL string) string {
	return pkgoauth.NormalizeServerURL(serverURL)
}

// AuthManager manages OAuth authentication for the Muster Agent.
// It handles 401 detection, auth flow orchestration, and state transitions.
type AuthManager struct {
	mu            sync.RWMutex
	client        *Client
	state         AuthState
	serverURL     string
	resource      string
	authChallenge *pkgoauth.AuthChallenge
	authURL       string
	lastError     error
	waitFunc      func() error // Called when waiting for auth to complete
}

// AuthManagerConfig configures the auth manager.
type AuthManagerConfig struct {
	// CallbackPort is the port for the local OAuth callback server.
	CallbackPort int

	// TokenStorageDir is the directory for storing tokens.
	TokenStorageDir string

	// FileMode enables file-based token persistence.
	FileMode bool
}

// NewAuthManager creates a new auth manager.
func NewAuthManager(cfg AuthManagerConfig) (*AuthManager, error) {
	clientCfg := ClientConfig{
		CallbackPort: cfg.CallbackPort,
		TokenStoreConfig: TokenStoreConfig{
			StorageDir: cfg.TokenStorageDir,
			FileMode:   cfg.FileMode,
		},
	}

	client, err := NewClient(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth client: %w", err)
	}

	return &AuthManager{
		client: client,
		state:  AuthStateUnknown,
	}, nil
}

// CheckConnection checks whether the agent has a valid token for the server.
// If no valid token exists, probes the server to discover OAuth auth requirements.
//
// Returns:
//   - AuthStateAuthenticated if a valid token exists in the file store
//   - AuthStatePendingAuth if auth is required (authChallenge will be populated)
//   - AuthStateUnknown if the server doesn't require auth or can't be reached
func (m *AuthManager) CheckConnection(ctx context.Context, serverURL string) (AuthState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	normalizedURL := normalizeServerURL(serverURL)
	m.serverURL = normalizedURL

	if m.client.HasValidToken(normalizedURL) {
		m.state = AuthStateAuthenticated
		return m.state, nil
	}

	// Probe the server to discover auth requirements
	discovered := m.discoverAuthChallenge(ctx, serverURL)
	if discovered != nil {
		m.authChallenge = discovered.challenge
		m.resource = discovered.resource
		m.state = AuthStatePendingAuth
		return m.state, nil
	}

	// Could not determine auth requirements -- the server may not require
	// auth, or it may be unreachable. Return PendingAuth so the caller
	// can attempt a connection and let mcp-go detect 401 at that point.
	m.state = AuthStatePendingAuth
	return m.state, nil
}

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
func (m *AuthManager) discoverAuthChallenge(ctx context.Context, serverURL string) *discoveredAuth {
	httpClient := m.client.GetHTTPClient()

	// Try a HEAD request to the server endpoint to check for 401
	discovered := probeEndpoint(ctx, httpClient, serverURL)
	if discovered != nil {
		return discovered
	}

	// Fall back to RFC 9728 protected resource metadata discovery
	return discoverFromResourceMetadata(ctx, httpClient, serverURL)
}

// probeEndpoint sends a HEAD request to the server and parses any 401 WWW-Authenticate header.
func probeEndpoint(ctx context.Context, httpClient *http.Client, serverURL string) *discoveredAuth {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, serverURL, nil)
	if err != nil {
		return nil
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusUnauthorized {
		return nil
	}

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if wwwAuth == "" {
		return nil
	}

	challenge, err := pkgoauth.ParseWWWAuthenticate(wwwAuth)
	if err != nil {
		return nil
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
		return &discoveredAuth{challenge: challenge, resource: metadata.resourceIdentifier()}
	}

	if metadata != nil && metadata.issuer() != "" {
		challenge.Issuer = metadata.issuer()
		return &discoveredAuth{challenge: challenge, resource: metadata.resourceIdentifier()}
	}

	return nil
}

// discoverFromResourceMetadata fetches /.well-known/oauth-protected-resource from
// the server's base URL (RFC 9728) and extracts the authorization server issuer.
func discoverFromResourceMetadata(ctx context.Context, httpClient *http.Client, serverURL string) *discoveredAuth {
	baseURL := pkgoauth.NormalizeServerURL(serverURL)
	metadataURL := baseURL + "/.well-known/oauth-protected-resource"

	metadata := fetchResourceMetadata(ctx, httpClient, metadataURL)
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

// StartAuthFlow initiates the OAuth authentication flow.
// Returns the authorization URL that the user should open in their browser.
// This should only be called when in AuthStatePendingAuth.
func (m *AuthManager) StartAuthFlow(ctx context.Context) (string, error) {
	return m.startAuthFlowWithOptions(ctx, nil)
}

// StartAuthFlowSilent initiates a silent OAuth authentication flow using prompt=none.
// This attempts re-authentication without user interaction if the user has an active
// session at the IdP. The loginHint should be the user's email from a previous session.
//
// If silent auth fails (user needs to log in), WaitForAuth will return an error
// that can be detected with mcpoauth.IsSilentAuthError(). The caller should then
// fall back to interactive authentication via StartAuthFlow().
//
// This should only be called when in AuthStatePendingAuth.
func (m *AuthManager) StartAuthFlowSilent(ctx context.Context, loginHint, idTokenHint string) (string, error) {
	opts := &AuthFlowOptions{
		Silent:      true,
		LoginHint:   loginHint,
		IDTokenHint: idTokenHint,
	}
	return m.startAuthFlowWithOptions(ctx, opts)
}

// startAuthFlowWithOptions is the internal method that handles both regular and silent auth flows.
func (m *AuthManager) startAuthFlowWithOptions(ctx context.Context, opts *AuthFlowOptions) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state != AuthStatePendingAuth {
		return "", fmt.Errorf("cannot start auth flow in state: %s", m.state)
	}

	if m.authChallenge == nil {
		return "", errors.New("no auth challenge available")
	}

	issuerURL := m.authChallenge.Issuer
	if issuerURL == "" {
		return "", errors.New("no issuer URL in auth challenge")
	}

	// The server's own RFC 9728 metadata is the authority on its RFC 8707
	// resource identifier. Passing it here keeps the token bound to the
	// value the server validates against, instead of one derived from the
	// endpoint URL the user happened to type.
	flowOpts := AuthFlowOptions{}
	if opts != nil {
		flowOpts = *opts
	}
	if m.resource != "" {
		flowOpts.Resource = m.resource
	}

	authURL, waitFn, err := m.client.CompleteAuthFlowWithOptions(ctx, m.serverURL, issuerURL, &flowOpts)
	if err != nil {
		slog.Debug("Failed to start OAuth authentication flow",
			"server_url", m.serverURL,
			"issuer_url", issuerURL,
			"silent", opts != nil && opts.Silent,
			"error", err.Error(),
		)
		m.lastError = err
		return "", err
	}

	slog.Debug("OAuth authentication flow started",
		"server_url", m.serverURL,
		"issuer_url", issuerURL,
		"silent", opts != nil && opts.Silent,
	)

	m.authURL = authURL
	m.waitFunc = func() error {
		_, err := waitFn()
		return err
	}

	return authURL, nil
}

// WaitForAuth waits for the authentication flow to complete.
// This blocks until the user completes authentication or the context is cancelled.
func (m *AuthManager) WaitForAuth(ctx context.Context) error {
	m.mu.RLock()
	waitFn := m.waitFunc
	m.mu.RUnlock()

	if waitFn == nil {
		return errors.New("no auth flow in progress")
	}

	if err := waitFn(); err != nil {
		slog.Debug("OAuth authentication flow failed",
			"server_url", m.serverURL,
			"error", err.Error(),
		)
		m.mu.Lock()
		m.state = AuthStateError
		m.lastError = err
		m.mu.Unlock()
		return err
	}

	slog.Debug("OAuth authentication completed successfully",
		"server_url", m.serverURL,
	)

	m.mu.Lock()
	m.state = AuthStateAuthenticated
	m.authURL = ""
	m.waitFunc = nil
	m.mu.Unlock()

	return nil
}

// GetAccessToken returns the access token for the server.
// Token refresh is handled by mcp-go's transport layer, so this method
// simply reads the current token from the store.
func (m *AuthManager) GetAccessToken() (string, error) {
	m.mu.RLock()
	serverURL := m.serverURL
	state := m.state
	m.mu.RUnlock()

	if state != AuthStateAuthenticated {
		return "", fmt.Errorf("not authenticated (state: %s)", state)
	}

	token, err := m.client.GetToken(serverURL)
	if err != nil {
		return "", err
	}

	return token.AccessToken, nil
}

// GetBearerToken returns the token formatted as a Bearer authorization header value.
func (m *AuthManager) GetBearerToken() (string, error) {
	token, err := m.GetAccessToken()
	if err != nil {
		return "", err
	}
	return "Bearer " + token, nil
}

// GetState returns the current auth state.
func (m *AuthManager) GetState() AuthState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// GetAuthChallenge returns the current auth challenge (if in pending auth state).
func (m *AuthManager) GetAuthChallenge() *pkgoauth.AuthChallenge {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.authChallenge
}

// GetAuthURL returns the authorization URL (if auth flow has been started).
func (m *AuthManager) GetAuthURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.authURL
}

// GetLastError returns the last error that occurred.
func (m *AuthManager) GetLastError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastError
}

// GetServerURL returns the server URL being authenticated to.
func (m *AuthManager) GetServerURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.serverURL
}

// GetStoredToken returns the stored token for the current server.
// Returns nil if not authenticated or no token exists.
func (m *AuthManager) GetStoredToken() *StoredToken {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.serverURL == "" {
		return nil
	}

	return m.client.tokenStore.GetToken(m.serverURL)
}

// GetStoredTokenForEndpoint returns the stored token for a specific endpoint,
// including expired tokens. This is used for silent re-authentication where
// we need the id_token from an expired session for login hints.
// Note: No mutex is needed here - we only use the endpoint parameter, not struct fields.
func (m *AuthManager) GetStoredTokenForEndpoint(endpoint string) *StoredToken {
	normalizedURL := normalizeServerURL(endpoint)
	return m.client.tokenStore.GetTokenIncludingExpiring(normalizedURL)
}

// ClearToken clears the stored token for the current server.
func (m *AuthManager) ClearToken() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.serverURL == "" {
		return nil
	}

	m.state = AuthStateUnknown
	return m.client.ClearToken(m.serverURL)
}

// HasValidTokenForEndpoint checks if a valid token exists for the given endpoint.
// This method checks the filesystem for tokens that may have been created by
// external processes (e.g., 'muster auth login' CLI command).
// If a valid token is found, it updates the internal auth state to AuthStateAuthenticated.
// This enables the agent to detect CLI-based authentication and upgrade from pending auth state.
func (m *AuthManager) HasValidTokenForEndpoint(endpoint string) bool {
	// Normalize the endpoint URL for consistent token lookup
	normalizedURL := normalizeServerURL(endpoint)

	// Check if the client has a valid token (reads from filesystem if not in cache)
	if m.client.HasValidToken(normalizedURL) {
		m.mu.Lock()
		defer m.mu.Unlock()

		// Update internal state if we were in pending auth state
		if m.state == AuthStatePendingAuth || m.state == AuthStateUnknown {
			m.state = AuthStateAuthenticated
			m.serverURL = normalizedURL
			slog.Debug("Valid token detected for endpoint, updating auth state",
				"endpoint", endpoint,
				"state", m.state.String(),
			)
		}
		return true
	}
	return false
}

// HasCredentials reports whether usable credentials exist for the endpoint:
// either a non-expired access token or an expired token paired with a
// refresh token. Unlike HasValidTokenForEndpoint this does not update
// internal auth state because the token may still need to be refreshed.
func (m *AuthManager) HasCredentials(endpoint string) bool {
	normalizedURL := normalizeServerURL(endpoint)
	return m.client.HasCredentials(normalizedURL)
}

// Close cleans up resources.
func (m *AuthManager) Close() error {
	if m.client != nil {
		return m.client.Close()
	}
	return nil
}
