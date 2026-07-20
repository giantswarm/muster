package aggregator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/internal/server"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
)

// testSessionCtx returns a context with test session and subject values injected.
func testSessionCtx() context.Context {
	ctx := api.WithSubject(context.Background(), "test-user")
	return api.WithSessionID(ctx, "test-session")
}

// getServerInfo is a test helper that retrieves a ServerInfo by name, failing the test if not found.
func getServerInfo(t *testing.T, reg *ServerRegistry, name string) *ServerInfo {
	t.Helper()
	info, ok := reg.GetServerInfo(name)
	if !ok {
		t.Fatalf("server %q not found in registry", name)
	}
	return info
}

func TestHandleAuthStatusResource_DegradedWithoutSession(t *testing.T) {
	aggServer := &AggregatorServer{registry: NewServerRegistry("x")}

	err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "auth-server", ToolPrefix: "auth"},
		URL:                "https://auth.example.com",
		AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com"},
	})
	if err != nil {
		t.Fatalf("failed to register server: %v", err)
	}

	result, err := aggServer.handleAuthStatusResource(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}

	textContent, ok := result[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", result[0])
	}

	var response pkgoauth.AuthStatusResponse
	if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	assert.Len(t, response.Servers, 1)
	assert.Equal(t, pkgoauth.SessionServerStatusAuthRequired, response.Servers[0].Status)
}

func TestHandleAuthStatusResource_NoServers(t *testing.T) {
	// Create a minimal aggregator server with an empty registry
	aggServer := &AggregatorServer{
		registry: NewServerRegistry("x"),
	}

	// Call the handler
	result, err := aggServer.handleAuthStatusResource(testSessionCtx(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return one content item
	if len(result) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result))
	}

	// Parse the response
	textContent, ok := result[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", result[0])
	}

	var response pkgoauth.AuthStatusResponse
	if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Should have empty servers list
	if len(response.Servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(response.Servers))
	}
}

func TestHandleAuthStatusResource_WithAuthRequiredServer(t *testing.T) {
	// Create a minimal aggregator server
	aggServer := &AggregatorServer{
		registry: NewServerRegistry("x"),
	}

	// Add a server in auth_required state
	err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "test-server", ToolPrefix: "test"},
		URL:                "https://test.example.com",
		AuthInfo: &AuthInfo{
			Issuer: "https://dex.example.com",
			Scope:  "openid profile",
		},
	})
	if err != nil {
		t.Fatalf("failed to register pending auth server: %v", err)
	}

	// Call the handler
	result, err := aggServer.handleAuthStatusResource(testSessionCtx(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse the response
	textContent, ok := result[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", result[0])
	}

	var response pkgoauth.AuthStatusResponse
	if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Should have one server
	if len(response.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(response.Servers))
	}

	// Check server status
	srv := response.Servers[0]
	if srv.Name != "test-server" {
		t.Errorf("expected server name 'test-server', got '%s'", srv.Name)
	}
	if srv.Status != "auth_required" {
		t.Errorf("expected status 'auth_required', got '%s'", srv.Status)
	}
	if srv.Issuer != "https://dex.example.com" {
		t.Errorf("expected issuer 'https://dex.example.com', got '%s'", srv.Issuer)
	}
	if srv.Scope != "openid profile" {
		t.Errorf("expected scope 'openid profile', got '%s'", srv.Scope)
	}
	// Auth tool should be prefixed
	if srv.AuthTool == "" {
		t.Error("expected auth tool to be set")
	}
}

func TestHandleAuthStatusResource_SSOServerNoAuthTool(t *testing.T) {
	t.Run("token forwarding server does not get AuthTool", func(t *testing.T) {
		aggServer := &AggregatorServer{
			registry: NewServerRegistry("x"),
		}

		err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "sso-fwd-server", ToolPrefix: "ssofwd"},
			URL:                "https://sso-fwd.example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
		})
		if err != nil {
			t.Fatalf("failed to register server: %v", err)
		}

		result, err := aggServer.handleAuthStatusResource(testSessionCtx(), mcp.ReadResourceRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		textContent, ok := result[0].(mcp.TextResourceContents)
		if !ok {
			t.Fatalf("expected TextResourceContents, got %T", result[0])
		}

		var response pkgoauth.AuthStatusResponse
		if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if len(response.Servers) != 1 {
			t.Fatalf("expected 1 server, got %d", len(response.Servers))
		}

		srv := response.Servers[0]
		if srv.AuthTool != "" {
			t.Errorf("expected empty AuthTool for SSO token-forwarding server, got %q", srv.AuthTool)
		}
		if !srv.TokenForwardingEnabled {
			t.Error("expected TokenForwardingEnabled to be true")
		}
		// Issuer and scope should still be set even without AuthTool
		if srv.Issuer != "https://dex.example.com" {
			t.Errorf("expected issuer to be set, got %q", srv.Issuer)
		}
	})

	t.Run("token exchange server does not get AuthTool", func(t *testing.T) {
		aggServer := &AggregatorServer{
			registry: NewServerRegistry("x"),
		}

		err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "sso-exch-server", ToolPrefix: "ssoexch"},
			URL:                "https://sso-exch.example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			AuthConfig: &api.MCPServerAuth{
				TokenExchange: &api.TokenExchangeConfig{
					Enabled:          true,
					DexTokenEndpoint: "https://remote-dex.example.com/token",
					ConnectorID:      "cluster-a-dex",
					ClientID:         "test-client",
				},
			},
		})
		if err != nil {
			t.Fatalf("failed to register server: %v", err)
		}

		result, err := aggServer.handleAuthStatusResource(testSessionCtx(), mcp.ReadResourceRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		textContent, ok := result[0].(mcp.TextResourceContents)
		if !ok {
			t.Fatalf("expected TextResourceContents, got %T", result[0])
		}

		var response pkgoauth.AuthStatusResponse
		if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if len(response.Servers) != 1 {
			t.Fatalf("expected 1 server, got %d", len(response.Servers))
		}

		srv := response.Servers[0]
		if srv.AuthTool != "" {
			t.Errorf("expected empty AuthTool for SSO token-exchange server, got %q", srv.AuthTool)
		}
		if !srv.TokenExchangeEnabled {
			t.Error("expected TokenExchangeEnabled to be true")
		}
	})

	t.Run("non-SSO server still gets AuthTool", func(t *testing.T) {
		aggServer := &AggregatorServer{
			registry: NewServerRegistry("x"),
		}

		err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "regular-server", ToolPrefix: "reg"},
			URL:                "https://regular.example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		})
		if err != nil {
			t.Fatalf("failed to register server: %v", err)
		}

		result, err := aggServer.handleAuthStatusResource(testSessionCtx(), mcp.ReadResourceRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		textContent, ok := result[0].(mcp.TextResourceContents)
		if !ok {
			t.Fatalf("expected TextResourceContents, got %T", result[0])
		}

		var response pkgoauth.AuthStatusResponse
		if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if len(response.Servers) != 1 {
			t.Fatalf("expected 1 server, got %d", len(response.Servers))
		}

		srv := response.Servers[0]
		if srv.AuthTool != "core_auth_login" {
			t.Errorf("expected AuthTool 'core_auth_login' for non-SSO server, got %q", srv.AuthTool)
		}
	})
}

func TestDetermineSessionAuthStatus_SSOServers(t *testing.T) {
	sub := "test-user-sso"
	sessionID := "session-abc-123"

	t.Run("returns sso_pending for SSO token forwarding server with pending SSO", func(t *testing.T) {
		tracker := newSSOTracker()
		aggServer := &AggregatorServer{
			registry:   NewServerRegistry("x"),
			ssoTracker: tracker,
		}

		err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "sso-server", ToolPrefix: "sso"},
			URL:                "https://sso.example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
		})
		if err != nil {
			t.Fatalf("failed to register server: %v", err)
		}

		tracker.MarkSSOPendingIfNotPending(sub, "sso-server")

		info := getServerInfo(t, aggServer.registry, "sso-server")
		status := aggServer.determineSessionAuthStatus(sub, sessionID, "sso-server", info)
		if status != pkgoauth.SessionServerStatusSSOPending {
			t.Errorf("expected status %q, got %q", pkgoauth.SessionServerStatusSSOPending, status)
		}
	})

	t.Run("returns sso_pending for SSO token exchange server with pending SSO", func(t *testing.T) {
		tracker := newSSOTracker()
		aggServer := &AggregatorServer{
			registry:   NewServerRegistry("x"),
			ssoTracker: tracker,
		}

		err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "exchange-server", ToolPrefix: "exch"},
			URL:                "https://exchange.example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			AuthConfig: &api.MCPServerAuth{
				TokenExchange: &api.TokenExchangeConfig{
					Enabled:          true,
					DexTokenEndpoint: "https://remote-dex.example.com/token",
					ConnectorID:      "cluster-a-dex",
					ClientID:         "test-client",
				},
			},
		})
		if err != nil {
			t.Fatalf("failed to register server: %v", err)
		}

		tracker.MarkSSOPendingIfNotPending(sub, "exchange-server")

		info := getServerInfo(t, aggServer.registry, "exchange-server")
		status := aggServer.determineSessionAuthStatus(sub, sessionID, "exchange-server", info)
		if status != pkgoauth.SessionServerStatusSSOPending {
			t.Errorf("expected status %q, got %q", pkgoauth.SessionServerStatusSSOPending, status)
		}
	})

	t.Run("returns auth_required for SSO server when no pending SSO recorded", func(t *testing.T) {
		aggServer := &AggregatorServer{
			registry:   NewServerRegistry("x"),
			ssoTracker: newSSOTracker(),
		}

		err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "sso-no-pending", ToolPrefix: "sso"},
			URL:                "https://sso.example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
		})
		if err != nil {
			t.Fatalf("failed to register server: %v", err)
		}

		info := getServerInfo(t, aggServer.registry, "sso-no-pending")
		status := aggServer.determineSessionAuthStatus(sub, sessionID, "sso-no-pending", info)
		if status != pkgoauth.SessionServerStatusAuthRequired {
			t.Errorf("expected status %q, got %q", pkgoauth.SessionServerStatusAuthRequired, status)
		}
	})

	t.Run("returns reauth_required when SSO failed for the server", func(t *testing.T) {
		tracker := newSSOTracker()

		aggServer := &AggregatorServer{
			registry:   NewServerRegistry("x"),
			ssoTracker: tracker,
		}

		err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "sso-server", ToolPrefix: "sso"},
			URL:                "https://sso.example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
		})
		if err != nil {
			t.Fatalf("failed to register server: %v", err)
		}

		tracker.MarkSSOFailed(sub, "sso-server")

		info := getServerInfo(t, aggServer.registry, "sso-server")
		status := aggServer.determineSessionAuthStatus(sub, sessionID, "sso-server", info)
		if status != pkgoauth.SessionServerStatusReauthRequired {
			t.Errorf("expected status %q, got %q", pkgoauth.SessionServerStatusReauthRequired, status)
		}
	})

	t.Run("returns auth_required for non-SSO server", func(t *testing.T) {
		aggServer := &AggregatorServer{
			registry:   NewServerRegistry("x"),
			ssoTracker: newSSOTracker(),
		}

		err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "non-sso-server", ToolPrefix: "nonsso"},
			URL:                "https://non-sso.example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		})
		if err != nil {
			t.Fatalf("failed to register server: %v", err)
		}

		info := getServerInfo(t, aggServer.registry, "non-sso-server")
		status := aggServer.determineSessionAuthStatus(sub, sessionID, "non-sso-server", info)
		if status != pkgoauth.SessionServerStatusAuthRequired {
			t.Errorf("expected status %q, got %q", pkgoauth.SessionServerStatusAuthRequired, status)
		}
	})
}

func TestSSOTracker_PendingTimeout(t *testing.T) {
	tracker := newSSOTracker()

	t.Run("MarkSSOPendingIfNotPending and IsSSOPendingWithinTimeout", func(t *testing.T) {
		assert.True(t, tracker.MarkSSOPendingIfNotPending("user1", "server1"))
		assert.True(t, tracker.IsSSOPendingWithinTimeout("user1", "server1"))
		assert.False(t, tracker.IsSSOPendingWithinTimeout("user1", "server2"))
		assert.False(t, tracker.IsSSOPendingWithinTimeout("user2", "server1"))
	})

	t.Run("ClearSSOPending removes pending state", func(t *testing.T) {
		tracker.MarkSSOPendingIfNotPending("user2", "serverA")
		assert.True(t, tracker.IsSSOPendingWithinTimeout("user2", "serverA"))

		tracker.ClearSSOPending("user2", "serverA")
		assert.False(t, tracker.IsSSOPendingWithinTimeout("user2", "serverA"))
	})

	t.Run("MarkSSOPendingIfNotPending preserves first timestamp", func(t *testing.T) {
		assert.True(t, tracker.MarkSSOPendingIfNotPending("user3", "serverB"))
		// Second call returns false but does not reset the timestamp.
		assert.False(t, tracker.MarkSSOPendingIfNotPending("user3", "serverB"))
		assert.True(t, tracker.IsSSOPendingWithinTimeout("user3", "serverB"))
	})
}

func TestDetermineSessionAuthStatus_SSOPendingTimeout(t *testing.T) {
	sub := "timeout-user"
	sessionID := "timeout-session"

	tracker := newSSOTracker()
	aggServer := &AggregatorServer{
		registry:   NewServerRegistry("x"),
		ssoTracker: tracker,
	}

	err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "sso-server", ToolPrefix: "sso"},
		URL:                "https://sso.example.com",
		AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
	})
	if err != nil {
		t.Fatalf("failed to register server: %v", err)
	}
	info := getServerInfo(t, aggServer.registry, "sso-server")

	// Before marking pending: should return auth_required (not stuck as sso_pending)
	status := aggServer.determineSessionAuthStatus(sub, sessionID, "sso-server", info)
	if status != pkgoauth.SessionServerStatusAuthRequired {
		t.Errorf("expected auth_required before pending, got %q", status)
	}

	// After marking pending: should return sso_pending
	tracker.MarkSSOPendingIfNotPending(sub, "sso-server")
	status = aggServer.determineSessionAuthStatus(sub, sessionID, "sso-server", info)
	if status != pkgoauth.SessionServerStatusSSOPending {
		t.Errorf("expected sso_pending after MarkSSOPendingIfNotPending, got %q", status)
	}

	// After ClearSSOPending: should return auth_required again
	tracker.ClearSSOPending(sub, "sso-server")
	status = aggServer.determineSessionAuthStatus(sub, sessionID, "sso-server", info)
	if status != pkgoauth.SessionServerStatusAuthRequired {
		t.Errorf("expected auth_required after ClearSSOPending, got %q", status)
	}
}

func TestDetermineSessionAuthStatus_ReauthRequired_WhenSSOFailed(t *testing.T) {
	sub := "degraded-user"
	sessionID := "degraded-session"

	tracker := newSSOTracker()
	aggServer := &AggregatorServer{
		registry:   NewServerRegistry("x"),
		ssoTracker: tracker,
	}

	err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "sso-server", ToolPrefix: "sso"},
		URL:                "https://sso.example.com",
		AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
	})
	if err != nil {
		t.Fatalf("failed to register server: %v", err)
	}
	info := getServerInfo(t, aggServer.registry, "sso-server")

	// Before failure: should return auth_required
	status := aggServer.determineSessionAuthStatus(sub, sessionID, "sso-server", info)
	assert.Equal(t, pkgoauth.SessionServerStatusAuthRequired, status,
		"should be auth_required before any failure")

	// After SSO failure (e.g. refresh chain broken): should return reauth_required
	tracker.MarkSSOFailed(sub, "sso-server")
	status = aggServer.determineSessionAuthStatus(sub, sessionID, "sso-server", info)
	assert.Equal(t, pkgoauth.SessionServerStatusReauthRequired, status,
		"should be reauth_required when SSO has failed for an SSO-enabled server")
}

func TestDetermineSessionAuthStatus_ReauthRequired_TokenExchangeServer(t *testing.T) {
	sub := "exchange-user"
	sessionID := "exchange-session"

	tracker := newSSOTracker()
	aggServer := &AggregatorServer{
		registry:   NewServerRegistry("x"),
		ssoTracker: tracker,
	}

	err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "exchange-server", ToolPrefix: "exchange"},
		URL:                "https://exchange.example.com",
		AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		AuthConfig: &api.MCPServerAuth{TokenExchange: &api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://remote-dex.example.com/token",
			ConnectorID:      "cluster-a-dex",
			ClientID:         "test-client",
		}},
	})
	if err != nil {
		t.Fatalf("failed to register server: %v", err)
	}
	info := getServerInfo(t, aggServer.registry, "exchange-server")

	tracker.MarkSSOFailed(sub, "exchange-server")
	status := aggServer.determineSessionAuthStatus(sub, sessionID, "exchange-server", info)
	assert.Equal(t, pkgoauth.SessionServerStatusReauthRequired, status,
		"token exchange server with SSO failure should also return reauth_required")
}

func TestHandleAuthStatusResource_ReauthRequired_PopulatesAuthMetadata(t *testing.T) {
	sub := "reauth-user"
	tracker := newSSOTracker()
	aggServer := &AggregatorServer{
		registry:   NewServerRegistry("x"),
		ssoTracker: tracker,
	}

	err := aggServer.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "sso-server", ToolPrefix: "sso"},
		URL:                "https://sso.example.com",
		AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
	})
	if err != nil {
		t.Fatalf("failed to register server: %v", err)
	}

	tracker.MarkSSOFailed(sub, "sso-server")

	ctx := api.WithSubject(context.Background(), sub)
	ctx = api.WithSessionID(ctx, "reauth-session")

	result, err := aggServer.handleAuthStatusResource(ctx, mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	textContent, ok := result[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", result[0])
	}

	var response pkgoauth.AuthStatusResponse
	if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	assert.Len(t, response.Servers, 1)
	srv := response.Servers[0]
	assert.Equal(t, pkgoauth.SessionServerStatusReauthRequired, srv.Status,
		"status should be reauth_required when SSO has failed")
	assert.Equal(t, "https://dex.example.com", srv.Issuer,
		"issuer should be populated for reauth_required")
	assert.Equal(t, "openid", srv.Scope,
		"scope should be populated for reauth_required")
	assert.Equal(t, "core_auth_login", srv.AuthTool,
		"AuthTool should be core_auth_login for reauth_required so the agent can prompt re-authentication")
	assert.True(t, srv.TokenForwardingEnabled,
		"TokenForwardingEnabled should be true")
	assert.True(t, srv.SSOAttemptFailed,
		"SSOAttemptFailed should be true when SSO has failed")
}

func TestAuthStatusResponse_MarshalJSON(t *testing.T) {
	response := pkgoauth.AuthStatusResponse{
		Servers: []pkgoauth.ServerAuthStatus{
			{
				Name:     "server1",
				Status:   "connected",
				Issuer:   "",
				Scope:    "",
				AuthTool: "",
			},
			{
				Name:     "server2",
				Status:   "auth_required",
				Issuer:   "https://idp.example.com",
				Scope:    "openid",
				AuthTool: "core_auth_login",
			},
		},
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	// Unmarshal and verify
	var parsed pkgoauth.AuthStatusResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(parsed.Servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(parsed.Servers))
	}
}

// TestStoreIDTokenForSSO_SetsExpiresAtFromJWT locks in the contract that the
// stored proxy-side token must carry an ExpiresAt derived from the JWT `exp`
// claim. A zero ExpiresAt is treated as never-expiring by IsExpiredWithMargin.
func TestStoreIDTokenForSSO_SetsExpiresAtFromJWT(t *testing.T) {
	mock := newMockOAuthHandler(true)
	api.RegisterOAuthHandler(mock)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	a := &AggregatorServer{
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config: config.OAuthServerConfig{
					Provider: "generic",
					BaseURL:  "https://muster.example",
				},
			},
		},
	}

	t.Run("populates ExpiresAt when JWT carries an exp claim", func(t *testing.T) {
		// JWT payload: {"sub":"alice","exp":9999999999} (year 2286).
		idToken := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSIsImV4cCI6OTk5OTk5OTk5OX0.sig"
		a.storeIDTokenForSSO("family-1", "alice", idToken)

		stored := mock.GetFullTokenByIssuer("family-1", "https://muster.example")
		if stored == nil {
			t.Fatal("token was not stored")
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
		mock.tokens = map[string]*api.OAuthToken{}
		a.storeIDTokenForSSO("family-2", "bob", "not-a-jwt")

		if stored := mock.GetFullTokenByIssuer("family-2", "https://muster.example"); stored != nil {
			t.Fatalf("unparseable token must not be stored (would land as never-expiring), got %+v", stored)
		}
	})

	t.Run("refuses to store a JWT without exp", func(t *testing.T) {
		mock.tokens = map[string]*api.OAuthToken{}
		// Payload: {"sub":"carol"} — no exp.
		idToken := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJjYXJvbCJ9.sig"
		a.storeIDTokenForSSO("family-3", "carol", idToken)

		if stored := mock.GetFullTokenByIssuer("family-3", "https://muster.example"); stored != nil {
			t.Fatalf("JWT without exp must not be stored, got %+v", stored)
		}
	})

	t.Run("stored entry is IsExpiredWithMargin-expired when JWT exp is in the past", func(t *testing.T) {
		mock.tokens = map[string]*api.OAuthToken{}
		// Payload: {"sub":"dave","exp":1} — Unix epoch + 1s.
		idToken := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJkYXZlIiwiZXhwIjoxfQ.sig"
		a.storeIDTokenForSSO("family-4", "dave", idToken)

		stored := mock.GetFullTokenByIssuer("family-4", "https://muster.example")
		if stored == nil {
			t.Fatal("token with parseable past exp must still be stored — eviction is the consumer's job")
		}
		token := &pkgoauth.Token{ExpiresAt: stored.ExpiresAt}
		if !token.IsExpiredWithMargin(0) {
			t.Errorf("stored entry should be IsExpiredWithMargin(0)-expired; ExpiresAt=%v", stored.ExpiresAt)
		}
	})
}

// TestInitSSOForSession_PersistsIDToken locks in the fix for the garm
// re-exchange rotation-storm deauth. A session that reconnects after its
// login-time ID token expired (e.g. after a pod restart) re-inits SSO here from
// the live request-context ID token; that token MUST be persisted to the
// OAuth-proxy store so the background re-exchange closure
// (getIDTokenForForwarding, which runs on a detached context and can only read
// the store) can resolve a subject. Without this, every background re-exchange
// fails with "no subject ID token available for re-exchange" and the fallback
// refresher rotates the client's refresh token in a tight retry loop until
// OAuth 2.1 reuse detection revokes the family and deauths the session.
func TestInitSSOForSession_PersistsIDToken(t *testing.T) {
	mock := newMockOAuthHandler(true)
	api.RegisterOAuthHandler(mock)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	a := &AggregatorServer{
		registry: NewServerRegistry("x"),
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config: config.OAuthServerConfig{
					Provider: "generic",
					BaseURL:  "https://muster.example",
				},
			},
		},
	}

	// JWT payload: {"sub":"alice","exp":9999999999} (year 2286).
	idToken := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSIsImV4cCI6OTk5OTk5OTk5OX0.sig"
	a.initSSOForSession(ssoSession{
		userID:    "alice",
		sessionID: "family-reconnect",
		tokens:    server.CallerTokens{IDToken: idToken},
	})

	stored := mock.GetFullTokenByIssuer("family-reconnect", "https://muster.example")
	if stored == nil {
		t.Fatal("initSSOForSession must persist the request-context ID token to the proxy store so background re-exchange can resolve it")
	}
	if stored.IDToken != idToken {
		t.Errorf("stored IDToken = %q, want %q", stored.IDToken, idToken)
	}

	t.Run("no-ops when the caller carries no ID token", func(t *testing.T) {
		mock.tokens = map[string]*api.OAuthToken{}
		a.initSSOForSession(ssoSession{
			userID:    "bob",
			sessionID: "family-no-idtoken",
			tokens:    server.CallerTokens{},
		})
		if stored := mock.GetFullTokenByIssuer("family-no-idtoken", "https://muster.example"); stored != nil {
			t.Fatalf("must not store anything when the caller has no ID token, got %+v", stored)
		}
	})
}
