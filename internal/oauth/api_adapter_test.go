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
	token := adapter.GetToken("user@example.com", "server")
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
	token := adapter.GetTokenByIssuer("user@example.com", "issuer")
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
	token := adapter.GetTokenByIssuer(subject, issuer)
	if token == nil {
		t.Fatal("Expected token before clearing")
	}

	// Clear the token via adapter
	adapter.ClearTokenByIssuer(subject, issuer)

	// Verify token is gone
	token = adapter.GetTokenByIssuer(subject, issuer)
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

func TestAdapter_DeleteTokensByUser(t *testing.T) {
	t.Run("delegates to manager and removes all tokens for subject", func(t *testing.T) {
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

		subject := "user@example.com"
		issuer := "https://auth.example.com"

		// Store a token directly via manager internals
		manager.client.StoreToken(subject, &pkgoauth.Token{
			AccessToken: "access-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
			Issuer:      issuer,
		})

		// Verify the token exists via the adapter
		if adapter.GetTokenByIssuer(subject, issuer) == nil {
			t.Fatal("expected token to exist before deletion")
		}

		// Delete via adapter
		adapter.DeleteTokensByUser(subject)

		// Token should be gone
		if adapter.GetTokenByIssuer(subject, issuer) != nil {
			t.Error("expected token to be deleted after DeleteTokensByUser")
		}
	})

	t.Run("does not affect tokens for other subjects", func(t *testing.T) {
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

		targetSubject := "target@example.com"
		otherSubject := "other@example.com"
		issuer := "https://auth.example.com"

		// Store tokens for both subjects
		manager.client.StoreToken(targetSubject, &pkgoauth.Token{
			AccessToken: "target-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
			Issuer:      issuer,
		})
		manager.client.StoreToken(otherSubject, &pkgoauth.Token{
			AccessToken: "other-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
			Issuer:      issuer,
		})

		// Delete only target subject's tokens
		adapter.DeleteTokensByUser(targetSubject)

		// Target subject's token should be gone
		if adapter.GetTokenByIssuer(targetSubject, issuer) != nil {
			t.Error("expected target subject's token to be deleted")
		}

		// Other subject's token should still exist
		if adapter.GetTokenByIssuer(otherSubject, issuer) == nil {
			t.Error("expected other subject's token to remain")
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

		adapter := NewAdapter(manager)

		// Should not panic when no tokens exist for this subject
		adapter.DeleteTokensByUser("nobody@example.com")
	})
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
