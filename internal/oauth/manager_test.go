package oauth

import (
	"context"
	"testing"

	"muster/internal/config"
)

func TestNewManager_Disabled(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled: false,
	}

	manager := NewManager(cfg)
	if manager != nil {
		t.Error("Expected nil manager when OAuth is disabled")
	}
}

func TestNewManager_Enabled(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "https://external.example.com/oauth-client.json",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager when OAuth is enabled")
	}
	defer manager.Stop()

	if !manager.IsEnabled() {
		t.Error("Manager should report as enabled")
	}
}

func TestManager_IsEnabled_NilManager(t *testing.T) {
	var manager *Manager
	if manager.IsEnabled() {
		t.Error("Nil manager should not be enabled")
	}
}

func TestManager_GetCallbackPath(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "client-id",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	path := manager.GetCallbackPath()
	if path != "/oauth/proxy/callback" {
		t.Errorf("Expected callback path %q, got %q", "/oauth/proxy/callback", path)
	}
}

func TestManager_GetCallbackPath_NilManager(t *testing.T) {
	var manager *Manager
	path := manager.GetCallbackPath()
	if path != "" {
		t.Errorf("Expected empty path for nil manager, got %q", path)
	}
}

func TestManager_GetHTTPHandler(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "client-id",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	handler := manager.GetHTTPHandler()
	if handler == nil {
		t.Error("Expected non-nil HTTP handler")
	}
}

func TestManager_GetHTTPHandler_NilManager(t *testing.T) {
	var manager *Manager
	handler := manager.GetHTTPHandler()
	if handler != nil {
		t.Error("Expected nil handler for nil manager")
	}
}

func TestManager_RegisterServer(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "client-id",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	serverName := "mcp-kubernetes"
	issuer := "https://auth.example.com"
	scope := "openid profile"

	// Initially no config
	serverCfg := manager.GetServerConfig(serverName)
	if serverCfg != nil {
		t.Error("Expected nil server config initially")
	}

	// Register the server
	manager.RegisterServer(serverName, issuer, scope)

	// Now should be retrievable
	serverCfg = manager.GetServerConfig(serverName)
	if serverCfg == nil {
		t.Fatal("Expected server config after registration")
	}

	if serverCfg.ServerName != serverName {
		t.Errorf("Expected server name %q, got %q", serverName, serverCfg.ServerName)
	}

	if serverCfg.Issuer != issuer {
		t.Errorf("Expected issuer %q, got %q", issuer, serverCfg.Issuer)
	}

	if serverCfg.Scope != scope {
		t.Errorf("Expected scope %q, got %q", scope, serverCfg.Scope)
	}
}

func TestManager_RegisterServer_NilManager(t *testing.T) {
	var manager *Manager
	// Should not panic
	manager.RegisterServer("server", "issuer", "scope")
}

func TestManager_GetServerConfig_NilManager(t *testing.T) {
	var manager *Manager
	cfg := manager.GetServerConfig("any-server")
	if cfg != nil {
		t.Error("Expected nil config for nil manager")
	}
}

func TestManager_GetToken_NoServerConfig(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "client-id",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	// No server registered, should return nil
	token := manager.GetToken("session-123", "unknown-server")
	if token != nil {
		t.Error("Expected nil token for unknown server")
	}
}

func TestManager_GetToken_NilManager(t *testing.T) {
	var manager *Manager
	token := manager.GetToken("session", "server")
	if token != nil {
		t.Error("Expected nil token for nil manager")
	}
}

func TestManager_GetTokenByIssuer_NilManager(t *testing.T) {
	var manager *Manager
	token := manager.GetTokenByIssuer("session", "issuer")
	if token != nil {
		t.Error("Expected nil token for nil manager")
	}
}

func TestManager_Stop_NilManager(t *testing.T) {
	var manager *Manager
	// Should not panic
	manager.Stop()
}

func TestManager_Stop(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "client-id",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}

	// Should not panic
	manager.Stop()
}

func TestManager_GetToken_WithToken(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "client-id",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	serverName := "mcp-kubernetes"
	issuer := "https://auth.example.com"
	scope := "openid profile"
	sessionID := "session-123"

	// Register the server
	manager.RegisterServer(serverName, issuer, scope)

	// Store a token directly in the client
	testToken := &Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       scope,
		Issuer:      issuer,
	}
	manager.client.StoreToken(sessionID, testToken)

	// Now GetToken should return the token
	token := manager.GetToken(sessionID, serverName)
	if token == nil {
		t.Fatal("Expected token")
	}

	if token.AccessToken != testToken.AccessToken {
		t.Errorf("Expected access token %q, got %q", testToken.AccessToken, token.AccessToken)
	}
}

func TestManager_GetTokenByIssuer(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "client-id",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	issuer := "https://auth.example.com"
	sessionID := "session-123"

	// Store a token directly
	testToken := &Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       "openid",
		Issuer:      issuer,
	}
	manager.client.StoreToken(sessionID, testToken)

	// GetTokenByIssuer should find it
	token := manager.GetTokenByIssuer(sessionID, issuer)
	if token == nil {
		t.Fatal("Expected token")
	}

	if token.AccessToken != testToken.AccessToken {
		t.Errorf("Expected access token %q, got %q", testToken.AccessToken, token.AccessToken)
	}
}

func TestManager_GetCIMDPath(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "",
		CallbackPath: "/oauth/proxy/callback",
		CIMDPath:     "/.well-known/oauth-client.json",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	path := manager.GetCIMDPath()
	if path != "/.well-known/oauth-client.json" {
		t.Errorf("Expected CIMD path %q, got %q", "/.well-known/oauth-client.json", path)
	}
}

func TestManager_GetCIMDPath_NilManager(t *testing.T) {
	var manager *Manager
	path := manager.GetCIMDPath()
	if path != "" {
		t.Errorf("Expected empty path for nil manager, got %q", path)
	}
}

func TestManager_ShouldServeCIMD(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "", // Empty means self-host
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	if !manager.ShouldServeCIMD() {
		t.Error("Manager should serve CIMD when clientId is empty")
	}
}

func TestManager_ShouldServeCIMD_NilManager(t *testing.T) {
	var manager *Manager
	if manager.ShouldServeCIMD() {
		t.Error("Nil manager should not serve CIMD")
	}
}

func TestManager_GetCIMDHandler(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	handler := manager.GetCIMDHandler()
	if handler == nil {
		t.Error("Expected non-nil CIMD handler")
	}
}

func TestManager_GetCIMDHandler_NilManager(t *testing.T) {
	var manager *Manager
	handler := manager.GetCIMDHandler()
	if handler != nil {
		t.Error("Expected nil handler for nil manager")
	}
}

func TestManager_CreateAuthChallenge_NilManager(t *testing.T) {
	var manager *Manager
	ctx := context.Background()
	_, err := manager.CreateAuthChallenge(ctx, "session", "server", &WWWAuthenticateParams{})
	if err == nil {
		t.Error("Expected error for nil manager")
	}
}

func TestManager_HandleCallback_NilManager(t *testing.T) {
	var manager *Manager
	ctx := context.Background()
	err := manager.HandleCallback(ctx, "code", "state")
	if err == nil {
		t.Error("Expected error for nil manager")
	}
}

func TestManager_HandleCallback_InvalidState(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "client-id",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	ctx := context.Background()
	err := manager.HandleCallback(ctx, "code", "invalid-state")
	if err == nil {
		t.Error("Expected error for invalid state")
	}
}

func TestManager_CreateAuthChallenge(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "client-id",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	// CreateAuthChallenge will fail without a valid issuer that returns metadata
	// but we can test that it registers the server config
	authParams := &WWWAuthenticateParams{
		Scheme: "Bearer",
		Realm:  "https://invalid-issuer.example.com",
		Scope:  "openid profile",
	}

	ctx := context.Background()
	_, err := manager.CreateAuthChallenge(ctx, "session-123", "mcp-server", authParams)
	// Expected to fail because the issuer doesn't return valid metadata
	if err == nil {
		// If it succeeds (unlikely), that's also fine
		t.Log("CreateAuthChallenge succeeded unexpectedly")
	}

	// Verify server config was registered
	serverCfg := manager.GetServerConfig("mcp-server")
	if serverCfg == nil {
		t.Error("Expected server config to be registered")
	} else {
		if serverCfg.Issuer != "https://invalid-issuer.example.com" {
			t.Errorf("Expected issuer %q, got %q", "https://invalid-issuer.example.com", serverCfg.Issuer)
		}
	}
}

func TestNewManager_SelfHostedCIMD(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "", // Empty - should auto-derive and self-host
		CallbackPath: "/oauth/proxy/callback",
		CIMDPath:     "/.well-known/oauth-client.json",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	if !manager.ShouldServeCIMD() {
		t.Error("Manager should serve CIMD when clientId is empty")
	}

	cimdPath := manager.GetCIMDPath()
	if cimdPath != "/.well-known/oauth-client.json" {
		t.Errorf("Expected CIMD path %q, got %q", "/.well-known/oauth-client.json", cimdPath)
	}
}
