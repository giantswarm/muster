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

	// Pending auth flows: stores code verifiers by state nonce
	pendingVerifiers sync.Map
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

	client := NewClient(cfg.ClientID, cfg.PublicURL, cfg.CallbackPath)
	handler := NewHandler(client)

	m := &Manager{
		config:        cfg,
		client:        client,
		handler:       handler,
		serverConfigs: make(map[string]*AuthServerConfig),
	}

	logging.Info("OAuth", "OAuth manager initialized (publicURL=%s, clientID=%s)",
		cfg.PublicURL, cfg.ClientID)

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

	// Generate authorization URL
	authURL, codeVerifier, err := m.client.GenerateAuthURL(ctx, sessionID, serverName, issuer, scope)
	if err != nil {
		return nil, fmt.Errorf("failed to generate auth URL: %w", err)
	}

	// Store the code verifier for later retrieval during callback
	// We use a simple key based on session and server
	verifierKey := fmt.Sprintf("%s:%s", sessionID, serverName)
	m.pendingVerifiers.Store(verifierKey, codeVerifier)

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

	// Validate state
	stateData := m.client.stateStore.ValidateState(state)
	if stateData == nil {
		return fmt.Errorf("invalid or expired state")
	}

	// Get server config for issuer
	m.mu.RLock()
	serverCfg := m.serverConfigs[stateData.ServerName]
	m.mu.RUnlock()

	if serverCfg == nil {
		return fmt.Errorf("unknown server: %s", stateData.ServerName)
	}

	// Retrieve code verifier
	verifierKey := fmt.Sprintf("%s:%s", stateData.SessionID, stateData.ServerName)
	verifierVal, ok := m.pendingVerifiers.LoadAndDelete(verifierKey)
	if !ok {
		return fmt.Errorf("code verifier not found")
	}
	codeVerifier := verifierVal.(string)

	// Exchange code for token
	token, err := m.client.ExchangeCode(ctx, code, codeVerifier, serverCfg.Issuer)
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
