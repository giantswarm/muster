package oauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
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
	// probeErr is why the last discovery probe got no response from the
	// server (an untrusted certificate, a refused connection). It explains
	// a missing challenge, so the auth flow reports it instead.
	probeErr error
	waitFunc func() error // Called when waiting for auth to complete
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
	// The indicator belongs to the server this call probes, so a value left
	// from an earlier server must not survive into the next login.
	m.resource = ""

	if m.client.HasValidToken(normalizedURL) {
		m.state = AuthStateAuthenticated
		return m.state, nil
	}

	// Probe the server to discover auth requirements
	discovered := m.probe(ctx, serverURL)
	if discovered != nil {
		m.applyDiscovery(discovered, serverURL)
		return m.state, nil
	}

	// Could not determine auth requirements -- the server may not require
	// auth, or it may be unreachable. Return PendingAuth so the caller
	// can attempt a connection and let mcp-go detect 401 at that point.
	m.state = AuthStatePendingAuth
	return m.state, nil
}

// RequireAuth prepares a new authorization flow for a server whose stored
// credentials may still be valid. Unlike CheckConnection it ignores the stored
// token: it probes the server for its auth challenge and enters
// AuthStatePendingAuth so StartAuthFlow can run. The stored token stays in
// place -- the flow's token exchange replaces it -- so a failed or abandoned
// flow leaves the existing session usable.
func (m *AuthManager) RequireAuth(ctx context.Context, serverURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.serverURL = normalizeServerURL(serverURL)
	m.resource = ""

	discovered := m.probe(ctx, serverURL)
	if m.probeErr != nil {
		return m.probeErr
	}
	if discovered == nil {
		return fmt.Errorf("%s did not issue an authentication challenge", serverURL)
	}
	m.applyDiscovery(discovered, serverURL)
	return nil
}

// probe runs the discovery probe and records why it failed to reach the
// server, if it did. The caller holds m.mu.
func (m *AuthManager) probe(ctx context.Context, serverURL string) *discoveredAuth {
	m.authChallenge = nil
	discovered, err := m.discoverAuthChallenge(ctx, serverURL)
	m.probeErr = nil
	if err != nil {
		m.probeErr = fmt.Errorf("cannot reach %s: %w", serverURL, err)
		slog.Debug("Auth discovery probe got no response",
			"server_url", serverURL,
			"error", err,
		)
	}
	return discovered
}

// applyDiscovery records what a discovery probe learned and enters
// AuthStatePendingAuth. The caller holds m.mu.
func (m *AuthManager) applyDiscovery(discovered *discoveredAuth, serverURL string) {
	m.authChallenge = discovered.challenge
	// The URL is the one the caller passed, not the normalized form,
	// because mcp-go derives its own indicator from the endpoint it
	// connects to and sends that value on token refresh.
	resource, err := resourceIndicator(discovered.resource, serverURL)
	if err != nil {
		slog.Warn("Cannot derive an RFC 8707 resource indicator",
			"server_url", serverURL,
			"error", err,
		)
	}
	m.resource = resource
	m.state = AuthStatePendingAuth
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
		if m.probeErr != nil {
			return "", m.probeErr
		}
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
