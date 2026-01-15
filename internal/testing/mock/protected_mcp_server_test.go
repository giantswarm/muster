package mock

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProtectedMCPServer_StartStop(t *testing.T) {
	server, err := NewProtectedMCPServer(ProtectedMCPServerConfig{
		Name:      "test-protected",
		Transport: HTTPTransportStreamableHTTP,
		Debug:     false,
	})
	if err != nil {
		t.Fatalf("Failed to create protected MCP server: %v", err)
	}

	ctx := context.Background()

	// Start the server
	port, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start protected MCP server: %v", err)
	}

	if port == 0 {
		t.Error("Expected non-zero port")
	}

	if !server.IsRunning() {
		t.Error("Expected server to be running")
	}

	// Check endpoint
	endpoint := server.Endpoint()
	if endpoint == "" {
		t.Error("Expected non-empty endpoint")
	}

	// Stop the server
	if err := server.Stop(ctx); err != nil {
		t.Fatalf("Failed to stop protected MCP server: %v", err)
	}

	if server.IsRunning() {
		t.Error("Expected server to be stopped")
	}
}

func TestProtectedMCPServer_RequiresAuth(t *testing.T) {
	// Create OAuth server first
	oauthServer := NewOAuthServer(OAuthServerConfig{
		TokenLifetime: 1 * time.Hour,
		AutoApprove:   true,
	})

	ctx := context.Background()
	_, err := oauthServer.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start OAuth server: %v", err)
	}
	defer oauthServer.Stop(ctx)

	// Create protected MCP server
	server, err := NewProtectedMCPServer(ProtectedMCPServerConfig{
		Name:        "protected-api",
		OAuthServer: oauthServer,
		Issuer:      oauthServer.GetIssuerURL(),
		Transport:   HTTPTransportStreamableHTTP,
		Tools: []ToolConfig{
			{
				Name:        "test_tool",
				Description: "A test tool",
				Responses: []ToolResponse{
					{Response: map[string]interface{}{"status": "ok"}},
				},
			},
		},
		Debug: false,
	})
	if err != nil {
		t.Fatalf("Failed to create protected MCP server: %v", err)
	}

	_, err = server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start protected MCP server: %v", err)
	}
	defer server.Stop(ctx)

	// Wait for server to be ready
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := server.WaitForReady(readyCtx); err != nil {
		t.Fatalf("Server not ready: %v", err)
	}

	// Make request without auth - should get 401
	endpoint := server.Endpoint()
	req, _ := http.NewRequest("POST", endpoint, nil)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 without auth, got %d", resp.StatusCode)
	}

	// Check for WWW-Authenticate header with RFC 9728 format
	// Should match real mcp-kubernetes: Bearer resource_metadata="...", error="...", error_description="..."
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if wwwAuth == "" {
		t.Error("Expected WWW-Authenticate header in 401 response")
	}

	// Verify RFC 9728 format (matching real mcp-kubernetes behavior)
	if !strings.HasPrefix(wwwAuth, "Bearer") {
		t.Errorf("Expected WWW-Authenticate to start with 'Bearer', got: %s", wwwAuth)
	}
	if !strings.Contains(wwwAuth, "resource_metadata=") {
		t.Errorf("Expected WWW-Authenticate to contain 'resource_metadata=', got: %s", wwwAuth)
	}
	if !strings.Contains(wwwAuth, ".well-known/oauth-protected-resource") {
		t.Errorf("Expected WWW-Authenticate to reference protected resource metadata, got: %s", wwwAuth)
	}
	if !strings.Contains(wwwAuth, "error=") {
		t.Errorf("Expected WWW-Authenticate to contain 'error=', got: %s", wwwAuth)
	}
}

func TestProtectedMCPServer_AllowsAuthenticatedRequests(t *testing.T) {
	// Create OAuth server first
	oauthServer := NewOAuthServer(OAuthServerConfig{
		TokenLifetime: 1 * time.Hour,
		AutoApprove:   true,
	})

	ctx := context.Background()
	_, err := oauthServer.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start OAuth server: %v", err)
	}
	defer oauthServer.Stop(ctx)

	// Create protected MCP server
	server, err := NewProtectedMCPServer(ProtectedMCPServerConfig{
		Name:        "protected-api",
		OAuthServer: oauthServer,
		Issuer:      oauthServer.GetIssuerURL(),
		Transport:   HTTPTransportStreamableHTTP,
		Tools: []ToolConfig{
			{
				Name:        "test_tool",
				Description: "A test tool",
				Responses: []ToolResponse{
					{Response: map[string]interface{}{"status": "ok"}},
				},
			},
		},
		Debug: false,
	})
	if err != nil {
		t.Fatalf("Failed to create protected MCP server: %v", err)
	}

	_, err = server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start protected MCP server: %v", err)
	}
	defer server.Stop(ctx)

	// Wait for server to be ready
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := server.WaitForReady(readyCtx); err != nil {
		t.Fatalf("Server not ready: %v", err)
	}

	// Get a valid token
	code := oauthServer.GenerateAuthCode("test", "http://localhost/callback", "openid", "state", "", "")
	tokenResp, err := oauthServer.SimulateCallback(code)
	if err != nil {
		t.Fatalf("Failed to get token: %v", err)
	}

	// Make request with valid auth - should NOT get 401
	endpoint := server.Endpoint()
	req, _ := http.NewRequest("POST", endpoint, nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	resp.Body.Close()

	// The request might fail for other reasons (invalid MCP request body),
	// but it should NOT be 401 if the token is valid
	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("Got 401 with valid token - authentication should have passed")
	}
}

func TestProtectedMCPServer_SSETransport(t *testing.T) {
	server, err := NewProtectedMCPServer(ProtectedMCPServerConfig{
		Name:      "sse-protected",
		Transport: HTTPTransportSSE,
		Debug:     false,
	})
	if err != nil {
		t.Fatalf("Failed to create protected MCP server: %v", err)
	}

	ctx := context.Background()
	port, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start protected MCP server: %v", err)
	}
	defer server.Stop(ctx)

	if port == 0 {
		t.Error("Expected non-zero port")
	}

	endpoint := server.Endpoint()
	if endpoint == "" {
		t.Error("Expected non-empty endpoint")
	}

	// SSE endpoint should end with /sse
	if !strings.Contains(endpoint, "/sse") {
		t.Errorf("Expected SSE endpoint to contain /sse, got %s", endpoint)
	}
}

func TestProtectedMCPServer_GetName(t *testing.T) {
	server, _ := NewProtectedMCPServer(ProtectedMCPServerConfig{
		Name: "my-protected-server",
	})

	if server.GetName() != "my-protected-server" {
		t.Errorf("Expected name 'my-protected-server', got '%s'", server.GetName())
	}
}

func TestProtectedMCPServer_GetIssuer(t *testing.T) {
	// With explicit issuer
	server1, _ := NewProtectedMCPServer(ProtectedMCPServerConfig{
		Name:   "server1",
		Issuer: "https://auth.example.com",
	})

	if server1.GetIssuer() != "https://auth.example.com" {
		t.Errorf("Expected issuer 'https://auth.example.com', got '%s'", server1.GetIssuer())
	}

	// With OAuth server
	oauthServer := NewOAuthServer(OAuthServerConfig{
		Issuer: "https://oauth.example.com",
	})

	server2, _ := NewProtectedMCPServer(ProtectedMCPServerConfig{
		Name:        "server2",
		OAuthServer: oauthServer,
	})

	// Note: OAuthServer.GetIssuerURL() returns the configured issuer
	// The issuer from the server config takes precedence
	if server2.GetIssuer() == "" {
		t.Error("Expected non-empty issuer from OAuth server")
	}
}
