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

// normalizeServerURL normalizes a server URL by stripping transport-specific
// path suffixes (/mcp, /sse) to get the base server URL. This ensures consistent
// token storage and OAuth metadata discovery regardless of which endpoint path
// is used when connecting.
func normalizeServerURL(serverURL string) string {
	serverURL = strings.TrimSuffix(serverURL, "/")
	serverURL = strings.TrimSuffix(serverURL, "/mcp")
	serverURL = strings.TrimSuffix(serverURL, "/sse")
	return serverURL
}

// AuthManager manages OAuth authentication for the Muster Agent.
// It handles 401 detection, auth flow orchestration, and state transitions.
type AuthManager struct {
	mu            sync.RWMutex
	client        *Client
	state         AuthState
	serverURL     string
	authChallenge *AuthChallenge
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

// CheckConnection attempts to connect to the server and detect auth requirements.
// It returns the auth state and any error that occurred.
//
// If a 401 is received, the manager transitions to AuthStatePendingAuth and
// extracts the auth challenge from the WWW-Authenticate header.
func (m *AuthManager) CheckConnection(ctx context.Context, serverURL string) (AuthState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Normalize server URL for consistent token storage (strip /mcp, /sse suffixes)
	normalizedURL := normalizeServerURL(serverURL)
	m.serverURL = normalizedURL

	// First, check if we have a valid token
	if m.client.HasValidToken(normalizedURL) {
		m.state = AuthStateAuthenticated
		return m.state, nil
	}

	// Try to make a request to the server to detect 401
	challenge, err := m.probeServerAuth(ctx, serverURL)
	if err != nil {
		if errors.Is(err, ErrAuthRequired) {
			// Server requires auth - we got a 401 response
			if challenge != nil && challenge.Issuer != "" {
				// Got a proper WWW-Authenticate header with OAuth info including issuer
				slog.Info("OAuth authentication required for Muster Server",
					"server_url", serverURL,
					"issuer", challenge.Issuer,
					"realm", challenge.Realm,
				)
				m.state = AuthStatePendingAuth
				m.authChallenge = challenge
				return m.state, nil
			}

			// Got 401 but either no WWW-Authenticate header or no issuer in it
			// Try to discover OAuth metadata from well-known endpoints
			if challenge != nil {
				slog.Info("Server returned 401 with WWW-Authenticate but no issuer, attempting to discover OAuth metadata",
					"server_url", serverURL,
					"resource_metadata_url", challenge.ResourceMetadataURL,
				)
			} else {
				slog.Info("Server returned 401 without WWW-Authenticate header, attempting to discover OAuth metadata",
					"server_url", serverURL,
				)
			}

			discoveredChallenge, discoverErr := m.discoverOAuthMetadata(ctx, serverURL)
			if discoverErr == nil && discoveredChallenge != nil {
				slog.Info("Discovered OAuth metadata for Muster Server",
					"server_url", serverURL,
					"issuer", discoveredChallenge.Issuer,
				)
				m.state = AuthStatePendingAuth
				m.authChallenge = discoveredChallenge
				return m.state, nil
			}

			// Could not discover OAuth metadata
			slog.Warn("Server requires authentication but OAuth metadata could not be discovered",
				"server_url", serverURL,
				"discover_error", discoverErr,
			)
			m.state = AuthStateError
			m.lastError = fmt.Errorf("server requires authentication but OAuth metadata could not be discovered: %w", err)
			return m.state, m.lastError
		}

		slog.Warn("Failed to probe server authentication status",
			"server_url", serverURL,
			"error", err.Error(),
		)
		m.state = AuthStateError
		m.lastError = err
		return m.state, err
	}

	// Probe returned nil, nil - server responded without 401.
	// This means either auth is not required, or the server doesn't protect
	// the probe endpoints. We'll return AuthStateUnknown and let the caller
	// try a direct connection.
	slog.Debug("Server probe succeeded without 401, auth may not be required",
		"server_url", serverURL,
	)
	m.state = AuthStateUnknown
	return m.state, nil
}

// probeServerAuth probes the server to detect authentication requirements.
// Returns an AuthChallenge if 401 is received, nil otherwise.
func (m *AuthManager) probeServerAuth(ctx context.Context, serverURL string) (*AuthChallenge, error) {
	// Normalize to base URL first, then construct probe URLs
	baseURL := normalizeServerURL(serverURL)

	// Try a request to the MCP endpoint
	// The actual endpoint depends on the transport type
	probeURLs := []string{
		baseURL + "/mcp",
		baseURL + "/sse",
		baseURL,
	}

	// Reuse the HTTP client from the OAuth client to avoid resource leaks
	httpClient := m.client.GetHTTPClient()

	for _, probeURL := range probeURLs {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
		if err != nil {
			continue
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			continue
		}

		// Extract needed data before closing the body to avoid defer in loop
		statusCode := resp.StatusCode
		var challenge *AuthChallenge
		if statusCode == http.StatusUnauthorized {
			challenge = ExtractAuthChallengeFromResponse(resp)
		}

		// Drain and close body immediately (not deferred) to avoid memory leak
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if statusCode == http.StatusUnauthorized {
			if challenge != nil {
				return challenge, ErrAuthRequired
			}
			return nil, ErrAuthRequired
		}

		// Server responded without 401 - might not require auth
		// or might require auth at a later stage
		if statusCode == http.StatusOK || statusCode == http.StatusMethodNotAllowed {
			return nil, nil
		}
	}

	// Couldn't determine auth status
	return nil, fmt.Errorf("failed to probe server authentication status")
}

// discoverOAuthMetadata attempts to discover OAuth metadata from well-known endpoints.
// This is used when the server returns 401 without a WWW-Authenticate header.
func (m *AuthManager) discoverOAuthMetadata(ctx context.Context, serverURL string) (*AuthChallenge, error) {
	// Normalize to base URL for well-known discovery
	baseURL := normalizeServerURL(serverURL)
	httpClient := m.client.GetHTTPClient()

	// Try the OAuth Protected Resource Metadata endpoint (RFC 9728)
	metadataURL := baseURL + "/.well-known/oauth-protected-resource"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OAuth metadata endpoint returned status %d", resp.StatusCode)
	}

	// Parse the metadata response
	var metadata struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata response: %w", err)
	}

	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse OAuth metadata: %w", err)
	}

	if len(metadata.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization servers found in OAuth metadata")
	}

	// Use the first authorization server as the issuer
	issuer := metadata.AuthorizationServers[0]

	return &AuthChallenge{
		Issuer: issuer,
		Realm:  issuer,
	}, nil
}

// StartAuthFlow initiates the OAuth authentication flow.
// Returns the authorization URL that the user should open in their browser.
// This should only be called when in AuthStatePendingAuth.
func (m *AuthManager) StartAuthFlow(ctx context.Context) (string, error) {
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

	authURL, waitFn, err := m.client.CompleteAuthFlow(ctx, m.serverURL, issuerURL)
	if err != nil {
		slog.Warn("Failed to start OAuth authentication flow",
			"server_url", m.serverURL,
			"issuer_url", issuerURL,
			"error", err.Error(),
		)
		m.lastError = err
		return "", err
	}

	slog.Info("OAuth authentication flow started",
		"server_url", m.serverURL,
		"issuer_url", issuerURL,
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
		slog.Warn("OAuth authentication flow failed",
			"server_url", m.serverURL,
			"error", err.Error(),
		)
		m.mu.Lock()
		m.state = AuthStateError
		m.lastError = err
		m.mu.Unlock()
		return err
	}

	slog.Info("OAuth authentication completed successfully",
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
// Returns an error if not authenticated.
func (m *AuthManager) GetAccessToken() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.state != AuthStateAuthenticated {
		return "", fmt.Errorf("not authenticated (state: %s)", m.state)
	}

	token, err := m.client.GetToken(m.serverURL)
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
func (m *AuthManager) GetAuthChallenge() *AuthChallenge {
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

// Close cleans up resources.
func (m *AuthManager) Close() error {
	if m.client != nil {
		return m.client.Close()
	}
	return nil
}
