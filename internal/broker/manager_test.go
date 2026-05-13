package broker

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/config"
)

const (
	testServerName = "mcp-kubernetes"
	testIssuer     = "https://auth.example.com"
	testScopes     = "openid profile"
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
	if path != "/oauth/proxy/callback" { //nolint:goconst
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

	serverName := testServerName
	issuer := testIssuer
	scope := testScopes

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

	serverName := testServerName
	issuer := testIssuer
	scope := testScopes
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
	manager.client.StoreToken(subject, "test-user", testToken)

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

	issuer := testIssuer
	subject := "user-123"

	// Store a token directly
	testToken := &pkgoauth.Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       "openid",
		Issuer:      issuer,
	}
	manager.client.StoreToken(subject, "test-user", testToken)

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

	issuer := testIssuer
	subject := "user-123"

	// Store a token directly
	testToken := &pkgoauth.Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       "openid",
		Issuer:      issuer,
	}
	manager.client.StoreToken(subject, "test-user", testToken)

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
	ctx := t.Context()
	_, err := manager.CreateAuthChallenge(ctx, "user@example.com", "test-user", "server", "", "")
	if err == nil {
		t.Error("Expected error for nil manager")
	}
}

func TestManager_HandleCallback_NilManager(t *testing.T) {
	var manager *Manager
	ctx := t.Context()
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

	ctx := t.Context()
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
	scope := testScopes

	ctx := t.Context()
	_, err := manager.CreateAuthChallenge(ctx, "user-123", "test-user", "mcp-server", issuer, scope)
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

		targetUserID := "target-user"
		otherUserID := "other-user"
		sessionA := "session-A"
		sessionB := "session-B"

		for _, issuer := range []string{"https://issuer1.example.com", "https://issuer2.example.com"} {
			manager.client.StoreToken(sessionA, targetUserID, &pkgoauth.Token{
				AccessToken: "token-for-" + issuer,
				TokenType:   "Bearer",
				ExpiresIn:   3600,
				Issuer:      issuer,
			})
		}

		manager.client.StoreToken(sessionB, otherUserID, &pkgoauth.Token{
			AccessToken: "other-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
			Issuer:      "https://issuer1.example.com",
		})

		if manager.client.tokenStore.Count() != 3 {
			t.Fatalf("expected 3 tokens before deletion, got %d", manager.client.tokenStore.Count())
		}

		manager.DeleteTokensByUser(targetUserID)

		if manager.client.tokenStore.Count() != 1 {
			t.Errorf("expected 1 token after deletion, got %d", manager.client.tokenStore.Count())
		}

		if manager.GetTokenByIssuer(sessionA, "https://issuer1.example.com") != nil {
			t.Error("expected issuer1 token to be deleted for target user")
		}
		if manager.GetTokenByIssuer(sessionA, "https://issuer2.example.com") != nil {
			t.Error("expected issuer2 token to be deleted for target user")
		}

		if manager.GetTokenByIssuer(sessionB, "https://issuer1.example.com") == nil {
			t.Error("expected other user's token to remain after deletion")
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

func TestManager_SessionIssuer_DeterministicAcrossMultipleIssuers(t *testing.T) {
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

	const sessionID = "session-multi-issuer"
	// Store tokens for three issuers in non-sorted order; the function
	// must return the lexicographically-smallest issuer every call.
	for _, issuer := range []string{
		"https://idp-c.example.com",
		"https://idp-a.example.com",
		"https://idp-b.example.com",
	} {
		manager.StoreToken(sessionID, "user", issuer, &pkgoauth.Token{
			AccessToken: "access-" + issuer,
			IDToken:     "id-" + issuer,
			Issuer:      issuer,
		})
	}

	const expected = "https://idp-a.example.com"
	for range 10 {
		got, err := manager.SessionIssuer(t.Context(), sessionID)
		if err != nil {
			t.Fatalf("SessionIssuer returned error: %v", err)
		}
		if got != expected {
			t.Fatalf("SessionIssuer returned %q, expected stable %q (Go map iteration order leaked)", got, expected)
		}
	}
}

func TestManager_SessionIssuer_UnknownSession(t *testing.T) {
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

	_, err := manager.SessionIssuer(t.Context(), "no-such-session")
	if !errors.Is(err, ErrSessionUnknown) {
		t.Fatalf("expected ErrSessionUnknown, got %v", err)
	}
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

func TestManager_PersistMusterIDToken_SetsExpiresAtFromJWT(t *testing.T) {
	m := NewManager(config.OAuthMCPClientConfig{
		Enabled:      true,
		PublicURL:    "https://muster.test",
		ClientID:     "muster-test",
		CallbackPath: "/oauth/proxy/callback",
	})
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	t.Cleanup(m.Stop)
	m.SetMusterIssuer("https://muster.example")

	t.Run("populates ExpiresAt when JWT carries an exp claim", func(t *testing.T) {
		idToken := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSIsImV4cCI6OTk5OTk5OTk5OX0.sig" //nolint:gosec
		if err := m.PersistMusterIDToken("family-1", "alice", idToken); err != nil {
			t.Fatalf("PersistMusterIDToken returned error: %v", err)
		}
		stored := m.GetTokenByIssuer("family-1", "https://muster.example")
		if stored == nil {
			t.Fatal("token not stored")
		}
		if stored.ExpiresAt.IsZero() {
			t.Fatal("ExpiresAt should be populated from JWT exp claim")
		}
		want := time.Unix(9999999999, 0)
		if !stored.ExpiresAt.Equal(want) {
			t.Errorf("ExpiresAt = %v, want %v", stored.ExpiresAt, want)
		}
	})

	t.Run("refuses to store an unparseable token", func(t *testing.T) {
		err := m.PersistMusterIDToken("family-2", "bob", "not-a-jwt")
		if err == nil {
			t.Fatal("expected error for unparseable token")
		}
		if stored := m.GetTokenByIssuer("family-2", "https://muster.example"); stored != nil {
			t.Fatalf("unparseable token must not be stored (would land as never-expiring), got %+v", stored)
		}
	})

	t.Run("refuses to store a JWT without exp", func(t *testing.T) {
		idToken := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJjYXJvbCJ9.sig" //nolint:gosec
		err := m.PersistMusterIDToken("family-3", "carol", idToken)
		if err == nil {
			t.Fatal("expected error for JWT without exp")
		}
		if stored := m.GetTokenByIssuer("family-3", "https://muster.example"); stored != nil {
			t.Fatalf("JWT without exp must not be stored, got %+v", stored)
		}
	})

	t.Run("no-op when muster issuer is unset", func(t *testing.T) {
		m2 := NewManager(config.OAuthMCPClientConfig{
			Enabled:      true,
			PublicURL:    "https://muster.test",
			ClientID:     "muster-test",
			CallbackPath: "/oauth/proxy/callback",
		})
		t.Cleanup(m2.Stop)
		idToken := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJlIiwiZXhwIjo5OTk5OTk5OTk5fQ.sig" //nolint:gosec
		if err := m2.PersistMusterIDToken("family-4", "eve", idToken); err != nil {
			t.Fatalf("PersistMusterIDToken should silently no-op when issuer unset, got %v", err)
		}
	})

	t.Run("JWT with past exp is accepted on write", func(t *testing.T) {
		// PersistMusterIDToken only rejects unparseable tokens. A
		// parseable JWT with an exp in the past is accepted (a write-side
		// invariant); whether subsequent reads filter expired entries is
		// the read path's concern and orthogonal to this contract.
		idToken := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJkYXZlIiwiZXhwIjoxfQ.sig" //nolint:gosec
		require.NoError(t, m.PersistMusterIDToken("family-5", "dave", idToken))
	})

	t.Run("returns ErrMalformedIDToken sentinel for malformed input", func(t *testing.T) {
		err := m.PersistMusterIDToken("family-6", "frank", "not-a-jwt")
		require.ErrorIs(t, err, ErrMalformedIDToken)
	})
}

func TestManager_ClearMusterSession_RemovesAllSessionEntries(t *testing.T) {
	m := NewManager(config.OAuthMCPClientConfig{
		Enabled:      true,
		PublicURL:    "https://muster.test",
		ClientID:     "muster-test",
		CallbackPath: "/oauth/proxy/callback",
	})
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	t.Cleanup(m.Stop)
	m.SetMusterIssuer("https://muster.example")

	idToken := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4IiwiZXhwIjo5OTk5OTk5OTk5fQ.sig" //nolint:gosec
	if err := m.PersistMusterIDToken("family-1", "x", idToken); err != nil {
		t.Fatalf("PersistMusterIDToken: %v", err)
	}
	if m.GetTokenByIssuer("family-1", "https://muster.example") == nil {
		t.Fatal("precondition: token should be stored")
	}

	m.ClearMusterSession("family-1")
	if stored := m.GetTokenByIssuer("family-1", "https://muster.example"); stored != nil {
		t.Errorf("expected session cleared, got %+v", stored)
	}
}
