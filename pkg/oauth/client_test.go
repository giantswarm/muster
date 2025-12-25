package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	t.Run("creates client with defaults", func(t *testing.T) {
		c := NewClient()
		if c.httpClient == nil {
			t.Error("expected httpClient to be set")
		}
		if c.logger == nil {
			t.Error("expected logger to be set")
		}
		if c.metadataCache == nil {
			t.Error("expected metadataCache to be initialized")
		}
		if c.metadataTTL != DefaultMetadataCacheTTL {
			t.Errorf("expected metadataTTL to be %v, got %v", DefaultMetadataCacheTTL, c.metadataTTL)
		}
	})

	t.Run("applies options", func(t *testing.T) {
		customHTTP := &http.Client{Timeout: 10 * time.Second}
		customTTL := 5 * time.Minute

		c := NewClient(
			WithHTTPClient(customHTTP),
			WithMetadataCacheTTL(customTTL),
		)

		if c.httpClient != customHTTP {
			t.Error("expected custom httpClient to be set")
		}
		if c.metadataTTL != customTTL {
			t.Errorf("expected metadataTTL to be %v, got %v", customTTL, c.metadataTTL)
		}
	})
}

func TestDiscoverMetadata(t *testing.T) {
	t.Run("discovers via RFC 8414 endpoint", func(t *testing.T) {
		metadata := &Metadata{
			Issuer:                "https://issuer.example.com",
			AuthorizationEndpoint: "https://issuer.example.com/authorize",
			TokenEndpoint:         "https://issuer.example.com/token",
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

		c := NewClient(WithHTTPClient(server.Client()))
		result, err := c.DiscoverMetadata(context.Background(), server.URL)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Issuer != metadata.Issuer {
			t.Errorf("expected issuer %s, got %s", metadata.Issuer, result.Issuer)
		}
		if result.AuthorizationEndpoint != metadata.AuthorizationEndpoint {
			t.Errorf("expected auth endpoint %s, got %s", metadata.AuthorizationEndpoint, result.AuthorizationEndpoint)
		}
	})

	t.Run("falls back to OIDC endpoint", func(t *testing.T) {
		metadata := &Metadata{
			Issuer:                "https://issuer.example.com",
			AuthorizationEndpoint: "https://issuer.example.com/authorize",
			TokenEndpoint:         "https://issuer.example.com/token",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/openid-configuration" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(metadata)
				return
			}
			// RFC 8414 endpoint returns 404
			http.NotFound(w, r)
		}))
		defer server.Close()

		c := NewClient(WithHTTPClient(server.Client()))
		result, err := c.DiscoverMetadata(context.Background(), server.URL)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Issuer != metadata.Issuer {
			t.Errorf("expected issuer %s, got %s", metadata.Issuer, result.Issuer)
		}
	})

	t.Run("returns error when both endpoints fail", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		defer server.Close()

		c := NewClient(WithHTTPClient(server.Client()))
		_, err := c.DiscoverMetadata(context.Background(), server.URL)

		if err == nil {
			t.Error("expected error when discovery fails")
		}
	})

	t.Run("caches metadata", func(t *testing.T) {
		var callCount int32
		metadata := &Metadata{
			Issuer:                "https://issuer.example.com",
			AuthorizationEndpoint: "https://issuer.example.com/authorize",
			TokenEndpoint:         "https://issuer.example.com/token",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&callCount, 1)
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(metadata)
				return
			}
			http.NotFound(w, r)
		}))
		defer server.Close()

		c := NewClient(WithHTTPClient(server.Client()))

		// First call should hit the server
		_, err := c.DiscoverMetadata(context.Background(), server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Second call should use cache
		_, err = c.DiscoverMetadata(context.Background(), server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if atomic.LoadInt32(&callCount) != 1 {
			t.Errorf("expected 1 server call (cached), got %d", callCount)
		}
	})

	t.Run("deduplicates concurrent requests", func(t *testing.T) {
		var callCount int32
		metadata := &Metadata{
			Issuer:                "https://issuer.example.com",
			AuthorizationEndpoint: "https://issuer.example.com/authorize",
			TokenEndpoint:         "https://issuer.example.com/token",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Add a small delay to ensure concurrent requests overlap
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt32(&callCount, 1)
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(metadata)
				return
			}
			http.NotFound(w, r)
		}))
		defer server.Close()

		c := NewClient(WithHTTPClient(server.Client()))

		// Make concurrent requests
		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = c.DiscoverMetadata(context.Background(), server.URL)
			}()
		}
		wg.Wait()

		// With singleflight, only 1 request should be made
		if atomic.LoadInt32(&callCount) != 1 {
			t.Errorf("expected 1 server call (singleflight), got %d", callCount)
		}
	})

	t.Run("strips trailing slash from issuer", func(t *testing.T) {
		metadata := &Metadata{
			Issuer:                "https://issuer.example.com",
			AuthorizationEndpoint: "https://issuer.example.com/authorize",
			TokenEndpoint:         "https://issuer.example.com/token",
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

		c := NewClient(WithHTTPClient(server.Client()))
		// Pass URL with trailing slash
		_, err := c.DiscoverMetadata(context.Background(), server.URL+"/")

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestExchangeCode(t *testing.T) {
	t.Run("exchanges code for token", func(t *testing.T) {
		expectedToken := &Token{
			AccessToken:  "access-token-123",
			RefreshToken: "refresh-token-456",
			ExpiresIn:    3600,
			TokenType:    "Bearer",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.URL.Path != "/token" {
				t.Errorf("expected /token path, got %s", r.URL.Path)
			}

			err := r.ParseForm()
			if err != nil {
				t.Fatalf("failed to parse form: %v", err)
			}

			if r.Form.Get("grant_type") != "authorization_code" {
				t.Errorf("expected grant_type authorization_code, got %s", r.Form.Get("grant_type"))
			}
			if r.Form.Get("code") != "auth-code" {
				t.Errorf("expected code auth-code, got %s", r.Form.Get("code"))
			}
			if r.Form.Get("redirect_uri") != "http://localhost:8080/callback" {
				t.Errorf("expected redirect_uri, got %s", r.Form.Get("redirect_uri"))
			}
			if r.Form.Get("client_id") != "test-client" {
				t.Errorf("expected client_id test-client, got %s", r.Form.Get("client_id"))
			}
			if r.Form.Get("code_verifier") != "verifier123" {
				t.Errorf("expected code_verifier verifier123, got %s", r.Form.Get("code_verifier"))
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(expectedToken)
		}))
		defer server.Close()

		c := NewClient(WithHTTPClient(server.Client()))
		token, err := c.ExchangeCode(
			context.Background(),
			server.URL+"/token",
			"auth-code",
			"http://localhost:8080/callback",
			"test-client",
			"verifier123",
		)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token.AccessToken != expectedToken.AccessToken {
			t.Errorf("expected access token %s, got %s", expectedToken.AccessToken, token.AccessToken)
		}
		if token.RefreshToken != expectedToken.RefreshToken {
			t.Errorf("expected refresh token %s, got %s", expectedToken.RefreshToken, token.RefreshToken)
		}
		if token.ExpiresAt.IsZero() {
			t.Error("expected ExpiresAt to be calculated from ExpiresIn")
		}
	})

	t.Run("returns error on failed request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": "invalid_grant"}`))
		}))
		defer server.Close()

		c := NewClient(WithHTTPClient(server.Client()))
		_, err := c.ExchangeCode(
			context.Background(),
			server.URL+"/token",
			"invalid-code",
			"http://localhost:8080/callback",
			"test-client",
			"verifier123",
		)

		if err == nil {
			t.Error("expected error for failed request")
		}
	})
}

func TestRefreshToken(t *testing.T) {
	t.Run("refreshes token", func(t *testing.T) {
		expectedToken := &Token{
			AccessToken:  "new-access-token",
			RefreshToken: "new-refresh-token",
			ExpiresIn:    3600,
			TokenType:    "Bearer",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := r.ParseForm()
			if err != nil {
				t.Fatalf("failed to parse form: %v", err)
			}

			if r.Form.Get("grant_type") != "refresh_token" {
				t.Errorf("expected grant_type refresh_token, got %s", r.Form.Get("grant_type"))
			}
			if r.Form.Get("refresh_token") != "old-refresh-token" {
				t.Errorf("expected refresh_token, got %s", r.Form.Get("refresh_token"))
			}
			if r.Form.Get("client_id") != "test-client" {
				t.Errorf("expected client_id test-client, got %s", r.Form.Get("client_id"))
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(expectedToken)
		}))
		defer server.Close()

		c := NewClient(WithHTTPClient(server.Client()))
		token, err := c.RefreshToken(
			context.Background(),
			server.URL+"/token",
			"old-refresh-token",
			"test-client",
		)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token.AccessToken != expectedToken.AccessToken {
			t.Errorf("expected access token %s, got %s", expectedToken.AccessToken, token.AccessToken)
		}
	})

	t.Run("returns error on failed refresh", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "invalid_grant"}`))
		}))
		defer server.Close()

		c := NewClient(WithHTTPClient(server.Client()))
		_, err := c.RefreshToken(
			context.Background(),
			server.URL+"/token",
			"expired-refresh-token",
			"test-client",
		)

		if err == nil {
			t.Error("expected error for failed refresh")
		}
	})
}

func TestBuildAuthorizationURL(t *testing.T) {
	c := NewClient()

	t.Run("builds URL with all parameters", func(t *testing.T) {
		pkce := &PKCEChallenge{
			CodeChallenge:       "challenge123",
			CodeChallengeMethod: "S256",
		}

		url, err := c.BuildAuthorizationURL(
			"https://auth.example.com/authorize",
			"test-client",
			"http://localhost:8080/callback",
			"state123",
			"openid profile email",
			pkce,
		)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify URL contains expected parameters
		expectedParams := []string{
			"response_type=code",
			"client_id=test-client",
			"redirect_uri=http%3A%2F%2Flocalhost%3A8080%2Fcallback",
			"state=state123",
			"scope=openid+profile+email",
			"code_challenge=challenge123",
			"code_challenge_method=S256",
		}

		for _, param := range expectedParams {
			if !contains(url, param) {
				t.Errorf("expected URL to contain %s, got %s", param, url)
			}
		}
	})

	t.Run("builds URL without PKCE", func(t *testing.T) {
		url, err := c.BuildAuthorizationURL(
			"https://auth.example.com/authorize",
			"test-client",
			"http://localhost:8080/callback",
			"state123",
			"openid",
			nil, // no PKCE
		)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should not contain PKCE parameters
		if contains(url, "code_challenge") {
			t.Errorf("expected URL to not contain code_challenge, got %s", url)
		}
	})

	t.Run("builds URL without scope", func(t *testing.T) {
		url, err := c.BuildAuthorizationURL(
			"https://auth.example.com/authorize",
			"test-client",
			"http://localhost:8080/callback",
			"state123",
			"", // no scope
			nil,
		)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should not contain scope parameter
		if contains(url, "scope=") {
			t.Errorf("expected URL to not contain scope, got %s", url)
		}
	})

	t.Run("returns error for invalid URL", func(t *testing.T) {
		_, err := c.BuildAuthorizationURL(
			"://invalid-url",
			"test-client",
			"http://localhost:8080/callback",
			"state123",
			"openid",
			nil,
		)

		if err == nil {
			t.Error("expected error for invalid URL")
		}
	})
}

func TestClearMetadataCache(t *testing.T) {
	metadata := &Metadata{
		Issuer:                "https://issuer.example.com",
		AuthorizationEndpoint: "https://issuer.example.com/authorize",
		TokenEndpoint:         "https://issuer.example.com/token",
	}

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(metadata)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	c := NewClient(WithHTTPClient(server.Client()))

	// First call
	_, err := c.DiscoverMetadata(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call (should be cached)
	_, err = c.DiscoverMetadata(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 call before cache clear, got %d", callCount)
	}

	// Clear cache
	c.ClearMetadataCache()

	// Third call (cache cleared, should hit server)
	_, err = c.DiscoverMetadata(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("expected 2 calls after cache clear, got %d", callCount)
	}
}

func TestMetadataCacheExpiry(t *testing.T) {
	metadata := &Metadata{
		Issuer:                "https://issuer.example.com",
		AuthorizationEndpoint: "https://issuer.example.com/authorize",
		TokenEndpoint:         "https://issuer.example.com/token",
	}

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(metadata)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	// Use very short TTL for testing
	c := NewClient(
		WithHTTPClient(server.Client()),
		WithMetadataCacheTTL(50*time.Millisecond),
	)

	// First call
	_, err := c.DiscoverMetadata(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait for cache to expire
	time.Sleep(100 * time.Millisecond)

	// Second call (cache expired)
	_, err = c.DiscoverMetadata(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("expected 2 calls after cache expiry, got %d", callCount)
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
