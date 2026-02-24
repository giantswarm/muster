package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
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
	challenge := m.discoverAuthChallenge(ctx, serverURL)
	if challenge != nil {
		m.authChallenge = challenge
		m.state = AuthStatePendingAuth
		return m.state, nil
	}

	// Could not determine auth requirements -- the server may not require
	// auth, or it may be unreachable. Return PendingAuth so the caller
	// can attempt a connection and let mcp-go detect 401 at that point.
	m.state = AuthStatePendingAuth
	return m.state, nil
}

// discoverAuthChallenge probes the server to discover OAuth auth requirements.
// It first sends a HEAD request to the endpoint to check for a 401 + WWW-Authenticate
// header. If that yields an issuer, it's used directly. Otherwise it falls back to
// fetching /.well-known/oauth-protected-resource (RFC 9728).
func (m *AuthManager) discoverAuthChallenge(ctx context.Context, serverURL string) *pkgoauth.AuthChallenge {
	httpClient := m.client.GetHTTPClient()

	// Try a HEAD request to the server endpoint to check for 401
	challenge := probeEndpoint(ctx, httpClient, serverURL)
	if challenge != nil {
		return challenge
	}

	// Fall back to RFC 9728 protected resource metadata discovery
	return discoverFromResourceMetadata(ctx, httpClient, serverURL)
}

// probeEndpoint sends a HEAD request to the server and parses any 401 WWW-Authenticate header.
func probeEndpoint(ctx context.Context, httpClient *http.Client, serverURL string) *pkgoauth.AuthChallenge {
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
		resp.Body.Close()
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

	// If we have a direct issuer, use it
	if challenge.GetIssuer() != "" {
		return challenge
	}

	// If we have a resource_metadata URL, fetch it
	if challenge.ResourceMetadataURL != "" {
		if issuer := fetchIssuerFromResourceMetadata(ctx, httpClient, challenge.ResourceMetadataURL); issuer != "" {
			challenge.Issuer = issuer
			return challenge
		}
	}

	return nil
}

// discoverFromResourceMetadata fetches /.well-known/oauth-protected-resource from
// the server's base URL (RFC 9728) and extracts the authorization server issuer.
func discoverFromResourceMetadata(ctx context.Context, httpClient *http.Client, serverURL string) *pkgoauth.AuthChallenge {
	baseURL := strings.TrimSuffix(serverURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/mcp")
	baseURL = strings.TrimSuffix(baseURL, "/sse")
	metadataURL := baseURL + "/.well-known/oauth-protected-resource"

	issuer := fetchIssuerFromResourceMetadata(ctx, httpClient, metadataURL)
	if issuer == "" {
		return nil
	}

	return &pkgoauth.AuthChallenge{
		Scheme: "Bearer",
		Issuer: issuer,
	}
}

// protectedResourceMetadata is the JSON structure returned by RFC 9728 endpoints.
type protectedResourceMetadata struct {
	AuthorizationServers []string `json:"authorization_servers"`
}

// fetchIssuerFromResourceMetadata fetches a resource metadata URL and extracts
// the first authorization server as the issuer.
func fetchIssuerFromResourceMetadata(ctx context.Context, httpClient *http.Client, metadataURL string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return ""
	}

	var meta protectedResourceMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return ""
	}

	if len(meta.AuthorizationServers) > 0 {
		return meta.AuthorizationServers[0]
	}
	return ""
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

	authURL, waitFn, err := m.client.CompleteAuthFlowWithOptions(ctx, m.serverURL, issuerURL, opts)
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
