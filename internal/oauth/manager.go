package oauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"muster/internal/api"
	"muster/internal/config"
	"muster/pkg/logging"
	pkgoauth "muster/pkg/oauth"
)

// AuthCompletionCallback is called after successful OAuth authentication.
type AuthCompletionCallback func(ctx context.Context, sessionID, serverName, accessToken string) error

// Manager coordinates OAuth flows for remote MCP server authentication.
// It manages the OAuth client, HTTP handlers, and integrates with the aggregator.
type Manager struct {
	mu sync.RWMutex

	// Configuration
	config config.OAuthConfig

	// Core components
	client         *Client
	handler        *Handler
	tokenExchanger *TokenExchanger

	// Server authentication metadata: serverName -> AuthServerConfig
	serverConfigs map[string]*AuthServerConfig

	// Callback to establish session connection after authentication
	authCompletionCallback AuthCompletionCallback
}

// AuthServerConfig holds OAuth configuration for a specific remote MCP server.
type AuthServerConfig struct {
	ServerName string
	Issuer     string
	Scope      string
}

// NewManager creates a new OAuth manager with the given configuration.
func NewManager(cfg config.OAuthConfig) *Manager {
	if !cfg.Enabled {
		logging.Info("OAuth", "OAuth proxy is disabled")
		return nil
	}

	// Use the effective client ID (auto-derived from PublicURL if not explicitly set)
	effectiveClientID := cfg.GetEffectiveClientID()
	// Use the effective CIMD scopes (defaults to comprehensive Google API scopes for SSO)
	cimdScopes := cfg.GetCIMDScopes()
	client := NewClient(effectiveClientID, cfg.PublicURL, cfg.CallbackPath, cimdScopes)

	// Configure custom HTTP client with CA if provided
	// The same HTTP client is shared with the token exchanger for consistent TLS config
	var customHTTPClient *http.Client
	if cfg.CAFile != "" {
		httpClient, err := createHTTPClientWithCA(cfg.CAFile)
		if err != nil {
			logging.Warn("OAuth", "Failed to configure custom CA, using default: %v", err)
		} else {
			customHTTPClient = httpClient
			client.SetHTTPClient(httpClient)
			logging.Info("OAuth", "Configured OAuth proxy with custom CA from %s", cfg.CAFile)
		}
	}

	handler := NewHandler(client)

	// Create token exchanger for RFC 8693 cross-cluster SSO
	// Use the same HTTP client as the OAuth client for consistent TLS configuration.
	// This ensures token exchange requests trust the same CA certificates.
	tokenExchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{
		AllowPrivateIP: cfg.CAFile != "", // If custom CA is provided, likely internal deployment
		HTTPClient:     customHTTPClient, // Share the same HTTP client with CA config
	})

	m := &Manager{
		config:         cfg,
		client:         client,
		handler:        handler,
		tokenExchanger: tokenExchanger,
		serverConfigs:  make(map[string]*AuthServerConfig),
	}

	// Set manager reference on handler for callback handling
	handler.SetManager(m)

	// Log whether we're serving our own CIMD
	if cfg.ShouldServeCIMD() {
		logging.Info("OAuth", "OAuth manager initialized with self-hosted CIMD (publicURL=%s, clientID=%s, cimdPath=%s)",
			cfg.PublicURL, effectiveClientID, cfg.GetCIMDPath())
	} else {
		logging.Info("OAuth", "OAuth manager initialized with external CIMD (publicURL=%s, clientID=%s)",
			cfg.PublicURL, effectiveClientID)
	}

	return m
}

// createHTTPClientWithCA creates an HTTP client that trusts the specified CA certificate.
func createHTTPClientWithCA(caFile string) (*http.Client, error) {
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA file: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs: caCertPool,
		},
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}, nil
}

// IsEnabled returns whether OAuth proxy is enabled.
func (m *Manager) IsEnabled() bool {
	return m != nil && m.config.Enabled
}

// GetHTTPHandler returns the HTTP handler for OAuth endpoints.
func (m *Manager) GetHTTPHandler() http.Handler {
	if m == nil {
		return nil
	}
	return m.handler
}

// GetCallbackPath returns the configured callback path.
func (m *Manager) GetCallbackPath() string {
	if m == nil {
		return ""
	}
	return m.config.CallbackPath
}

// GetCIMDPath returns the path for serving the CIMD.
func (m *Manager) GetCIMDPath() string {
	if m == nil {
		return ""
	}
	return m.config.GetCIMDPath()
}

// ShouldServeCIMD returns true if muster should serve its own CIMD.
func (m *Manager) ShouldServeCIMD() bool {
	if m == nil {
		return false
	}
	return m.config.ShouldServeCIMD()
}

// GetCIMDHandler returns the HTTP handler for serving the CIMD.
func (m *Manager) GetCIMDHandler() http.HandlerFunc {
	if m == nil || m.handler == nil {
		return nil
	}
	return m.handler.ServeCIMD
}

// RegisterServer registers OAuth configuration for a remote MCP server.
func (m *Manager) RegisterServer(serverName, issuer, scope string) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.serverConfigs[serverName] = &AuthServerConfig{
		ServerName: serverName,
		Issuer:     issuer,
		Scope:      scope,
	}

	logging.Debug("OAuth", "Registered OAuth config for server=%s issuer=%s scope=%s",
		serverName, issuer, scope)
}

// GetServerConfig returns the OAuth configuration for a server.
func (m *Manager) GetServerConfig(serverName string) *AuthServerConfig {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.serverConfigs[serverName]
}

// GetToken retrieves a valid token for the given session and server.
// It will proactively refresh the token if it's about to expire.
func (m *Manager) GetToken(sessionID, serverName string) *pkgoauth.Token {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	serverCfg := m.serverConfigs[serverName]
	m.mu.RUnlock()

	if serverCfg == nil {
		return nil
	}

	// Try to refresh the token if needed
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	token, refreshed, err := m.client.RefreshTokenIfNeeded(ctx, sessionID, serverCfg.Issuer)
	if err != nil {
		logging.Debug("OAuth", "Token refresh failed for session=%s server=%s: %v",
			logging.TruncateSessionID(sessionID), serverName, err)
		// Fall back to getting the token directly (might still be valid)
		return m.client.GetToken(sessionID, serverCfg.Issuer, serverCfg.Scope)
	}

	if refreshed {
		logging.Debug("OAuth", "Token proactively refreshed for session=%s server=%s",
			logging.TruncateSessionID(sessionID), serverName)
	}

	// Verify the token is still valid
	if token != nil && !token.IsExpiredWithMargin(tokenExpiryMargin) {
		return token
	}

	return nil
}

// GetTokenByIssuer retrieves a valid token for the given session and issuer.
// This is used for SSO when we have the issuer from a 401 response.
// It will proactively refresh the token if it's about to expire.
func (m *Manager) GetTokenByIssuer(sessionID, issuer string) *pkgoauth.Token {
	if m == nil {
		return nil
	}

	// Try to refresh the token if needed
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	token, refreshed, err := m.client.RefreshTokenIfNeeded(ctx, sessionID, issuer)
	if err != nil {
		logging.Debug("OAuth", "Token refresh failed for session=%s issuer=%s: %v",
			logging.TruncateSessionID(sessionID), issuer, err)
		// Fall back to getting the token directly (might still be valid)
		return m.client.tokenStore.GetByIssuer(sessionID, issuer)
	}

	if refreshed {
		logging.Debug("OAuth", "Token proactively refreshed for session=%s issuer=%s",
			logging.TruncateSessionID(sessionID), issuer)
	}

	// Verify the token is still valid
	if token != nil && !token.IsExpiredWithMargin(tokenExpiryMargin) {
		return token
	}

	return nil
}

// ClearTokenByIssuer removes all tokens for a given session and issuer.
// This is used to clear invalid/expired tokens before requesting fresh authentication.
func (m *Manager) ClearTokenByIssuer(sessionID, issuer string) {
	if m == nil {
		return
	}

	m.client.tokenStore.DeleteByIssuer(sessionID, issuer)
	logging.Debug("OAuth", "Cleared tokens for session=%s issuer=%s", logging.TruncateSessionID(sessionID), issuer)
}

// RefreshTokenIfNeeded checks if the token for the given session and issuer needs refresh
// and refreshes it if necessary. This is the primary method for automatic token refresh
// in long-running sessions (Issue #214).
//
// Returns:
//   - The token (refreshed or original)
//   - Boolean indicating if a refresh occurred
//   - Any error during refresh (token is still returned if available)
func (m *Manager) RefreshTokenIfNeeded(ctx context.Context, sessionID, issuer string) (*pkgoauth.Token, bool, error) {
	if m == nil {
		return nil, false, fmt.Errorf("OAuth proxy is disabled")
	}

	return m.client.RefreshTokenIfNeeded(ctx, sessionID, issuer)
}

// CreateAuthChallenge creates an authentication challenge for a 401 response.
// Returns the auth URL the user should visit and the challenge response.
func (m *Manager) CreateAuthChallenge(ctx context.Context, sessionID, serverName, issuer, scope string) (*AuthRequiredResponse, error) {
	if m == nil {
		return nil, fmt.Errorf("OAuth proxy is disabled")
	}

	// Register server config if we got it from the 401
	m.RegisterServer(serverName, issuer, scope)

	// Generate authorization URL (code verifier is stored with the state)
	authURL, err := m.client.GenerateAuthURL(ctx, sessionID, serverName, issuer, scope)
	if err != nil {
		return nil, fmt.Errorf("failed to generate auth URL: %w", err)
	}

	challenge := &AuthRequiredResponse{
		Status:     "auth_required",
		AuthURL:    authURL,
		ServerName: serverName,
		Message:    fmt.Sprintf("Authentication required for %s. Please visit the link below to authenticate.", serverName),
	}

	logging.Info("OAuth", "Created auth challenge for session=%s server=%s",
		sessionID, serverName)

	return challenge, nil
}

// SetAuthCompletionCallback sets the callback to be called after successful authentication.
// The aggregator uses this to establish session connections after browser OAuth completes.
func (m *Manager) SetAuthCompletionCallback(callback AuthCompletionCallback) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.authCompletionCallback = callback
	logging.Debug("OAuth", "Auth completion callback registered")
}

// HandleCallback processes an OAuth callback and stores the token.
// Note: This is a programmatic API for testing. The production flow uses
// Handler.HandleCallback which is the actual HTTP endpoint and handles
// the auth completion callback invocation.
func (m *Manager) HandleCallback(ctx context.Context, code, state string) error {
	if m == nil {
		return fmt.Errorf("OAuth proxy is disabled")
	}

	// Validate state (returns the full state including issuer and code verifier)
	stateData := m.client.stateStore.ValidateState(state)
	if stateData == nil {
		return fmt.Errorf("invalid or expired state")
	}

	// Validate we have the required data
	if stateData.Issuer == "" {
		return fmt.Errorf("missing issuer in state")
	}
	if stateData.CodeVerifier == "" {
		return fmt.Errorf("missing code verifier in state")
	}

	// Exchange code for token using issuer and code verifier from state
	token, err := m.client.ExchangeCode(ctx, code, stateData.CodeVerifier, stateData.Issuer)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	// Store the token
	m.client.StoreToken(stateData.SessionID, token)

	logging.Info("OAuth", "Successfully completed OAuth flow for session=%s server=%s",
		stateData.SessionID, stateData.ServerName)

	return nil
}

// ExchangeTokenForRemoteCluster exchanges a local token for one valid on a remote cluster.
// This implements RFC 8693 Token Exchange for cross-cluster SSO scenarios.
//
// Args:
//   - ctx: Context for the operation
//   - localToken: The local ID token to exchange
//   - userID: The user's unique identifier (from validated JWT 'sub' claim)
//   - config: Token exchange configuration for the remote cluster
//
// Returns the exchanged access token, or an error if exchange fails.
func (m *Manager) ExchangeTokenForRemoteCluster(ctx context.Context, localToken, userID string, config *api.TokenExchangeConfig) (string, error) {
	if m == nil {
		return "", fmt.Errorf("OAuth proxy is disabled")
	}
	if m.tokenExchanger == nil {
		return "", fmt.Errorf("token exchanger not initialized")
	}
	if config == nil {
		return "", fmt.Errorf("token exchange config is nil")
	}

	result, err := m.tokenExchanger.Exchange(ctx, &ExchangeRequest{
		Config:           config,
		SubjectToken:     localToken,
		SubjectTokenType: "", // defaults to ID token
		UserID:           userID,
	})
	if err != nil {
		return "", fmt.Errorf("exchange token for remote cluster: %w", err)
	}

	return result.AccessToken, nil
}

// ExchangeTokenForRemoteClusterWithClient exchanges a local token for one valid on a remote cluster
// using a custom HTTP client. This is used when the token exchange endpoint is accessed via
// Teleport Application Access, which requires mutual TLS authentication.
//
// The httpClient parameter should be configured with the appropriate TLS certificates
// (e.g., Teleport Machine ID certificates). If nil, uses the default HTTP client.
//
// Args:
//   - ctx: Context for the operation
//   - localToken: The local ID token to exchange
//   - userID: The user's unique identifier (from validated JWT 'sub' claim)
//   - config: Token exchange configuration for the remote cluster
//   - httpClient: Custom HTTP client with Teleport TLS certificates (or nil for default)
//
// Returns the exchanged access token, or an error if exchange fails.
func (m *Manager) ExchangeTokenForRemoteClusterWithClient(ctx context.Context, localToken, userID string, config *api.TokenExchangeConfig, httpClient *http.Client) (string, error) {
	if m == nil {
		return "", fmt.Errorf("OAuth proxy is disabled")
	}
	if m.tokenExchanger == nil {
		return "", fmt.Errorf("token exchanger not initialized")
	}
	if config == nil {
		return "", fmt.Errorf("token exchange config is nil")
	}

	result, err := m.tokenExchanger.ExchangeWithClient(ctx, &ExchangeRequest{
		Config:           config,
		SubjectToken:     localToken,
		SubjectTokenType: "", // defaults to ID token
		UserID:           userID,
	}, httpClient)
	if err != nil {
		return "", fmt.Errorf("exchange token for remote cluster with custom client: %w", err)
	}

	return result.AccessToken, nil
}

// GetTokenExchanger returns the token exchanger for direct access.
// This is useful for cache management and monitoring.
func (m *Manager) GetTokenExchanger() *TokenExchanger {
	if m == nil {
		return nil
	}
	return m.tokenExchanger
}

// Stop stops the OAuth manager and cleans up resources.
func (m *Manager) Stop() {
	if m == nil {
		return
	}

	m.client.Stop()
	logging.Info("OAuth", "OAuth manager stopped")
}
