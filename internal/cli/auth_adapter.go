package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"muster/internal/agent/oauth"
	"muster/internal/api"
	pkgoauth "muster/pkg/oauth"
)

// DefaultTokenStorageDir is the default directory for storing OAuth tokens.
const DefaultTokenStorageDir = ".config/muster/tokens"

// AuthAdapter implements api.AuthHandler using internal/agent/oauth.
// It wraps the AuthManager and TokenStore to provide OAuth authentication
// for CLI commands following the project's service locator pattern.
type AuthAdapter struct {
	// manager handles OAuth flows and state management.
	// Each login creates a new manager instance for that specific endpoint.
	managers map[string]*oauth.AuthManager

	// tokenStorageDir is the directory for storing tokens.
	tokenStorageDir string

	// httpClient is shared across operations.
	httpClient *http.Client
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
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// Register registers the adapter with the API layer.
func (a *AuthAdapter) Register() {
	api.RegisterAuthHandler(a)
}

// getOrCreateManager gets or creates an AuthManager for the given endpoint.
func (a *AuthAdapter) getOrCreateManager(endpoint string) (*oauth.AuthManager, error) {
	normalizedEndpoint := normalizeEndpoint(endpoint)

	if mgr, ok := a.managers[normalizedEndpoint]; ok {
		return mgr, nil
	}

	mgr, err := oauth.NewAuthManager(oauth.AuthManagerConfig{
		TokenStorageDir: a.tokenStorageDir,
		FileMode:        true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create auth manager: %w", err)
	}

	a.managers[normalizedEndpoint] = mgr
	return mgr, nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	state, _ := mgr.CheckConnection(ctx, endpoint)
	return state == oauth.AuthStateAuthenticated
}

// GetBearerToken returns a valid Bearer token for the endpoint.
func (a *AuthAdapter) GetBearerToken(endpoint string) (string, error) {
	mgr, err := a.getOrCreateManager(endpoint)
	if err != nil {
		return "", err
	}

	// First check if we need to check connection state
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		return &AuthFailedError{Endpoint: endpoint, Reason: err}
	}

	// Open browser
	fmt.Printf("Opening browser for authentication...\n")
	fmt.Printf("If the browser doesn't open, visit:\n  %s\n\n", authURL)

	if err := oauth.OpenBrowser(authURL); err != nil {
		fmt.Printf("Failed to open browser: %v\n", err)
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
	mgr, err := a.getOrCreateManager(endpoint)
	if err != nil {
		return err
	}

	if err := mgr.ClearToken(); err != nil {
		return fmt.Errorf("failed to clear token: %w", err)
	}

	// Remove manager from cache
	normalizedEndpoint := normalizeEndpoint(endpoint)
	delete(a.managers, normalizedEndpoint)

	return nil
}

// LogoutAll clears all stored tokens.
func (a *AuthAdapter) LogoutAll() error {
	// Close all managers
	for _, mgr := range a.managers {
		_ = mgr.Close()
	}
	a.managers = make(map[string]*oauth.AuthManager)

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
	for endpoint, mgr := range a.managers {
		status := a.getStatusFromManager(endpoint, mgr)
		statuses = append(statuses, status)
	}

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
		if challenge := mgr.GetAuthChallenge(); challenge != nil {
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

// RefreshToken forces a token refresh for the endpoint.
func (a *AuthAdapter) RefreshToken(ctx context.Context, endpoint string) error {
	// Clear the current token and re-authenticate
	if err := a.Logout(endpoint); err != nil {
		return err
	}
	return a.Login(ctx, endpoint)
}

// Close cleans up any resources held by the auth adapter.
func (a *AuthAdapter) Close() error {
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
	if err := parseJSON(data, &token); err != nil {
		return nil, err
	}

	return &tokenFileInfo{
		ServerURL: token.ServerURL,
		IssuerURL: token.IssuerURL,
		Expiry:    token.Expiry,
	}, nil
}

// parseJSON parses JSON data into the given value.
func parseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// normalizeEndpoint normalizes an endpoint URL for consistent key usage.
func normalizeEndpoint(endpoint string) string {
	// Strip trailing slashes and transport-specific paths
	endpoint = stripSuffix(endpoint, "/")
	endpoint = stripSuffix(endpoint, "/mcp")
	endpoint = stripSuffix(endpoint, "/sse")
	return endpoint
}

func stripSuffix(s, suffix string) string {
	if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)]
	}
	return s
}

// CheckServerWithAuth verifies server connectivity and authentication status.
// This is an enhanced version of CheckServerRunning that returns structured status.
func CheckServerWithAuth(ctx context.Context, endpoint string) (*ServerStatus, error) {
	status := &ServerStatus{
		Endpoint: endpoint,
	}

	// First check basic connectivity
	client := &http.Client{
		Timeout: 5 * time.Second,
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

		// Parse WWW-Authenticate header to get OAuth info
		challenge := pkgoauth.ParseWWWAuthenticateFromResponse(resp)
		if challenge != nil && challenge.Issuer != "" {
			// OAuth is properly configured
		}

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
