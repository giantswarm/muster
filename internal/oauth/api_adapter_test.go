package oauth

import (
	"testing"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/config"
)

func TestNewAdapter(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
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

	adapter := NewAdapter(manager)
	if adapter == nil {
		t.Fatal("Expected non-nil adapter")
	}
}

func TestAdapter_IsEnabled(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
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

	adapter := NewAdapter(manager)
	if !adapter.IsEnabled() {
		t.Error("Adapter should be enabled")
	}
}

func TestAdapter_GetToken(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
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

	adapter := NewAdapter(manager)

	// Initially nil
	token := adapter.GetToken("session", "server")
	if token != nil {
		t.Error("Expected nil token initially")
	}
}

func TestAdapter_GetTokenByIssuer(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
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

	adapter := NewAdapter(manager)

	// Initially nil
	token := adapter.GetTokenByIssuer("session", "issuer")
	if token != nil {
		t.Error("Expected nil token initially")
	}
}

func TestAdapter_ClearTokenByIssuer(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
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

	adapter := NewAdapter(manager)

	issuer := "https://auth.example.com"
	sessionID := "session-123"

	// Store a token directly
	testToken := &pkgoauth.Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       "openid",
		Issuer:      issuer,
	}
	manager.client.StoreToken(sessionID, testToken)

	// Verify token exists
	token := adapter.GetTokenByIssuer(sessionID, issuer)
	if token == nil {
		t.Fatal("Expected token before clearing")
	}

	// Clear the token via adapter
	adapter.ClearTokenByIssuer(sessionID, issuer)

	// Verify token is gone
	token = adapter.GetTokenByIssuer(sessionID, issuer)
	if token != nil {
		t.Error("Expected nil token after clearing")
	}
}

func TestAdapter_GetHTTPHandler(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
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

	adapter := NewAdapter(manager)
	handler := adapter.GetHTTPHandler()
	if handler == nil {
		t.Error("Expected non-nil HTTP handler")
	}
}

func TestAdapter_GetCallbackPath(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
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

	adapter := NewAdapter(manager)
	path := adapter.GetCallbackPath()
	if path != "/oauth/proxy/callback" {
		t.Errorf("Expected callback path %q, got %q", "/oauth/proxy/callback", path)
	}
}

func TestAdapter_GetCIMDPath(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "",
		CallbackPath: "/oauth/proxy/callback",
		CIMD: config.OAuthCIMDConfig{
			Path: "/.well-known/oauth-client.json",
		},
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}
	defer manager.Stop()

	adapter := NewAdapter(manager)
	path := adapter.GetCIMDPath()
	if path != "/.well-known/oauth-client.json" {
		t.Errorf("Expected CIMD path %q, got %q", "/.well-known/oauth-client.json", path)
	}
}

func TestAdapter_ShouldServeCIMD(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
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

	adapter := NewAdapter(manager)
	if !adapter.ShouldServeCIMD() {
		t.Error("Adapter should serve CIMD when clientId is empty")
	}
}

func TestAdapter_GetCIMDHandler(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
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

	adapter := NewAdapter(manager)
	handler := adapter.GetCIMDHandler()
	if handler == nil {
		t.Error("Expected non-nil CIMD handler")
	}
}

func TestAdapter_RegisterServer(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
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

	adapter := NewAdapter(manager)

	// Should not panic
	adapter.RegisterServer("server", "issuer", "scope")

	// Verify it was registered
	serverCfg := manager.GetServerConfig("server")
	if serverCfg == nil {
		t.Error("Expected server config to be registered")
	}
}

func TestAdapter_Stop(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "client-id",
		CallbackPath: "/oauth/proxy/callback",
	}

	manager := NewManager(cfg)
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}

	adapter := NewAdapter(manager)

	// Should not panic
	adapter.Stop()
}

func TestTokenToAPIToken(t *testing.T) {
	// Test with nil token
	result := tokenToAPIToken(nil)
	if result != nil {
		t.Error("Expected nil for nil input token")
	}

	// Test with valid token
	token := &pkgoauth.Token{
		AccessToken: "access-token",
		TokenType:   "Bearer",
		Scope:       "openid profile",
	}
	result = tokenToAPIToken(token)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	if result.AccessToken != "access-token" {
		t.Errorf("Expected access token %q, got %q", "access-token", result.AccessToken)
	}
	if result.TokenType != "Bearer" {
		t.Errorf("Expected token type %q, got %q", "Bearer", result.TokenType)
	}
	if result.Scope != "openid profile" {
		t.Errorf("Expected scope %q, got %q", "openid profile", result.Scope)
	}
}
