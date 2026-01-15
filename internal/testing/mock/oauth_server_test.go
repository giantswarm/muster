package mock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOAuthServer_StartStop(t *testing.T) {
	server := NewOAuthServer(OAuthServerConfig{
		Debug: false,
	})

	ctx := context.Background()

	// Start the server
	port, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start OAuth server: %v", err)
	}

	if port == 0 {
		t.Error("Expected non-zero port")
	}

	if !server.IsRunning() {
		t.Error("Expected server to be running")
	}

	// Wait for ready
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := server.WaitForReady(readyCtx); err != nil {
		t.Fatalf("Server not ready: %v", err)
	}

	// Stop the server
	if err := server.Stop(ctx); err != nil {
		t.Fatalf("Failed to stop OAuth server: %v", err)
	}

	if server.IsRunning() {
		t.Error("Expected server to be stopped")
	}
}

func TestOAuthServer_Metadata(t *testing.T) {
	server := NewOAuthServer(OAuthServerConfig{
		AcceptedScopes: []string{"openid", "profile", "custom:scope"},
	})

	ctx := context.Background()
	port, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start OAuth server: %v", err)
	}
	defer server.Stop(ctx)

	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := server.WaitForReady(readyCtx); err != nil {
		t.Fatalf("Server not ready: %v", err)
	}

	// Fetch metadata
	resp, err := http.Get(server.GetMetadataURL())
	if err != nil {
		t.Fatalf("Failed to fetch metadata: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var metadata map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		t.Fatalf("Failed to decode metadata: %v", err)
	}

	// Check required fields
	if metadata["issuer"] == nil {
		t.Error("Missing issuer in metadata")
	}

	expectedIssuer := server.GetIssuerURL()
	if metadata["issuer"] != expectedIssuer {
		t.Errorf("Expected issuer %s, got %v", expectedIssuer, metadata["issuer"])
	}

	if metadata["authorization_endpoint"] == nil {
		t.Error("Missing authorization_endpoint in metadata")
	}

	if metadata["token_endpoint"] == nil {
		t.Error("Missing token_endpoint in metadata")
	}

	// Check port is correct
	if server.Port() != port {
		t.Errorf("Port mismatch: expected %d, got %d", port, server.Port())
	}
}

func TestOAuthServer_GenerateAndValidateToken(t *testing.T) {
	server := NewOAuthServer(OAuthServerConfig{
		TokenLifetime: 1 * time.Hour,
		AutoApprove:   true,
	})

	ctx := context.Background()
	_, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start OAuth server: %v", err)
	}
	defer server.Stop(ctx)

	// Generate an auth code
	code := server.GenerateAuthCode("test-client", "http://localhost/callback", "openid profile", "state123", "", "")

	if code == "" {
		t.Error("Expected non-empty auth code")
	}

	// Simulate callback to exchange code for token
	tokenResp, err := server.SimulateCallback(code)
	if err != nil {
		t.Fatalf("Failed to simulate callback: %v", err)
	}

	if tokenResp.AccessToken == "" {
		t.Error("Expected non-empty access token")
	}

	if tokenResp.TokenType != "Bearer" {
		t.Errorf("Expected token type Bearer, got %s", tokenResp.TokenType)
	}

	// Validate the token
	if !server.ValidateToken(tokenResp.AccessToken) {
		t.Error("Expected token to be valid")
	}

	// Invalid token should not validate
	if server.ValidateToken("invalid-token") {
		t.Error("Expected invalid token to fail validation")
	}
}

func TestOAuthServer_TokenExchange(t *testing.T) {
	server := NewOAuthServer(OAuthServerConfig{
		TokenLifetime: 1 * time.Hour,
		AutoApprove:   true,
		ClientID:      "test-client",
	})

	ctx := context.Background()
	_, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start OAuth server: %v", err)
	}
	defer server.Stop(ctx)

	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := server.WaitForReady(readyCtx); err != nil {
		t.Fatalf("Server not ready: %v", err)
	}

	// Generate an auth code
	code := server.GenerateAuthCode("test-client", "http://localhost/callback", "openid", "state456", "", "")

	// Exchange code for token via HTTP
	tokenURL := server.GetTokenURL()
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("client_id", "test-client")
	data.Set("redirect_uri", "http://localhost/callback")

	resp, err := http.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		t.Fatalf("Failed to exchange code: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("Failed to decode token response: %v", err)
	}

	if tokenResp.AccessToken == "" {
		t.Error("Expected non-empty access token")
	}

	// Validate the received token
	if !server.ValidateToken(tokenResp.AccessToken) {
		t.Error("Expected exchanged token to be valid")
	}
}

func TestOAuthServer_PKCE(t *testing.T) {
	server := NewOAuthServer(OAuthServerConfig{
		PKCERequired: true,
		AutoApprove:  true,
	})

	ctx := context.Background()
	_, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start OAuth server: %v", err)
	}
	defer server.Stop(ctx)

	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := server.WaitForReady(readyCtx); err != nil {
		t.Fatalf("Server not ready: %v", err)
	}

	// Try to authorize without PKCE - should fail
	authURL := server.GetAuthorizeURL() + "?response_type=code&client_id=test-client&redirect_uri=http://localhost/callback"

	resp, err := http.Get(authURL)
	if err != nil {
		t.Fatalf("Failed to make auth request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400 for missing PKCE, got %d", resp.StatusCode)
	}
}

func TestOAuthServer_InvalidGrant(t *testing.T) {
	server := NewOAuthServer(OAuthServerConfig{
		SimulateErrors: &OAuthErrorSimulation{
			InvalidGrant: true,
		},
	})

	ctx := context.Background()
	_, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start OAuth server: %v", err)
	}
	defer server.Stop(ctx)

	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := server.WaitForReady(readyCtx); err != nil {
		t.Fatalf("Server not ready: %v", err)
	}

	// Try to exchange any code - should fail with invalid_grant
	tokenURL := server.GetTokenURL()
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", "any-code")

	resp, err := http.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		t.Fatalf("Failed to make token request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400 for simulated invalid_grant, got %d", resp.StatusCode)
	}

	var errResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}

	if errResp["error"] != "invalid_grant" {
		t.Errorf("Expected error 'invalid_grant', got '%s'", errResp["error"])
	}
}

func TestOAuthServer_TokenExpiry(t *testing.T) {
	// Use a very short token lifetime for testing
	server := NewOAuthServer(OAuthServerConfig{
		TokenLifetime: 100 * time.Millisecond,
	})

	ctx := context.Background()
	_, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start OAuth server: %v", err)
	}
	defer server.Stop(ctx)

	// Generate a token
	code := server.GenerateAuthCode("test-client", "http://localhost/callback", "openid", "state", "", "")
	tokenResp, err := server.SimulateCallback(code)
	if err != nil {
		t.Fatalf("Failed to get token: %v", err)
	}

	// Token should be valid initially
	if !server.ValidateToken(tokenResp.AccessToken) {
		t.Error("Expected token to be valid initially")
	}

	// Wait for expiry
	time.Sleep(150 * time.Millisecond)

	// Token should now be invalid
	if server.ValidateToken(tokenResp.AccessToken) {
		t.Error("Expected token to be expired")
	}
}

func TestOAuthServer_WWWAuthenticateHeader(t *testing.T) {
	server := NewOAuthServer(OAuthServerConfig{
		Issuer: "https://auth.example.com",
	})

	header := server.WWWAuthenticateHeader()

	if !strings.Contains(header, "Bearer") {
		t.Error("Expected WWW-Authenticate header to contain 'Bearer'")
	}

	if !strings.Contains(header, "https://auth.example.com") {
		t.Error("Expected WWW-Authenticate header to contain issuer URL")
	}
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{"Valid Bearer token", "Bearer abc123", "abc123"},
		{"Empty header", "", ""},
		{"No Bearer prefix", "abc123", ""},
		{"Bearer with spaces", "Bearer  token with spaces", " token with spaces"},
		{"Basic auth", "Basic abc123", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractBearerToken(tt.header)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestOAuthServer_TokenExpiryWithMockClock(t *testing.T) {
	// Use mock clock for instant token expiry testing (no sleeps needed)
	mockClock := NewMockClock(time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC))

	server := NewOAuthServer(OAuthServerConfig{
		TokenLifetime: 1 * time.Hour,
		Clock:         mockClock,
	})

	ctx := context.Background()
	_, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start OAuth server: %v", err)
	}
	defer server.Stop(ctx)

	// Generate a token
	code := server.GenerateAuthCode("test-client", "http://localhost/callback", "openid", "state", "", "")
	tokenResp, err := server.SimulateCallback(code)
	if err != nil {
		t.Fatalf("Failed to get token: %v", err)
	}

	// Token should be valid initially
	if !server.ValidateToken(tokenResp.AccessToken) {
		t.Error("Expected token to be valid initially")
	}

	// Advance clock by 30 minutes - token should still be valid
	mockClock.Advance(30 * time.Minute)
	if !server.ValidateToken(tokenResp.AccessToken) {
		t.Error("Expected token to still be valid after 30 minutes")
	}

	// Advance clock by 31 more minutes - token should now be expired
	mockClock.Advance(31 * time.Minute)
	if server.ValidateToken(tokenResp.AccessToken) {
		t.Error("Expected token to be expired after 61 minutes")
	}
}

func TestOAuthServer_SetClock(t *testing.T) {
	server := NewOAuthServer(OAuthServerConfig{
		TokenLifetime: 1 * time.Hour,
	})

	// Verify default clock is RealClock
	_, isReal := server.GetClock().(RealClock)
	if !isReal {
		t.Error("Expected default clock to be RealClock")
	}

	// Set a mock clock
	mockClock := NewMockClock(time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC))
	server.SetClock(mockClock)

	if server.GetClock() != mockClock {
		t.Error("Expected clock to be the mock clock we set")
	}
}
