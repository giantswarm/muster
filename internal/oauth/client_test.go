package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
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
			callbackPath: "/oauth/proxy/callback",
			expected:     "https://muster.example.com/oauth/proxy/callback",
		},
		{
			name:         "URL with trailing slash",
			publicURL:    "https://muster.example.com/",
			callbackPath: "/oauth/proxy/callback",
			expected:     "https://muster.example.com/oauth/proxy/callback",
		},
		{
			name:         "localhost",
			publicURL:    "http://localhost:8090",
			callbackPath: "/oauth/proxy/callback",
			expected:     "http://localhost:8090/oauth/proxy/callback",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient("client-id", tc.publicURL, tc.callbackPath, "openid profile email")
			defer client.Stop()

			result := client.GetRedirectURI()
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestClient_TokenStoreAndStateStore(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
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
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
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
	testToken := &pkgoauth.Token{
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

func TestClient_GetToken_SSO_FallbackToIssuer(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	sessionID := "session-123"
	issuer := "https://auth.example.com"
	scope1 := "openid profile"
	scope2 := "openid email" // Different scope

	// Store a token with scope1
	testToken := &pkgoauth.Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       scope1,
		Issuer:      issuer,
	}
	client.StoreToken(sessionID, testToken)

	// Request with scope2 should still find the token via SSO fallback
	token := client.GetToken(sessionID, issuer, scope2)
	if token == nil {
		t.Fatal("Expected token via SSO fallback")
	}

	if token.AccessToken != testToken.AccessToken {
		t.Errorf("Expected access token %q, got %q", testToken.AccessToken, token.AccessToken)
	}
}

func TestClient_DiscoverMetadata(t *testing.T) {
	// Create a test server that returns OAuth metadata
	metadata := pkgoauth.Metadata{
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

	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	// Fetch metadata
	ctx := context.Background()
	result, err := client.DiscoverMetadata(ctx, server.URL)
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
	result2, err := client.DiscoverMetadata(ctx, server.URL)
	if err != nil {
		t.Fatalf("Failed to fetch metadata from cache: %v", err)
	}

	if result2.AuthorizationEndpoint != metadata.AuthorizationEndpoint {
		t.Errorf("Cached result should match: expected %q, got %q",
			metadata.AuthorizationEndpoint, result2.AuthorizationEndpoint)
	}
}

func TestClient_DiscoverMetadata_OpenIDFallback(t *testing.T) {
	// Create a test server that only supports OpenID Connect discovery
	metadata := pkgoauth.Metadata{
		Issuer:                "https://auth.example.com",
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		TokenEndpoint:         "https://auth.example.com/token",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(metadata)
			return
		}
		// Return 404 for oauth-authorization-server
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	ctx := context.Background()
	result, err := client.DiscoverMetadata(ctx, server.URL)
	if err != nil {
		t.Fatalf("Failed to fetch metadata via OpenID fallback: %v", err)
	}

	if result.AuthorizationEndpoint != metadata.AuthorizationEndpoint {
		t.Errorf("Expected authorization endpoint %q, got %q",
			metadata.AuthorizationEndpoint, result.AuthorizationEndpoint)
	}
}

func TestClient_DiscoverMetadata_Error(t *testing.T) {
	// Create a test server that returns errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Server Error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	ctx := context.Background()
	_, err := client.DiscoverMetadata(ctx, server.URL)
	if err == nil {
		t.Fatal("Expected error for failed metadata fetch")
	}
}

func TestClient_GenerateAuthURL(t *testing.T) {
	// Create a test server that returns OAuth metadata
	metadata := pkgoauth.Metadata{
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

	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	ctx := context.Background()
	authURL, err := client.GenerateAuthURL(ctx, "session-123", "mcp-kubernetes", server.URL, "openid profile")
	if err != nil {
		t.Fatalf("Failed to generate auth URL: %v", err)
	}

	// Verify URL contains expected parameters
	if authURL == "" {
		t.Error("Expected non-empty auth URL")
	}

	// Check for PKCE parameters
	if !strings.Contains(authURL, "code_challenge=") {
		t.Error("Auth URL should contain code_challenge")
	}
	if !strings.Contains(authURL, "code_challenge_method=S256") {
		t.Error("Auth URL should contain code_challenge_method=S256")
	}
	if !strings.Contains(authURL, "response_type=code") {
		t.Error("Auth URL should contain response_type=code")
	}
	if !strings.Contains(authURL, "client_id=") {
		t.Error("Auth URL should contain client_id")
	}
	if !strings.Contains(authURL, "redirect_uri=") {
		t.Error("Auth URL should contain redirect_uri")
	}
	if !strings.Contains(authURL, "state=") {
		t.Error("Auth URL should contain state")
	}
	if !strings.Contains(authURL, "scope=") {
		t.Error("Auth URL should contain scope")
	}
}

func TestClient_ExchangeCode(t *testing.T) {
	tokenResponse := map[string]interface{}{
		"access_token": "new-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"scope":        "openid profile",
	}

	// Use a mux to handle multiple paths without closure issues
	mux := http.NewServeMux()

	var serverURL string
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		metadata := pkgoauth.Metadata{
			Issuer:                "https://auth.example.com",
			AuthorizationEndpoint: "https://auth.example.com/authorize",
			TokenEndpoint:         serverURL + "/token",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metadata)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Verify request parameters
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		if r.FormValue("grant_type") != "authorization_code" {
			http.Error(w, "Invalid grant_type", http.StatusBadRequest)
			return
		}
		if r.FormValue("code") == "" {
			http.Error(w, "Missing code", http.StatusBadRequest)
			return
		}
		if r.FormValue("code_verifier") == "" {
			http.Error(w, "Missing code_verifier", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse)
	})

	server := httptest.NewServer(mux)
	serverURL = server.URL
	defer server.Close()

	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	ctx := context.Background()
	token, err := client.ExchangeCode(ctx, "auth-code", "code-verifier", server.URL)
	if err != nil {
		t.Fatalf("Failed to exchange code: %v", err)
	}

	if token.AccessToken != "new-access-token" {
		t.Errorf("Expected access token %q, got %q", "new-access-token", token.AccessToken)
	}
	if token.TokenType != "Bearer" {
		t.Errorf("Expected token type %q, got %q", "Bearer", token.TokenType)
	}
	if token.ExpiresIn != 3600 {
		t.Errorf("Expected expires_in %d, got %d", 3600, token.ExpiresIn)
	}
	if token.Issuer != server.URL {
		t.Errorf("Expected issuer %q, got %q", server.URL, token.Issuer)
	}
	if token.ExpiresAt.IsZero() {
		t.Error("Expected ExpiresAt to be set")
	}
}

func TestClient_ExchangeCode_Error(t *testing.T) {
	mux := http.NewServeMux()

	var serverURL string
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		metadata := pkgoauth.Metadata{
			Issuer:                "https://auth.example.com",
			AuthorizationEndpoint: "https://auth.example.com/authorize",
			TokenEndpoint:         serverURL + "/token",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metadata)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Invalid code", http.StatusBadRequest)
	})

	server := httptest.NewServer(mux)
	serverURL = server.URL
	defer server.Close()

	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	defer client.Stop()

	ctx := context.Background()
	_, err := client.ExchangeCode(ctx, "invalid-code", "code-verifier", server.URL)
	if err == nil {
		t.Fatal("Expected error for invalid code exchange")
	}
}

func TestGeneratePKCE(t *testing.T) {
	// Use the shared PKCE implementation (delegates to golang.org/x/oauth2)
	verifier, challenge := pkgoauth.GeneratePKCERaw()

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
	verifier2, challenge2 := pkgoauth.GeneratePKCERaw()

	if verifier == verifier2 {
		t.Error("Generated verifiers should be unique")
	}

	if challenge == challenge2 {
		t.Error("Generated challenges should be unique")
	}
}

func TestToken_IsExpired_Client(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		margin    time.Duration
		expected  bool
	}{
		{
			name:      "zero expiration never expires",
			expiresAt: time.Time{},
			margin:    time.Minute,
			expected:  false,
		},
		{
			name:      "future token not expired",
			expiresAt: time.Now().Add(time.Hour),
			margin:    time.Minute,
			expected:  false,
		},
		{
			name:      "past token is expired",
			expiresAt: time.Now().Add(-time.Hour),
			margin:    time.Minute,
			expected:  true,
		},
		{
			name:      "token within margin is expired",
			expiresAt: time.Now().Add(30 * time.Second),
			margin:    time.Minute,
			expected:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token := &pkgoauth.Token{ExpiresAt: tc.expiresAt}
			result := token.IsExpiredWithMargin(tc.margin)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, result)
			}
		})
	}
}
