package aggregator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/internal/api"
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

	err := aggServer.registry.RegisterPendingAuth(
		"auth-server", "https://auth.example.com", "auth",
		&AuthInfo{Issuer: "https://dex.example.com"},
	)
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
	err := aggServer.registry.RegisterPendingAuth(
		"test-server",
		"https://test.example.com",
		"test",
		&AuthInfo{
			Issuer: "https://dex.example.com",
			Scope:  "openid profile",
		},
	)
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

		err := aggServer.registry.RegisterPendingAuthWithConfig(
			"sso-fwd-server",
			"https://sso-fwd.example.com",
			"ssofwd",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{ForwardToken: true},
		)
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

		err := aggServer.registry.RegisterPendingAuthWithConfig(
			"sso-exch-server",
			"https://sso-exch.example.com",
			"ssoexch",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{
				TokenExchange: &api.TokenExchangeConfig{
					Enabled:          true,
					DexTokenEndpoint: "https://remote-dex.example.com/token",
					ConnectorID:      "cluster-a-dex",
					ClientID:         "test-client",
				},
			},
		)
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

		err := aggServer.registry.RegisterPendingAuth(
			"regular-server",
			"https://regular.example.com",
			"reg",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		)
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

		err := aggServer.registry.RegisterPendingAuthWithConfig(
			"sso-server",
			"https://sso.example.com",
			"sso",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{ForwardToken: true},
		)
		if err != nil {
			t.Fatalf("failed to register server: %v", err)
		}

		tracker.MarkSSOPending(sub, "sso-server")

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

		err := aggServer.registry.RegisterPendingAuthWithConfig(
			"exchange-server",
			"https://exchange.example.com",
			"exch",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{
				TokenExchange: &api.TokenExchangeConfig{
					Enabled:          true,
					DexTokenEndpoint: "https://remote-dex.example.com/token",
					ConnectorID:      "cluster-a-dex",
					ClientID:         "test-client",
				},
			},
		)
		if err != nil {
			t.Fatalf("failed to register server: %v", err)
		}

		tracker.MarkSSOPending(sub, "exchange-server")

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

		err := aggServer.registry.RegisterPendingAuthWithConfig(
			"sso-no-pending",
			"https://sso.example.com",
			"sso",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{ForwardToken: true},
		)
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

		err := aggServer.registry.RegisterPendingAuthWithConfig(
			"sso-server",
			"https://sso.example.com",
			"sso",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{ForwardToken: true},
		)
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

		err := aggServer.registry.RegisterPendingAuth(
			"non-sso-server",
			"https://non-sso.example.com",
			"nonsso",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		)
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

	t.Run("MarkSSOPending and IsSSOPendingWithinTimeout", func(t *testing.T) {
		tracker.MarkSSOPending("user1", "server1")
		assert.True(t, tracker.IsSSOPendingWithinTimeout("user1", "server1"))
		assert.False(t, tracker.IsSSOPendingWithinTimeout("user1", "server2"))
		assert.False(t, tracker.IsSSOPendingWithinTimeout("user2", "server1"))
	})

	t.Run("ClearSSOPending removes pending state", func(t *testing.T) {
		tracker.MarkSSOPending("user2", "serverA")
		assert.True(t, tracker.IsSSOPendingWithinTimeout("user2", "serverA"))

		tracker.ClearSSOPending("user2", "serverA")
		assert.False(t, tracker.IsSSOPendingWithinTimeout("user2", "serverA"))
	})

	t.Run("MarkSSOPending preserves first timestamp", func(t *testing.T) {
		tracker.MarkSSOPending("user3", "serverB")
		// Calling again should not reset the timestamp
		tracker.MarkSSOPending("user3", "serverB")
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

	err := aggServer.registry.RegisterPendingAuthWithConfig(
		"sso-server",
		"https://sso.example.com",
		"sso",
		&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		&api.MCPServerAuth{ForwardToken: true},
	)
	if err != nil {
		t.Fatalf("failed to register server: %v", err)
	}
	info := getServerInfo(t, aggServer.registry, "sso-server")

	// Before MarkSSOPending: should return auth_required (not stuck as sso_pending)
	status := aggServer.determineSessionAuthStatus(sub, sessionID, "sso-server", info)
	if status != pkgoauth.SessionServerStatusAuthRequired {
		t.Errorf("expected auth_required before pending, got %q", status)
	}

	// After MarkSSOPending: should return sso_pending
	tracker.MarkSSOPending(sub, "sso-server")
	status = aggServer.determineSessionAuthStatus(sub, sessionID, "sso-server", info)
	if status != pkgoauth.SessionServerStatusSSOPending {
		t.Errorf("expected sso_pending after MarkSSOPending, got %q", status)
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

	err := aggServer.registry.RegisterPendingAuthWithConfig(
		"sso-server",
		"https://sso.example.com",
		"sso",
		&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		&api.MCPServerAuth{ForwardToken: true},
	)
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

	err := aggServer.registry.RegisterPendingAuthWithConfig(
		"exchange-server",
		"https://exchange.example.com",
		"exchange",
		&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		&api.MCPServerAuth{TokenExchange: &api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://remote-dex.example.com/token",
			ConnectorID:      "cluster-a-dex",
			ClientID:         "test-client",
		}},
	)
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

	err := aggServer.registry.RegisterPendingAuthWithConfig(
		"sso-server",
		"https://sso.example.com",
		"sso",
		&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		&api.MCPServerAuth{ForwardToken: true},
	)
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
