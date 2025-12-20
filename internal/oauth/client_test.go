package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_GetRedirectURI(t *testing.T) {
	tests := []struct {
		name         string
		publicURL    string
		callbackPath string
		expected     string
	}{
		{
			name:         "simple URL",
			publicURL:    "https://muster.example.com",
			callbackPath: "/oauth/callback",
			expected:     "https://muster.example.com/oauth/callback",
		},
		{
			name:         "URL with trailing slash",
			publicURL:    "https://muster.example.com/",
			callbackPath: "/oauth/callback",
			expected:     "https://muster.example.com/oauth/callback",
		},
		{
			name:         "localhost",
			publicURL:    "http://localhost:8090",
			callbackPath: "/oauth/callback",
			expected:     "http://localhost:8090/oauth/callback",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient("client-id", tc.publicURL, tc.callbackPath)
			defer client.Stop()

			result := client.GetRedirectURI()
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestClient_TokenStoreAndStateStore(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/callback")
	defer client.Stop()

	// Verify stores are initialized
	if client.GetTokenStore() == nil {
		t.Error("TokenStore should not be nil")
	}

	if client.GetStateStore() == nil {
		t.Error("StateStore should not be nil")
	}
}

func TestClient_GetToken(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/callback")
	defer client.Stop()

	sessionID := "session-123"
	issuer := "https://auth.example.com"
	scope := "openid profile"

	// Initially no token
	token := client.GetToken(sessionID, issuer, scope)
	if token != nil {
		t.Error("Expected nil token initially")
	}

	// Store a token
	testToken := &Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       scope,
		Issuer:      issuer,
	}
	client.StoreToken(sessionID, testToken)

	// Now should be retrievable
	token = client.GetToken(sessionID, issuer, scope)
	if token == nil {
		t.Fatal("Expected token after storing")
	}

	if token.AccessToken != testToken.AccessToken {
		t.Errorf("Expected access token %q, got %q", testToken.AccessToken, token.AccessToken)
	}
}

func TestClient_FetchMetadata(t *testing.T) {
	// Create a test server that returns OAuth metadata
	metadata := OAuthMetadata{
		Issuer:                "https://auth.example.com",
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		TokenEndpoint:         "https://auth.example.com/token",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(metadata)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient("client-id", "https://muster.example.com", "/oauth/callback")
	defer client.Stop()

	// Fetch metadata
	ctx := context.Background()
	result, err := client.fetchMetadata(ctx, server.URL)
	if err != nil {
		t.Fatalf("Failed to fetch metadata: %v", err)
	}

	if result.AuthorizationEndpoint != metadata.AuthorizationEndpoint {
		t.Errorf("Expected authorization endpoint %q, got %q",
			metadata.AuthorizationEndpoint, result.AuthorizationEndpoint)
	}

	if result.TokenEndpoint != metadata.TokenEndpoint {
		t.Errorf("Expected token endpoint %q, got %q",
			metadata.TokenEndpoint, result.TokenEndpoint)
	}

	// Second call should hit cache
	result2, err := client.fetchMetadata(ctx, server.URL)
	if err != nil {
		t.Fatalf("Failed to fetch metadata from cache: %v", err)
	}

	if result2.AuthorizationEndpoint != metadata.AuthorizationEndpoint {
		t.Errorf("Cached result should match: expected %q, got %q",
			metadata.AuthorizationEndpoint, result2.AuthorizationEndpoint)
	}
}

func TestGeneratePKCE(t *testing.T) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		t.Fatalf("Failed to generate PKCE: %v", err)
	}

	if verifier == "" {
		t.Error("Verifier should not be empty")
	}

	if challenge == "" {
		t.Error("Challenge should not be empty")
	}

	// Verifier and challenge should be different
	if verifier == challenge {
		t.Error("Verifier and challenge should be different")
	}

	// Generate another pair to ensure randomness
	verifier2, challenge2, err := generatePKCE()
	if err != nil {
		t.Fatalf("Failed to generate second PKCE: %v", err)
	}

	if verifier == verifier2 {
		t.Error("Generated verifiers should be unique")
	}

	if challenge == challenge2 {
		t.Error("Generated challenges should be unique")
	}
}
