package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"muster/internal/agent/oauth"
	"muster/internal/api"
	"muster/pkg/logging"
	pkgoauth "muster/pkg/oauth"
)

// DefaultTokenStorageDir is the default directory for storing OAuth tokens.
const DefaultTokenStorageDir = ".config/muster/tokens"

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
	// mu protects concurrent access to managers map.
	mu sync.RWMutex

	// managers handles OAuth flows and state management.
	// Each login creates a new manager instance for that specific endpoint.
	managers map[string]*oauth.AuthManager

	// tokenStorageDir is the directory for storing tokens.
	tokenStorageDir string
}

// NewAuthAdapter creates a new auth adapter with default configuration.
func NewAuthAdapter() (*AuthAdapter, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	tokenDir := filepath.Join(homeDir, DefaultTokenStorageDir)

	return &AuthAdapter{
		managers:        make(map[string]*oauth.AuthManager),
		tokenStorageDir: tokenDir,
	}, nil
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

// HasValidToken checks if a valid cached token exists for the endpoint.
func (a *AuthAdapter) HasValidToken(endpoint string) bool {
	mgr, err := a.getOrCreateManager(endpoint)
	if err != nil {
		return false
	}

	// Check connection will check for valid tokens
	ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectionCheckTimeout)
	defer cancel()

	state, _ := mgr.CheckConnection(ctx, endpoint)
	return state == oauth.AuthStateAuthenticated
}

// GetBearerToken returns a valid Bearer token for the endpoint.
// It will automatically refresh the token if it's about to expire and a refresh token is available.
func (a *AuthAdapter) GetBearerToken(endpoint string) (string, error) {
	mgr, err := a.getOrCreateManager(endpoint)
	if err != nil {
		return "", err
	}

	// First check if we need to check connection state
	ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectionCheckTimeout)
	defer cancel()

	state, _ := mgr.CheckConnection(ctx, endpoint)
	if state != oauth.AuthStateAuthenticated {
		return "", &AuthRequiredError{Endpoint: endpoint}
	}

	// Try to proactively refresh the token if it's about to expire
	// This is silent - if refresh fails, we'll try with the existing token
	refreshed, err := a.tryRefreshToken(ctx, endpoint)
	if err != nil {
		logging.Debug("AuthAdapter", "Proactive token refresh failed: %v", err)
	} else if refreshed {
		logging.Debug("AuthAdapter", "Token proactively refreshed for %s", endpoint)
	}

	token, err := mgr.GetBearerToken()
	if err != nil {
		return "", &AuthRequiredError{Endpoint: endpoint}
	}

	return token, nil
}

// Login initiates the OAuth flow for the given endpoint.
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
	return nil
}

// LoginWithIssuer initiates the OAuth flow with a known issuer.
func (a *AuthAdapter) LoginWithIssuer(ctx context.Context, endpoint, issuerURL string) error {
	// For now, we use the same flow as Login since the AuthManager
	// will discover the issuer during CheckConnection
	return a.Login(ctx, endpoint)
}

// Logout clears stored tokens for the endpoint.
func (a *AuthAdapter) Logout(endpoint string) error {
	normalizedEndpoint := normalizeEndpoint(endpoint)

	// Remove manager from cache if it exists
	a.mu.Lock()
	if mgr, ok := a.managers[normalizedEndpoint]; ok {
		if err := mgr.Close(); err != nil {
			logging.Debug("AuthAdapter", "Error closing manager for %s: %v", normalizedEndpoint, err)
		}
		delete(a.managers, normalizedEndpoint)
	}
	a.mu.Unlock()

	// Clear the token directly from the token store.
	// We don't use the manager's ClearToken() because newly created managers
	// have an empty serverURL and would return early without clearing anything.
	store, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
		StorageDir: a.tokenStorageDir,
		FileMode:   true,
	})
	if err != nil {
		return fmt.Errorf("failed to create token store: %w", err)
	}

	if err := store.DeleteToken(normalizedEndpoint); err != nil {
		return fmt.Errorf("failed to clear token: %w", err)
	}

	return nil
}

// LogoutAll clears all stored tokens.
func (a *AuthAdapter) LogoutAll() error {
	// Close all managers
	a.mu.Lock()
	for endpoint, mgr := range a.managers {
		if err := mgr.Close(); err != nil {
			logging.Debug("AuthAdapter", "Error closing manager for %s: %v", endpoint, err)
		}
	}
	a.managers = make(map[string]*oauth.AuthManager)
	a.mu.Unlock()

	// Create a temporary token store to clear all tokens
	store, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
		StorageDir: a.tokenStorageDir,
		FileMode:   true,
	})
	if err != nil {
		return fmt.Errorf("failed to create token store: %w", err)
	}

	return store.Clear()
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

// idTokenClaims holds the identity claims we extract from ID tokens.
type idTokenClaims struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
}

// parseIDTokenClaims extracts identity claims from a JWT ID token.
// This performs basic JWT parsing without validation (validation is done at login time).
func parseIDTokenClaims(idToken string) idTokenClaims {
	var claims idTokenClaims

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

// RefreshToken attempts to refresh the token using the stored refresh token.
// If no refresh token is available or the refresh fails, it returns an error.
// This method forces a refresh regardless of how much time is left on the current token.
// Unlike Login(), this method never opens a browser - it only uses the refresh token.
func (a *AuthAdapter) RefreshToken(ctx context.Context, endpoint string) error {
	mgr, err := a.getOrCreateManager(endpoint)
	if err != nil {
		return err
	}

	// First check connection to initialize the manager with the stored token
	state, _ := mgr.CheckConnection(ctx, endpoint)
	if state != oauth.AuthStateAuthenticated {
		// Not authenticated - need full login, but don't do it automatically
		return fmt.Errorf("not authenticated. Run 'muster auth login --endpoint %s' first", endpoint)
	}

	// Force refresh the token (ignoring the threshold check)
	err = a.forceRefreshToken(ctx, endpoint)
	if err != nil {
		logging.Debug("AuthAdapter", "Token refresh failed: %v", err)
		return fmt.Errorf("token refresh failed: %w. Run 'muster auth login --endpoint %s' to re-authenticate", err, endpoint)
	}

	logging.Debug("AuthAdapter", "Token refreshed successfully for %s", endpoint)
	return nil
}

// tryRefreshToken attempts to refresh the token using the stored refresh token.
// Returns true if the token was refreshed, false if no refresh was needed.
// This is used for proactive background refresh and respects the TokenRefreshThreshold.
func (a *AuthAdapter) tryRefreshToken(ctx context.Context, endpoint string) (bool, error) {
	normalizedEndpoint := normalizeEndpoint(endpoint)

	// Create a temporary token store to access and update the stored token
	store, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
		StorageDir: a.tokenStorageDir,
		FileMode:   true,
	})
	if err != nil {
		return false, fmt.Errorf("failed to create token store: %w", err)
	}

	// Get the stored token (including expiring ones)
	storedToken := store.GetTokenIncludingExpiring(normalizedEndpoint)
	if storedToken == nil {
		return false, fmt.Errorf("no stored token found")
	}

	// Check if token has a refresh token
	if storedToken.RefreshToken == "" {
		return false, fmt.Errorf("no refresh token available")
	}

	// Check if token actually needs refresh
	if !tokenNeedsRefresh(storedToken) {
		return false, nil
	}

	// Perform the refresh
	if err := a.doTokenRefresh(ctx, store, storedToken, normalizedEndpoint); err != nil {
		return false, err
	}

	return true, nil
}

// forceRefreshToken performs a token refresh regardless of how much time is left.
// This is used when the user explicitly requests a refresh via "muster auth refresh".
func (a *AuthAdapter) forceRefreshToken(ctx context.Context, endpoint string) error {
	normalizedEndpoint := normalizeEndpoint(endpoint)

	// Create a temporary token store to access and update the stored token
	store, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
		StorageDir: a.tokenStorageDir,
		FileMode:   true,
	})
	if err != nil {
		return fmt.Errorf("failed to create token store: %w", err)
	}

	// Get the stored token (including expiring ones)
	storedToken := store.GetTokenIncludingExpiring(normalizedEndpoint)
	if storedToken == nil {
		return fmt.Errorf("no stored token found")
	}

	// Check if token has a refresh token
	if storedToken.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	// Perform the refresh (no threshold check)
	return a.doTokenRefresh(ctx, store, storedToken, normalizedEndpoint)
}

// doTokenRefresh performs the actual OAuth token refresh operation.
func (a *AuthAdapter) doTokenRefresh(ctx context.Context, store *oauth.TokenStore, storedToken *oauth.StoredToken, normalizedEndpoint string) error {
	oauthClient := pkgoauth.NewClient()
	metadata, err := oauthClient.DiscoverMetadata(ctx, storedToken.IssuerURL)
	if err != nil {
		return fmt.Errorf("failed to discover OAuth metadata: %w", err)
	}

	newToken, err := oauthClient.RefreshToken(ctx, metadata.TokenEndpoint, storedToken.RefreshToken, oauth.DefaultAgentClientID)
	if err != nil {
		return fmt.Errorf("token refresh failed: %w", err)
	}

	// Convert to oauth2.Token for storage
	oauth2Token := newToken.ToOAuth2Token()

	// Preserve refresh token if not returned
	if oauth2Token.RefreshToken == "" {
		oauth2Token.RefreshToken = storedToken.RefreshToken
	}

	// Store the refreshed token
	if err := store.StoreToken(normalizedEndpoint, storedToken.IssuerURL, oauth2Token); err != nil {
		return fmt.Errorf("failed to store refreshed token: %w", err)
	}

	return nil
}

// tokenNeedsRefresh checks if a token is approaching expiry and needs refresh.
// Uses the shared TokenRefreshThreshold from pkg/oauth for consistent behavior.
func tokenNeedsRefresh(token *oauth.StoredToken) bool {
	if token == nil || token.Expiry.IsZero() {
		return false
	}
	return time.Until(token.Expiry) < pkgoauth.TokenRefreshThreshold
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
func normalizeEndpoint(endpoint string) string {
	// Strip trailing slashes and transport-specific paths
	endpoint = strings.TrimSuffix(endpoint, "/")
	endpoint = strings.TrimSuffix(endpoint, "/mcp")
	endpoint = strings.TrimSuffix(endpoint, "/sse")
	return endpoint
}

// isPortInUseError checks if an error is related to a port being in use.
func isPortInUseError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "address already in use")
}

// CheckServerWithAuth verifies server connectivity and authentication status.
// This is an enhanced version of CheckServerRunning that returns structured status.
func CheckServerWithAuth(ctx context.Context, endpoint string) (*ServerStatus, error) {
	status := &ServerStatus{
		Endpoint: endpoint,
	}

	// First check basic connectivity
	client := &http.Client{
		Timeout: DefaultHTTPClientTimeout,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		status.Error = err
		return status, err
	}

	resp, err := client.Do(req)
	if err != nil {
		status.Error = fmt.Errorf("muster server is not running. Start it with: muster serve")
		return status, status.Error
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	status.Reachable = true

	// Check for 401 Unauthorized
	if resp.StatusCode == http.StatusUnauthorized {
		status.AuthRequired = true

		// Parse WWW-Authenticate header to extract OAuth configuration (issuer, etc.)
		// This validates that the server has OAuth properly configured.
		_ = pkgoauth.ParseWWWAuthenticateFromResponse(resp)

		// Check if we have a valid token
		authHandler := api.GetAuthHandler()
		if authHandler != nil {
			status.Authenticated = authHandler.HasValidToken(endpoint)
		}

		if !status.Authenticated {
			status.Error = &AuthRequiredError{Endpoint: endpoint}
		}

		return status, nil
	}

	// Server responded without 401 - check for valid responses
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK {
		return status, nil
	}

	status.Error = fmt.Errorf("muster server is not responding correctly (status: %d). Try restarting with: muster serve", resp.StatusCode)
	return status, status.Error
}
