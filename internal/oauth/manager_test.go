package oauth

import (
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
		ClientID:     "https://giantswarm.github.io/muster/oauth-client.json",
		CallbackPath: "/oauth/callback",
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
		CallbackPath: "/oauth/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	path := manager.GetCallbackPath()
	if path != "/oauth/callback" {
		t.Errorf("Expected callback path %q, got %q", "/oauth/callback", path)
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
		CallbackPath: "/oauth/callback",
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
		CallbackPath: "/oauth/callback",
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
		CallbackPath: "/oauth/callback",
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
		CallbackPath: "/oauth/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}

	// Should not panic
	manager.Stop()
}
