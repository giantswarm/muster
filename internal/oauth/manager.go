package oauth

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"muster/internal/config"
	"muster/pkg/logging"
)

// Manager coordinates OAuth flows for remote MCP server authentication.
// It manages the OAuth client, HTTP handlers, and integrates with the aggregator.
type Manager struct {
	mu sync.RWMutex

	// Configuration
	config config.OAuthConfig

	// Core components
	client  *Client
	handler *Handler

	// Server authentication metadata: serverName -> AuthServerConfig
	serverConfigs map[string]*AuthServerConfig
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
	client := NewClient(effectiveClientID, cfg.PublicURL, cfg.CallbackPath)
	handler := NewHandler(client)

	m := &Manager{
		config:        cfg,
		client:        client,
		handler:       handler,
		serverConfigs: make(map[string]*AuthServerConfig),
	}

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
func (m *Manager) GetToken(sessionID, serverName string) *Token {
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
func (m *Manager) GetTokenByIssuer(sessionID, issuer string) *Token {
	if m == nil {
		return nil
	}

	return m.client.tokenStore.GetByIssuer(sessionID, issuer)
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

// CreateAuthChallenge creates an authentication challenge for a 401 response.
// Returns the auth URL the user should visit and the challenge response.
func (m *Manager) CreateAuthChallenge(ctx context.Context, sessionID, serverName string, authParams *WWWAuthenticateParams) (*AuthChallenge, error) {
	if m == nil {
		return nil, fmt.Errorf("OAuth proxy is disabled")
	}

	issuer := authParams.GetIssuer()
	scope := authParams.Scope

	// Register server config if we got it from the 401
	m.RegisterServer(serverName, issuer, scope)

	// Generate authorization URL (code verifier is stored with the state)
	authURL, err := m.client.GenerateAuthURL(ctx, sessionID, serverName, issuer, scope)
	if err != nil {
		return nil, fmt.Errorf("failed to generate auth URL: %w", err)
	}

	challenge := &AuthChallenge{
		Status:     "auth_required",
		AuthURL:    authURL,
		ServerName: serverName,
		Message:    fmt.Sprintf("Authentication required for %s. Please visit the link below to authenticate.", serverName),
	}

	logging.Info("OAuth", "Created auth challenge for session=%s server=%s",
		sessionID, serverName)

	return challenge, nil
}

// HandleCallback processes an OAuth callback and stores the token.
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

// Stop stops the OAuth manager and cleans up resources.
func (m *Manager) Stop() {
	if m == nil {
		return
	}

	m.client.Stop()
	logging.Info("OAuth", "OAuth manager stopped")
}
