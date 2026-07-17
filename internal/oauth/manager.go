package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
)

// AuthCompletionCallback is called after successful OAuth authentication.
type AuthCompletionCallback func(ctx context.Context, sessionID, userID, serverName, accessToken string) error

// Manager coordinates OAuth flows for remote MCP server authentication.
// It manages the OAuth MCP client, HTTP handlers, and integrates with the aggregator.
type Manager struct {
	mu sync.RWMutex

	// Configuration (OAuth MCP client/proxy configuration for authenticating TO remote MCP servers)
	config config.OAuthMCPClientConfig

	// Core components
	client         *Client
	handler        *Handler
	tokenExchanger *TokenExchanger

	// Server authentication metadata: serverName -> AuthServerConfig
	serverConfigs map[string]*AuthServerConfig

	// Callback to establish session connection after authentication
	authCompletionCallback AuthCompletionCallback
}

// parsePostLoginRedirect validates an operator-configured post-login redirect
// target: it must be an absolute http(s) URL. The value never comes from
// request input, so this guards against misconfiguration, not attackers.
func parsePostLoginRedirect(raw string) (*url.URL, error) {
	target, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if (target.Scheme != "https" && target.Scheme != "http") || target.Host == "" {
		return nil, fmt.Errorf("must be an absolute http(s) URL")
	}
	return target, nil
}

// AuthServerConfig holds OAuth configuration for a specific remote MCP server.
type AuthServerConfig struct {
	ServerName string
	Issuer     string
	Scope      string
}

// ManagerOption configures optional Manager parameters.
type ManagerOption func(*managerOptions)

type managerOptions struct {
	clientOpts []ClientOption
}

// WithValkeyTokenStore injects a Valkey-backed TokenStorer into the OAuth client.
func WithValkeyTokenStore(ts TokenStorer) ManagerOption {
	return func(o *managerOptions) {
		o.clientOpts = append(o.clientOpts, WithTokenStorer(ts))
	}
}

// WithValkeyStateStore injects a Valkey-backed StateStorer into the OAuth client.
func WithValkeyStateStore(ss StateStorer) ManagerOption {
	return func(o *managerOptions) {
		o.clientOpts = append(o.clientOpts, WithStateStorer(ss))
	}
}

// NewManager creates a new OAuth manager with the given configuration.
// The cfg parameter contains the OAuth MCP client/proxy configuration for
// authenticating TO remote MCP servers.
func NewManager(cfg config.OAuthMCPClientConfig, opts ...ManagerOption) *Manager {
	if !cfg.Enabled {
		logging.Info("OAuth", "OAuth proxy is disabled")
		return nil
	}

	var mopts managerOptions
	for _, opt := range opts {
		opt(&mopts)
	}

	// Use the effective client ID (auto-derived from PublicURL if not explicitly set)
	effectiveClientID := cfg.GetEffectiveClientID()
	// Use the effective CIMD scopes (defaults to comprehensive Google API scopes for SSO)
	cimdScopes := cfg.GetCIMDScopes()
	client := NewClient(effectiveClientID, cfg.PublicURL, cfg.CallbackPath, cimdScopes, mopts.clientOpts...)

	handler := NewHandler(client)
	if cfg.PostLoginRedirectURL != "" {
		if target, err := parsePostLoginRedirect(cfg.PostLoginRedirectURL); err != nil {
			logging.Warn("OAuth", "Ignoring invalid oauth.mcpClient.postLoginRedirectUrl %q: %v", cfg.PostLoginRedirectURL, err)
		} else {
			handler.SetPostLoginRedirect(target)
			logging.Info("OAuth", "Post-login redirect enabled: %s", target)
		}
	}

	// Create token exchanger for RFC 8693 cross-cluster SSO.
	// Outbound TLS is handled by the augmented http.DefaultTransport (see
	// internal/app/tls.go), so no per-client CA wiring is needed. When
	// --extra-ca-file is set this muster is talking to in-cluster TLS endpoints,
	// so allow the token-exchange client to resolve to private/loopback IPs
	// (otherwise its SSRF guard rejects .svc.cluster.local targets like an
	// in-cluster Dex). mcp-oauth's NewPrivateIPAllowedHTTPClient builds a fresh
	// *http.Transport that bypasses the augmented pool, so hand the exchanger
	// an explicit client backed by the augmented DefaultTransport.
	var tokenExchangeHTTPClient *http.Client
	if cfg.ExtraCAFile != "" {
		tokenExchangeHTTPClient = &http.Client{
			Transport: http.DefaultTransport,
			Timeout:   30 * time.Second,
		}
	}
	tokenExchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{
		AllowPrivateIP: cfg.ExtraCAFile != "",
		HTTPClient:     tokenExchangeHTTPClient,
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
// mcp-go handles token refresh via its transport layer, so this method
// simply returns the stored token without proactive refresh.
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

	return m.client.GetToken(sessionID, serverCfg.Issuer, serverCfg.Scope)
}

// GetTokenByIssuer retrieves a valid token for the given session and issuer.
// This is used for SSO when we have the issuer from a 401 response.
func (m *Manager) GetTokenByIssuer(sessionID, issuer string) *pkgoauth.Token {
	if m == nil {
		return nil
	}

	return m.client.tokenStore.GetByIssuer(sessionID, issuer)
}

// ClearTokenByIssuer removes all tokens for a given session and issuer.
func (m *Manager) ClearTokenByIssuer(sessionID, issuer string) {
	if m == nil {
		return
	}

	m.client.tokenStore.DeleteByIssuer(sessionID, issuer)
	logging.Debug("OAuth", "Cleared tokens for session=%s issuer=%s", logging.TruncateIdentifier(sessionID), issuer)
}

// DeleteTokensByUser removes all downstream tokens for a given user across all sessions.
// This is used during "sign out everywhere" to clear all server-side token state.
func (m *Manager) DeleteTokensByUser(userID string) {
	if m == nil {
		return
	}

	m.client.tokenStore.DeleteByUser(userID)
	logging.Debug("OAuth", "Deleted all tokens for user=%s", logging.TruncateIdentifier(userID))
}

// DeleteTokensBySession removes all downstream tokens for a given session.
// This is used during per-session logout via token family revocation.
func (m *Manager) DeleteTokensBySession(sessionID string) {
	if m == nil {
		return
	}

	m.client.tokenStore.DeleteBySession(sessionID)
	logging.Debug("OAuth", "Deleted all tokens for session=%s", logging.TruncateIdentifier(sessionID))
}

// StoreToken persists a token for the given session and issuer.
// The userID is stored alongside for reverse-lookup by user.
func (m *Manager) StoreToken(sessionID, userID, issuer string, token *pkgoauth.Token) {
	if m == nil || token == nil {
		return
	}

	key := TokenKey{
		SessionID: sessionID,
		Issuer:    issuer,
		Scope:     token.Scope,
	}
	m.client.tokenStore.Store(key, token, userID)
}

// CreateAuthChallenge creates an authentication challenge for a 401 response.
// Returns the auth URL the user should visit and the challenge response.
func (m *Manager) CreateAuthChallenge(ctx context.Context, sessionID, userID, serverName, issuer, scope string) (*AuthRequiredResponse, error) {
	if m == nil {
		return nil, fmt.Errorf("OAuth proxy is disabled")
	}

	// Register server config if we got it from the 401
	m.RegisterServer(serverName, issuer, scope)

	// Generate authorization URL (code verifier is stored with the state)
	authURL, err := m.client.GenerateAuthURL(ctx, sessionID, userID, serverName, issuer, scope)
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
		logging.TruncateIdentifier(sessionID), serverName)

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

	// Store the token keyed by session ID, with user ID for reverse lookup
	m.client.StoreToken(stateData.SessionID, stateData.UserID, token)

	logging.Info("OAuth", "Successfully completed OAuth flow for session=%s server=%s",
		logging.TruncateIdentifier(stateData.SessionID), stateData.ServerName)

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
