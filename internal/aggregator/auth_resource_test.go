package aggregator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
)

// getServerInfo is a test helper that retrieves a ServerInfo by name, failing the test if not found.
func getServerInfo(t *testing.T, reg *ServerRegistry, name string) *ServerInfo {
	t.Helper()
	info, ok := reg.GetServerInfo(name)
	if !ok {
		t.Fatalf("server %q not found in registry", name)
	}
	return info
}

func TestHandleAuthStatusResource_NoServers(t *testing.T) {
	// Create a minimal aggregator server with an empty registry
	aggServer := &AggregatorServer{
		registry: NewServerRegistry("x"),
	}

	// Call the handler
	result, err := aggServer.handleAuthStatusResource(context.Background(), mcp.ReadResourceRequest{})
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
	result, err := aggServer.handleAuthStatusResource(context.Background(), mcp.ReadResourceRequest{})
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

func TestDetermineSessionAuthStatus_SSOPending(t *testing.T) {
	sessionTimeout := 5 * time.Minute
	sessionID := "test-session-sso"

	t.Run("returns sso_pending when SSO init in progress for token forwarding server", func(t *testing.T) {
		sr := NewSessionRegistry(sessionTimeout)
		defer sr.Stop()

		aggServer := &AggregatorServer{
			registry:        NewServerRegistry("x"),
			sessionRegistry: sr,
		}

		// Register an SSO server with token forwarding
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

		// Start SSO init for this session
		sr.StartSSOInit(sessionID)

		info := getServerInfo(t, aggServer.registry,"sso-server")
		status := aggServer.determineSessionAuthStatus(sessionID, "sso-server", info)
		if status != pkgoauth.ServerStatusSSOPending {
			t.Errorf("expected status %q, got %q", pkgoauth.ServerStatusSSOPending, status)
		}
	})

	t.Run("returns sso_pending when SSO init in progress for token exchange server", func(t *testing.T) {
		sr := NewSessionRegistry(sessionTimeout)
		defer sr.Stop()

		aggServer := &AggregatorServer{
			registry:        NewServerRegistry("x"),
			sessionRegistry: sr,
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

		sr.StartSSOInit(sessionID)

		info := getServerInfo(t, aggServer.registry,"exchange-server")
		status := aggServer.determineSessionAuthStatus(sessionID, "exchange-server", info)
		if status != pkgoauth.ServerStatusSSOPending {
			t.Errorf("expected status %q, got %q", pkgoauth.ServerStatusSSOPending, status)
		}
	})

	t.Run("returns auth_required when SSO init NOT in progress", func(t *testing.T) {
		sr := NewSessionRegistry(sessionTimeout)
		defer sr.Stop()

		aggServer := &AggregatorServer{
			registry:        NewServerRegistry("x"),
			sessionRegistry: sr,
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

		// No StartSSOInit call -- SSO is not in progress
		info := getServerInfo(t, aggServer.registry,"sso-server")
		status := aggServer.determineSessionAuthStatus(sessionID, "sso-server", info)
		if status != pkgoauth.ServerStatusAuthRequired {
			t.Errorf("expected status %q, got %q", pkgoauth.ServerStatusAuthRequired, status)
		}
	})

	t.Run("returns auth_required when SSO failed for the server", func(t *testing.T) {
		sr := NewSessionRegistry(sessionTimeout)
		defer sr.Stop()

		aggServer := &AggregatorServer{
			registry:        NewServerRegistry("x"),
			sessionRegistry: sr,
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

		sr.StartSSOInit(sessionID)
		sr.MarkSSOFailed(sessionID, "sso-server")

		info := getServerInfo(t, aggServer.registry,"sso-server")
		status := aggServer.determineSessionAuthStatus(sessionID, "sso-server", info)
		if status != pkgoauth.ServerStatusAuthRequired {
			t.Errorf("expected status %q, got %q", pkgoauth.ServerStatusAuthRequired, status)
		}
	})

	t.Run("returns auth_required for non-SSO server even when SSO init in progress", func(t *testing.T) {
		sr := NewSessionRegistry(sessionTimeout)
		defer sr.Stop()

		aggServer := &AggregatorServer{
			registry:        NewServerRegistry("x"),
			sessionRegistry: sr,
		}

		// Register a non-SSO server (no ForwardToken, no TokenExchange)
		err := aggServer.registry.RegisterPendingAuth(
			"non-sso-server",
			"https://non-sso.example.com",
			"nonsso",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		)
		if err != nil {
			t.Fatalf("failed to register server: %v", err)
		}

		sr.StartSSOInit(sessionID)

		info := getServerInfo(t, aggServer.registry,"non-sso-server")
		status := aggServer.determineSessionAuthStatus(sessionID, "non-sso-server", info)
		if status != pkgoauth.ServerStatusAuthRequired {
			t.Errorf("expected status %q, got %q", pkgoauth.ServerStatusAuthRequired, status)
		}
	})

	t.Run("sso_pending server does not get auth tool in full handler", func(t *testing.T) {
		sr := NewSessionRegistry(sessionTimeout)
		defer sr.Stop()

		aggServer := &AggregatorServer{
			registry:        NewServerRegistry("x"),
			sessionRegistry: sr,
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

		sr.StartSSOInit(sessionID)

		// Use the full handler to verify AuthTool is not set
		// Inject session ID into context so handleAuthStatusResource can find it
		ctx := api.WithClientSessionID(context.Background(), sessionID)
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

		if len(response.Servers) != 1 {
			t.Fatalf("expected 1 server, got %d", len(response.Servers))
		}

		srv := response.Servers[0]
		if srv.Status != pkgoauth.ServerStatusSSOPending {
			t.Errorf("expected status %q, got %q", pkgoauth.ServerStatusSSOPending, srv.Status)
		}
		if srv.AuthTool != "" {
			t.Errorf("expected empty AuthTool for sso_pending server, got %q", srv.AuthTool)
		}
	})
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
