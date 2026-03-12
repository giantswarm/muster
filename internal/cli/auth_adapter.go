package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	mcpoauth "github.com/giantswarm/mcp-oauth"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/agent/oauth"
	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// CallbackPortEnvVar is the environment variable for configuring the OAuth callback port.
const CallbackPortEnvVar = "MUSTER_OAUTH_CALLBACK_PORT"

// DefaultCallbackPort is the default port for OAuth callbacks if not configured.
const DefaultCallbackPort = 3000

// DefaultConnectionCheckTimeout is the timeout for checking connection state and tokens.
const DefaultConnectionCheckTimeout = 5 * time.Second

// DefaultHTTPClientTimeout is the timeout for HTTP client operations.
const DefaultHTTPClientTimeout = 5 * time.Second

// AuthAdapter implements api.AuthHandler using internal/agent/oauth.
// It wraps the AuthManager and TokenStore to provide OAuth authentication
// for CLI commands following the project's service locator pattern.
//
// Thread-safe: All public methods are safe for concurrent use.
type AuthAdapter struct {
	// mu protects concurrent access to the managers map.
	mu sync.RWMutex

	// managers handles OAuth flows and state management.
	// Each login creates a new manager instance for that specific endpoint.
	managers map[string]*oauth.AuthManager

	// tokenStorageDir is the directory for storing tokens.
	tokenStorageDir string

	// noSilentRefresh disables silent re-authentication attempts.
	// When true, Login() always uses interactive authentication.
	noSilentRefresh bool
}

// AuthAdapterConfig provides configuration options for the AuthAdapter.
type AuthAdapterConfig struct {
	// TokenStorageDir is the directory for storing OAuth tokens.
	// If empty, defaults to ~/.config/muster/tokens
	TokenStorageDir string

	// NoSilentRefresh disables silent re-authentication attempts.
	// When true, Login() always uses interactive authentication.
	NoSilentRefresh bool
}

// NewAuthAdapter creates a new auth adapter with default configuration.
func NewAuthAdapter() (*AuthAdapter, error) {
	return NewAuthAdapterWithConfig(AuthAdapterConfig{})
}

// NewAuthAdapterWithConfig creates a new auth adapter with the specified configuration.
// This is useful for testing or advanced use cases where custom token storage is needed.
func NewAuthAdapterWithConfig(cfg AuthAdapterConfig) (*AuthAdapter, error) {
	tokenDir := cfg.TokenStorageDir
	if tokenDir == "" {
		var err error
		tokenDir, err = pkgoauth.DefaultTokenDir()
		if err != nil {
			return nil, err
		}
	}

	// Ensure the token directory exists
	if err := os.MkdirAll(tokenDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create token storage directory: %w", err)
	}

	adapter := &AuthAdapter{
		managers:        make(map[string]*oauth.AuthManager),
		tokenStorageDir: tokenDir,
		noSilentRefresh: cfg.NoSilentRefresh,
	}

	return adapter, nil
}

// SetNoSilentRefresh enables or disables silent re-authentication.
// When disabled (the default), Login() attempts silent re-auth before interactive login.
// When enabled, Login() always uses interactive authentication.
func (a *AuthAdapter) SetNoSilentRefresh(noSilent bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.noSilentRefresh = noSilent
}

// Register registers the adapter with the API layer.
func (a *AuthAdapter) Register() {
	api.RegisterAuthHandler(a)
}

// getOrCreateManager gets or creates an AuthManager for the given endpoint.
func (a *AuthAdapter) getOrCreateManager(endpoint string) (*oauth.AuthManager, error) {
	normalizedEndpoint := normalizeEndpoint(endpoint)

	// Check with read lock first
	a.mu.RLock()
	if mgr, ok := a.managers[normalizedEndpoint]; ok {
		a.mu.RUnlock()
		return mgr, nil
	}
	a.mu.RUnlock()

	// Upgrade to write lock to create new manager
	a.mu.Lock()
	defer a.mu.Unlock()

	// Double-check after acquiring write lock
	if mgr, ok := a.managers[normalizedEndpoint]; ok {
		return mgr, nil
	}

	mgr, err := oauth.NewAuthManager(oauth.AuthManagerConfig{
		CallbackPort:    getCallbackPort(),
		TokenStorageDir: a.tokenStorageDir,
		FileMode:        true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create auth manager: %w", err)
	}

	a.managers[normalizedEndpoint] = mgr
	return mgr, nil
}

// getCallbackPort returns the OAuth callback port from environment or default.
func getCallbackPort() int {
	if portStr := os.Getenv(CallbackPortEnvVar); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil && port > 0 && port < 65536 {
			return port
		}
	}
	return DefaultCallbackPort
}

// CheckAuthRequired probes the endpoint to determine if OAuth is required.
func (a *AuthAdapter) CheckAuthRequired(ctx context.Context, endpoint string) (bool, error) {
	mgr, err := a.getOrCreateManager(endpoint)
	if err != nil {
		return false, err
	}

	state, err := mgr.CheckConnection(ctx, endpoint)
	if err != nil {
		// If we got an error but state indicates pending auth, auth is required
		if state == oauth.AuthStatePendingAuth {
			return true, nil
		}
		return false, err
	}

	return state == oauth.AuthStatePendingAuth, nil
}

// HasCredentials reports whether usable credentials exist for the endpoint:
// a valid access token or an expired token with a refresh token.
func (a *AuthAdapter) HasCredentials(endpoint string) bool {
	mgr, err := a.getOrCreateManager(endpoint)
	if err != nil {
		return false
	}
	return mgr.HasCredentials(endpoint)
}

// GetBearerToken returns a valid Bearer token for the endpoint.
// Token refresh is handled by mcp-go's transport layer via AgentTokenStore.
func (a *AuthAdapter) GetBearerToken(endpoint string) (string, error) {
	mgr, err := a.getOrCreateManager(endpoint)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectionCheckTimeout)
	defer cancel()

	state, _ := mgr.CheckConnection(ctx, endpoint)
	if state != oauth.AuthStateAuthenticated {
		return "", &AuthRequiredError{Endpoint: endpoint}
	}

	token, err := mgr.GetBearerToken()
	if err != nil {
		return "", &AuthRequiredError{Endpoint: endpoint}
	}

	return token, nil
}

// Login initiates the OAuth flow for the given endpoint.
// If a previous session exists, it first attempts silent re-authentication
// using prompt=none. If silent auth fails, it falls back to interactive login.
func (a *AuthAdapter) Login(ctx context.Context, endpoint string) error {
	mgr, err := a.getOrCreateManager(endpoint)
	if err != nil {
		return err
	}

	// Check connection to get auth challenge
	state, err := mgr.CheckConnection(ctx, endpoint)
	if err != nil && state != oauth.AuthStatePendingAuth {
		return fmt.Errorf("failed to check connection: %w", err)
	}

	if state == oauth.AuthStateAuthenticated {
		// Already authenticated
		return nil
	}

	if state != oauth.AuthStatePendingAuth {
		// No auth required
		return nil
	}

	// Check if we have a stored token that might indicate a previous session
	// This enables silent re-authentication when the IdP session is still valid
	a.mu.RLock()
	noSilent := a.noSilentRefresh
	a.mu.RUnlock()

	if !noSilent {
		// Use GetStoredTokenForEndpoint to get token including expired ones
		// We need the id_token from expired tokens for silent re-auth hints
		storedToken := mgr.GetStoredTokenForEndpoint(endpoint)
		if storedToken != nil {
			// We have a previous session - try silent re-authentication
			if err := a.trySilentReAuth(ctx, mgr, storedToken, endpoint); err == nil {
				return nil
			}
			// Silent auth failed - need to re-create manager for clean state
			// because WaitForAuth sets state to AuthStateError on failure
			a.mu.Lock()
			delete(a.managers, normalizeEndpoint(endpoint))
			a.mu.Unlock()

			// Re-create manager and check connection to get back to AuthStatePendingAuth
			mgr, err = a.getOrCreateManager(endpoint)
			if err != nil {
				return err
			}
			state, err = mgr.CheckConnection(ctx, endpoint)
			if err != nil && state != oauth.AuthStatePendingAuth {
				return fmt.Errorf("failed to check connection after silent auth: %w", err)
			}
			if state != oauth.AuthStatePendingAuth {
				// Somehow we're authenticated or don't need auth
				return nil
			}
		}
	}

	// Interactive authentication
	return a.interactiveLogin(ctx, mgr, endpoint)
}

// trySilentReAuth attempts silent re-authentication using prompt=none.
// This is used when the user has a previous session and may still have an
// active session at the IdP, avoiding the need for manual re-authentication.
func (a *AuthAdapter) trySilentReAuth(ctx context.Context, mgr *oauth.AuthManager, storedToken *oauth.StoredToken, endpoint string) error {
	logging.Debug("AuthAdapter", "Attempting silent re-authentication for %s", endpoint)

	// Extract login hint from previous session
	var loginHint string
	var idTokenHint string
	if storedToken.IDToken != "" {
		claims := parseIDTokenClaims(storedToken.IDToken)
		loginHint = claims.Email
		idTokenHint = storedToken.IDToken
	}

	// Start silent auth flow
	authURL, err := mgr.StartAuthFlowSilent(ctx, loginHint, idTokenHint)
	if err != nil {
		logging.Debug("AuthAdapter", "Failed to start silent auth flow: %v", err)
		return err
	}

	// Open browser for silent auth (should redirect quickly without UI)
	if err := oauth.OpenBrowser(authURL); err != nil {
		logging.Debug("AuthAdapter", "Failed to open browser for silent auth: %v", err)
		return err
	}

	// Wait for silent auth to complete
	if err := mgr.WaitForAuth(ctx); err != nil {
		// Check if this is a silent auth failure (login_required, consent_required, etc.)
		if mcpoauth.IsSilentAuthError(err) {
			logging.Debug("AuthAdapter", "Silent re-authentication failed, IdP requires interaction: %v", err)
			return err
		}
		// Other errors (network, timeout, etc.)
		logging.Debug("AuthAdapter", "Silent re-authentication failed: %v", err)
		return err
	}

	fmt.Printf("\nSuccessfully re-authenticated to %s (silent)\n", endpoint)
	return nil
}

// interactiveLogin performs the standard interactive OAuth login flow.
func (a *AuthAdapter) interactiveLogin(ctx context.Context, mgr *oauth.AuthManager, endpoint string) error {
	// Start auth flow
	authURL, err := mgr.StartAuthFlow(ctx)
	if err != nil {
		// Check for port-in-use errors and provide helpful guidance
		if isPortInUseError(err) {
			port := getCallbackPort()
			return &AuthFailedError{
				Endpoint: endpoint,
				Reason:   fmt.Errorf("callback port %d is already in use. Please free the port and try again", port),
			}
		}
		return &AuthFailedError{Endpoint: endpoint, Reason: err}
	}

	// Try to open browser, only show URL if it fails
	fmt.Print("Opening browser for authentication...")

	if err := oauth.OpenBrowser(authURL); err != nil {
		fmt.Println(" failed")
		fmt.Printf("Please open this URL in your browser:\n  %s\n\n", authURL)
	} else {
		fmt.Println(" done")
	}

	fmt.Println("Waiting for authentication to complete...")

	// Wait for auth to complete
	if err := mgr.WaitForAuth(ctx); err != nil {
		return &AuthFailedError{Endpoint: endpoint, Reason: err}
	}

	fmt.Printf("\nSuccessfully authenticated to %s\n", endpoint)
	fmt.Println("SSO-enabled servers will be connected automatically on first request.")
	return nil
}

// LoginWithIssuer initiates the OAuth flow with a known issuer.
func (a *AuthAdapter) LoginWithIssuer(ctx context.Context, endpoint, issuerURL string) error {
	// For now, we use the same flow as Login since the AuthManager
	// will discover the issuer during CheckConnection
	return a.Login(ctx, endpoint)
}

// Logout clears stored tokens for the endpoint.
// It first revokes the refresh token via RFC 7009 (best-effort), then performs local cleanup.
func (a *AuthAdapter) Logout(endpoint string) error {
	normalizedEndpoint := normalizeEndpoint(endpoint)

	// Read the stored refresh token before any cleanup so we can revoke it.
	store, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
		StorageDir: a.tokenStorageDir,
		FileMode:   true,
	})
	if err != nil {
		return fmt.Errorf("failed to create token store: %w", err)
	}

	// Best-effort refresh token revocation via POST /oauth/revoke (RFC 7009).
	// This must happen before local cleanup deletes the token file.
	if storedToken := store.GetTokenIncludingExpiring(normalizedEndpoint); storedToken != nil && storedToken.RefreshToken != "" {
		a.revokeRefreshToken(normalizedEndpoint, storedToken.RefreshToken)
	}

	// Remove manager from cache
	a.mu.Lock()
	if mgr, ok := a.managers[normalizedEndpoint]; ok {
		if err := mgr.Close(); err != nil {
			logging.Debug("AuthAdapter", "Error closing manager for %s: %v", normalizedEndpoint, err)
		}
		delete(a.managers, normalizedEndpoint)
	}
	a.mu.Unlock()

	// Clear the token directly from the token store.
	if err := store.DeleteToken(normalizedEndpoint); err != nil {
		return fmt.Errorf("failed to clear token: %w", err)
	}

	return nil
}

// LogoutAll clears all stored tokens.
// It revokes each endpoint's refresh token via RFC 7009, then calls DELETE /user-tokens
// to clear server-side downstream state.
func (a *AuthAdapter) LogoutAll() error {
	// Create a token store to read refresh tokens before cleanup.
	store, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
		StorageDir: a.tokenStorageDir,
		FileMode:   true,
	})
	if err != nil {
		return fmt.Errorf("failed to create token store: %w", err)
	}

	// Collect all known endpoints (from managers + token files) for revocation.
	endpoints := a.collectAllEndpoints()

	// Best-effort: revoke each endpoint's refresh token via POST /oauth/revoke.
	// Also track a valid Bearer token + endpoint for the DELETE /user-tokens call.
	var bearerToken, bearerEndpoint string
	for _, ep := range endpoints {
		storedToken := store.GetTokenIncludingExpiring(ep)
		if storedToken == nil {
			continue
		}
		if storedToken.RefreshToken != "" {
			a.revokeRefreshToken(ep, storedToken.RefreshToken)
		}
		// Use the first available access token for DELETE /user-tokens
		if bearerToken == "" && storedToken.AccessToken != "" {
			bearerToken = storedToken.AccessToken
			bearerEndpoint = ep
		}
	}

	// Best-effort: call DELETE /user-tokens with Bearer token to clear
	// server-side downstream state.
	if bearerToken != "" && bearerEndpoint != "" {
		a.deleteUserTokens(bearerEndpoint, bearerToken)
	}

	// Close all managers
	a.mu.Lock()
	for endpoint, mgr := range a.managers {
		if err := mgr.Close(); err != nil {
			logging.Debug("AuthAdapter", "Error closing manager for %s: %v", endpoint, err)
		}
	}
	a.managers = make(map[string]*oauth.AuthManager)
	a.mu.Unlock()

	return store.Clear()
}

// revokeRefreshToken revokes a refresh token at the endpoint's /oauth/revoke endpoint
// per RFC 7009. This is best-effort: errors are logged but do not prevent local cleanup.
func (a *AuthAdapter) revokeRefreshToken(endpoint, refreshToken string) {
	revokeURL := endpoint + "/oauth/revoke"

	v := url.Values{}
	v.Set("token", refreshToken)
	v.Set("token_type_hint", "refresh_token")
	body := strings.NewReader(v.Encode())
	req, err := http.NewRequest(http.MethodPost, revokeURL, body)
	if err != nil {
		logging.Warn("AuthAdapter", "Failed to create revoke request for %s: %v", endpoint, err)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: DefaultHTTPClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		logging.Warn("AuthAdapter", "Failed to revoke refresh token for %s (server may be unreachable): %v", endpoint, err)
		return
	}
	defer resp.Body.Close()

	// RFC 7009: server returns 200 regardless of whether the token was found.
	if resp.StatusCode == http.StatusOK {
		logging.Info("AuthAdapter", "Refresh token revoked for %s", endpoint)
	} else {
		logging.Warn("AuthAdapter", "Refresh token revocation returned status %d for %s", resp.StatusCode, endpoint)
	}
}

// deleteUserTokens sends DELETE /user-tokens with a Bearer token to clear all
// server-side downstream state for the current user. Returns true if the server
// acknowledged the request (204), false otherwise (e.g., 404 for old servers).
func (a *AuthAdapter) deleteUserTokens(endpoint, accessToken string) bool {
	url := endpoint + "/user-tokens"

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		logging.Warn("AuthAdapter", "Failed to create DELETE /user-tokens request for %s: %v", endpoint, err)
		return false
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: DefaultHTTPClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		logging.Warn("AuthAdapter", "Failed to call DELETE /user-tokens for %s: %v", endpoint, err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		logging.Info("AuthAdapter", "Server-side user tokens deleted for %s", endpoint)
		return true
	}

	logging.Warn("AuthAdapter", "DELETE /user-tokens returned status %d for %s", resp.StatusCode, endpoint)
	return false
}

// collectAllEndpoints returns a deduplicated list of all known endpoint URLs
// from managers and token files.
func (a *AuthAdapter) collectAllEndpoints() []string {
	seen := make(map[string]bool)

	a.mu.RLock()
	for endpoint := range a.managers {
		seen[endpoint] = true
	}
	a.mu.RUnlock()

	tokenFiles, _ := a.listTokenFiles()
	for _, tf := range tokenFiles {
		normalized := normalizeEndpoint(tf.ServerURL)
		seen[normalized] = true
	}

	endpoints := make([]string, 0, len(seen))
	for ep := range seen {
		endpoints = append(endpoints, ep)
	}
	return endpoints
}

// GetStatus returns authentication status for all known endpoints.
func (a *AuthAdapter) GetStatus() []api.AuthStatus {
	var statuses []api.AuthStatus

	// Get status from all known managers
	a.mu.RLock()
	for endpoint, mgr := range a.managers {
		status := a.getStatusFromManager(endpoint, mgr)
		statuses = append(statuses, status)
	}
	a.mu.RUnlock()

	// Also scan token files to find endpoints we don't have managers for
	tokenFiles, _ := a.listTokenFiles()
	for _, tokenFile := range tokenFiles {
		// Check if we already have this endpoint
		found := false
		for _, s := range statuses {
			if s.Endpoint == tokenFile.ServerURL {
				found = true
				break
			}
		}
		if !found {
			statuses = append(statuses, api.AuthStatus{
				Endpoint:      tokenFile.ServerURL,
				Authenticated: true, // If we have a token file, we're authenticated
				ExpiresAt:     tokenFile.Expiry,
				IssuerURL:     tokenFile.IssuerURL,
			})
		}
	}

	return statuses
}

// GetStatusForEndpoint returns authentication status for a specific endpoint.
func (a *AuthAdapter) GetStatusForEndpoint(endpoint string) *api.AuthStatus {
	mgr, err := a.getOrCreateManager(endpoint)
	if err != nil {
		return &api.AuthStatus{
			Endpoint: endpoint,
			Error:    err.Error(),
		}
	}

	// Check connection to properly initialize the state and load stored tokens
	ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectionCheckTimeout)
	defer cancel()
	_, _ = mgr.CheckConnection(ctx, endpoint)

	status := a.getStatusFromManager(endpoint, mgr)
	return &status
}

func (a *AuthAdapter) getStatusFromManager(endpoint string, mgr *oauth.AuthManager) api.AuthStatus {
	state := mgr.GetState()

	status := api.AuthStatus{
		Endpoint: endpoint,
	}

	switch state {
	case oauth.AuthStateAuthenticated:
		status.Authenticated = true
		// Get token info if available
		if storedToken := mgr.GetStoredToken(); storedToken != nil {
			status.ExpiresAt = storedToken.Expiry
			status.IssuerURL = storedToken.IssuerURL
			status.HasRefreshToken = storedToken.RefreshToken != ""
			if status.HasRefreshToken && !storedToken.CreatedAt.IsZero() {
				status.RefreshExpiresAt = storedToken.CreatedAt.Add(pkgoauth.DefaultSessionDuration)
			}
			// Extract identity from ID token if available
			if storedToken.IDToken != "" {
				claims := parseIDTokenClaims(storedToken.IDToken)
				status.Subject = claims.Subject
				status.Email = claims.Email
			}
		} else if challenge := mgr.GetAuthChallenge(); challenge != nil {
			// Fallback to auth challenge for issuer
			status.IssuerURL = challenge.Issuer
		}
	case oauth.AuthStatePendingAuth:
		status.Authenticated = false
	case oauth.AuthStateError:
		if err := mgr.GetLastError(); err != nil {
			status.Error = err.Error()
		}
	}

	return status
}

// parseIDTokenClaims extracts identity claims from a JWT ID token.
// This performs basic JWT parsing without validation (validation is done at login time).
func parseIDTokenClaims(idToken string) pkgoauth.IDTokenClaims {
	var claims pkgoauth.IDTokenClaims

	// JWT has 3 parts: header.payload.signature
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return claims
	}

	// Decode the payload (second part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims
	}

	// Parse claims
	_ = json.Unmarshal(payload, &claims)
	return claims
}

// InvalidateCache removes the cached auth manager for an endpoint.
// This forces the next GetStatusForEndpoint call to create a fresh manager
// that reads the latest token from the file store. This is needed after
// mcp-go's transport refreshes a token, since the refreshed token is
// persisted to file by AgentTokenStore but the AuthAdapter's in-memory
// TokenStore cache is stale.
func (a *AuthAdapter) InvalidateCache(endpoint string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	normalizedEndpoint := normalizeEndpoint(endpoint)
	if mgr, ok := a.managers[normalizedEndpoint]; ok {
		_ = mgr.Close()
		delete(a.managers, normalizedEndpoint)
	}
}

// Close cleans up any resources held by the auth adapter.
func (a *AuthAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	var errs []error
	for _, mgr := range a.managers {
		if err := mgr.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	a.managers = make(map[string]*oauth.AuthManager)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// tokenFileInfo holds basic info about a stored token.
type tokenFileInfo struct {
	ServerURL string
	IssuerURL string
	Expiry    time.Time
}

// listTokenFiles scans the token directory for stored tokens.
func (a *AuthAdapter) listTokenFiles() ([]tokenFileInfo, error) {
	entries, err := os.ReadDir(a.tokenStorageDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var tokens []tokenFileInfo
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		// Read token file to get server URL
		filePath := filepath.Join(a.tokenStorageDir, entry.Name())
		token, err := readTokenFile(filePath)
		if err != nil {
			continue
		}
		tokens = append(tokens, *token)
	}

	return tokens, nil
}

// readTokenFile reads a token file and extracts basic info.
func readTokenFile(filePath string) (*tokenFileInfo, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var token oauth.StoredToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}

	return &tokenFileInfo{
		ServerURL: token.ServerURL,
		IssuerURL: token.IssuerURL,
		Expiry:    token.Expiry,
	}, nil
}

// normalizeEndpoint normalizes an endpoint URL for consistent key usage.
// This is a thin wrapper around pkgoauth.NormalizeServerURL for local use.
func normalizeEndpoint(endpoint string) string {
	return pkgoauth.NormalizeServerURL(endpoint)
}

// isPortInUseError checks if an error is related to a port being in use.
func isPortInUseError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "address already in use")
}
