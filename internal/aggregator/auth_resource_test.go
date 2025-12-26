package aggregator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

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

	var response AuthStatusResponse
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

	var response AuthStatusResponse
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

func TestAuthStatusResponse_MarshalJSON(t *testing.T) {
	response := AuthStatusResponse{
		Servers: []ServerAuthStatus{
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
				AuthTool: "x_server2_authenticate",
			},
		},
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	// Unmarshal and verify
	var parsed AuthStatusResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(parsed.Servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(parsed.Servers))
	}
}
