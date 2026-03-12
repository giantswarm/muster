package oauth

import (
	"context"
	"testing"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/config"
)

func TestNewManager_Disabled(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
		Enabled: false,
	}

	manager := NewManager(cfg)
	if manager != nil {
		t.Error("Expected nil manager when OAuth is disabled")
	}
}

func TestNewManager_Enabled(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
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

	// No server registered, should return nil
	token := manager.GetToken("user-123", "unknown-server")
	if token != nil {
		t.Error("Expected nil token for unknown server")
	}
}

func TestManager_GetToken_NilManager(t *testing.T) {
	var manager *Manager
	token := manager.GetToken("user@example.com", "server")
	if token != nil {
		t.Error("Expected nil token for nil manager")
	}
}

func TestManager_GetTokenByIssuer_NilManager(t *testing.T) {
	var manager *Manager
	token := manager.GetTokenByIssuer("user@example.com", "issuer")
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

	// Should not panic
	manager.Stop()
}

func TestManager_GetToken_WithToken(t *testing.T) {
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

	serverName := "mcp-kubernetes"
	issuer := "https://auth.example.com"
	scope := "openid profile"
	subject := "user-123"

	// Register the server
	manager.RegisterServer(serverName, issuer, scope)

	// Store a token directly in the client
	testToken := &pkgoauth.Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       scope,
		Issuer:      issuer,
	}
	manager.client.StoreToken(subject, testToken)

	// Now GetToken should return the token
	token := manager.GetToken(subject, serverName)
	if token == nil {
		t.Fatal("Expected token")
	}

	if token.AccessToken != testToken.AccessToken {
		t.Errorf("Expected access token %q, got %q", testToken.AccessToken, token.AccessToken)
	}
}

func TestManager_GetTokenByIssuer(t *testing.T) {
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

	issuer := "https://auth.example.com"
	subject := "user-123"

	// Store a token directly
	testToken := &pkgoauth.Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       "openid",
		Issuer:      issuer,
	}
	manager.client.StoreToken(subject, testToken)

	// GetTokenByIssuer should find it
	token := manager.GetTokenByIssuer(subject, issuer)
	if token == nil {
		t.Fatal("Expected token")
	}

	if token.AccessToken != testToken.AccessToken {
		t.Errorf("Expected access token %q, got %q", testToken.AccessToken, token.AccessToken)
	}
}

func TestManager_ClearTokenByIssuer(t *testing.T) {
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

	issuer := "https://auth.example.com"
	subject := "user-123"

	// Store a token directly
	testToken := &pkgoauth.Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       "openid",
		Issuer:      issuer,
	}
	manager.client.StoreToken(subject, testToken)

	// Verify token exists
	token := manager.GetTokenByIssuer(subject, issuer)
	if token == nil {
		t.Fatal("Expected token before clearing")
	}

	// Clear the token
	manager.ClearTokenByIssuer(subject, issuer)

	// Verify token is gone
	token = manager.GetTokenByIssuer(subject, issuer)
	if token != nil {
		t.Error("Expected nil token after clearing")
	}
}

func TestManager_ClearTokenByIssuer_NilManager(t *testing.T) {
	var manager *Manager
	// Should not panic
	manager.ClearTokenByIssuer("user@example.com", "issuer")
}

func TestManager_GetCIMDPath(t *testing.T) {
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
	_, err := manager.CreateAuthChallenge(ctx, "user@example.com", "server", "", "")
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

	ctx := context.Background()
	err := manager.HandleCallback(ctx, "code", "invalid-state")
	if err == nil {
		t.Error("Expected error for invalid state")
	}
}

func TestManager_CreateAuthChallenge(t *testing.T) {
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

	// CreateAuthChallenge will fail without a valid issuer that returns metadata
	// but we can test that it registers the server config
	issuer := "https://invalid-issuer.example.com"
	scope := "openid profile"

	ctx := context.Background()
	_, err := manager.CreateAuthChallenge(ctx, "user-123", "mcp-server", issuer, scope)
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

func TestManager_DeleteTokensByUser(t *testing.T) {
	t.Run("removes all tokens for the given subject", func(t *testing.T) {
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

		subject := "user@example.com"
		otherSubject := "other@example.com"

		// Store tokens for the target subject under two different issuers
		for _, issuer := range []string{"https://issuer1.example.com", "https://issuer2.example.com"} {
			manager.client.StoreToken(subject, &pkgoauth.Token{
				AccessToken: "token-for-" + issuer,
				TokenType:   "Bearer",
				ExpiresIn:   3600,
				Issuer:      issuer,
			})
		}

		// Store a token for a different subject that must not be removed
		manager.client.StoreToken(otherSubject, &pkgoauth.Token{
			AccessToken: "other-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
			Issuer:      "https://issuer1.example.com",
		})

		// Verify 3 tokens exist
		if manager.client.tokenStore.Count() != 3 {
			t.Fatalf("expected 3 tokens before deletion, got %d", manager.client.tokenStore.Count())
		}

		// Delete all tokens for the target subject
		manager.DeleteTokensByUser(subject)

		// Only the other subject's token should remain
		if manager.client.tokenStore.Count() != 1 {
			t.Errorf("expected 1 token after deletion, got %d", manager.client.tokenStore.Count())
		}

		// Verify target subject's tokens are gone
		if manager.GetTokenByIssuer(subject, "https://issuer1.example.com") != nil {
			t.Error("expected issuer1 token to be deleted for target subject")
		}
		if manager.GetTokenByIssuer(subject, "https://issuer2.example.com") != nil {
			t.Error("expected issuer2 token to be deleted for target subject")
		}

		// Verify other subject's token still exists
		if manager.GetTokenByIssuer(otherSubject, "https://issuer1.example.com") == nil {
			t.Error("expected other subject's token to remain after deletion")
		}
	})

	t.Run("is a no-op when subject has no tokens", func(t *testing.T) {
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

		// Should not panic when the subject has no tokens
		manager.DeleteTokensByUser("no-tokens@example.com")

		if manager.client.tokenStore.Count() != 0 {
			t.Errorf("expected 0 tokens, got %d", manager.client.tokenStore.Count())
		}
	})

	t.Run("does not panic on nil manager", func(t *testing.T) {
		var manager *Manager
		// Should not panic
		manager.DeleteTokensByUser("user@example.com")
	})
}

func TestNewManager_SelfHostedCIMD(t *testing.T) {
	cfg := config.OAuthMCPClientConfig{
		Enabled:      true,
		PublicURL:    "https://muster.example.com",
		ClientID:     "", // Empty - should auto-derive and self-host
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

	if !manager.ShouldServeCIMD() {
		t.Error("Manager should serve CIMD when clientId is empty")
	}

	cimdPath := manager.GetCIMDPath()
	if cimdPath != "/.well-known/oauth-client.json" {
		t.Errorf("Expected CIMD path %q, got %q", "/.well-known/oauth-client.json", cimdPath)
	}
}
