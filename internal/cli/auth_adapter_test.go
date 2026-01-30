package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"muster/internal/agent/oauth"
)

func TestNewAuthAdapter(t *testing.T) {
	t.Run("creates adapter with default configuration", func(t *testing.T) {
		adapter, err := NewAuthAdapter()
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		if adapter == nil {
			t.Fatal("expected non-nil adapter")
		}
		if adapter.managers == nil {
			t.Error("expected managers map to be initialized")
		}
		if adapter.tokenStorageDir == "" {
			t.Error("expected tokenStorageDir to be set")
		}
	})
}

func TestAuthAdapter_HasValidToken(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer adapter.Close()

	// Without any tokens, should return false
	if adapter.HasValidToken("https://example.com") {
		t.Error("expected HasValidToken to return false when no token exists")
	}
}

func TestAuthAdapter_GetBearerToken_NotAuthenticated(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer adapter.Close()

	_, err = adapter.GetBearerToken("https://example.com")
	if err == nil {
		t.Error("expected error when getting token without authentication")
	}

	// Should return AuthRequiredError
	var authErr *AuthRequiredError
	if ok := isAuthRequiredError(err); !ok {
		t.Errorf("expected AuthRequiredError, got %T: %v", err, authErr)
	}
}

func TestAuthAdapter_Logout(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer adapter.Close()

	// Logout should work even when no token exists
	err = adapter.Logout("https://example.com")
	if err != nil {
		t.Errorf("unexpected error on logout: %v", err)
	}
}

func TestAuthAdapter_LogoutAll(t *testing.T) {
	// Create a temp directory for tokens
	tmpDir := t.TempDir()

	adapter := &AuthAdapter{
		managers:        make(map[string]*oauth.AuthManager),
		tokenStorageDir: tmpDir,
	}
	defer adapter.Close()

	// LogoutAll should work even when no managers exist
	err := adapter.LogoutAll()
	if err != nil {
		t.Errorf("unexpected error on logoutAll: %v", err)
	}
}

func TestAuthAdapter_GetStatus_Empty(t *testing.T) {
	// Create a temp directory for tokens
	tmpDir := t.TempDir()

	adapter := &AuthAdapter{
		managers:        make(map[string]*oauth.AuthManager),
		tokenStorageDir: tmpDir,
	}
	defer adapter.Close()

	statuses := adapter.GetStatus()
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses, got %d", len(statuses))
	}
}

func TestAuthAdapter_GetStatusForEndpoint_Unknown(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer adapter.Close()

	status := adapter.GetStatusForEndpoint("https://unknown.example.com")
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if status.Endpoint != "https://unknown.example.com" {
		t.Errorf("expected endpoint to match, got %s", status.Endpoint)
	}
}

func TestAuthAdapter_Close(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	err = adapter.Close()
	if err != nil {
		t.Errorf("unexpected error on close: %v", err)
	}

	// Managers should be cleared
	if len(adapter.managers) != 0 {
		t.Error("expected managers to be cleared after close")
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://example.com/mcp", "https://example.com"},
		{"https://example.com/sse", "https://example.com"},
		{"https://example.com/", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"https://example.com/mcp/", "https://example.com"},
		{"http://localhost:8080/mcp", "http://localhost:8080"},
		{"http://localhost:8080/sse", "http://localhost:8080"},
		{"http://localhost:8090", "http://localhost:8090"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeEndpoint(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeEndpoint(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestListTokenFiles(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		adapter := &AuthAdapter{
			tokenStorageDir: tmpDir,
		}

		tokens, err := adapter.listTokenFiles()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(tokens) != 0 {
			t.Errorf("expected 0 tokens, got %d", len(tokens))
		}
	})

	t.Run("non-existent directory", func(t *testing.T) {
		adapter := &AuthAdapter{
			tokenStorageDir: "/nonexistent/directory/path",
		}

		tokens, err := adapter.listTokenFiles()
		if err != nil {
			t.Errorf("unexpected error for non-existent dir: %v", err)
		}
		if len(tokens) != 0 {
			t.Errorf("expected 0 tokens, got %d", len(tokens))
		}
	})

	t.Run("directory with non-json files", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create a non-json file
		err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("test"), 0644)
		if err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		adapter := &AuthAdapter{
			tokenStorageDir: tmpDir,
		}

		tokens, err := adapter.listTokenFiles()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(tokens) != 0 {
			t.Errorf("expected 0 tokens (non-json files ignored), got %d", len(tokens))
		}
	})
}

func TestReadTokenFile(t *testing.T) {
	t.Run("non-existent file", func(t *testing.T) {
		_, err := readTokenFile("/nonexistent/path.json")
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "invalid.json")
		err := os.WriteFile(tmpFile, []byte("not valid json"), 0644)
		if err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		_, err = readTokenFile(tmpFile)
		if err == nil {
			t.Error("expected error for invalid json")
		}
	})
}

// isAuthRequiredError checks if an error is an AuthRequiredError using errors.As.
func isAuthRequiredError(err error) bool {
	var authErr *AuthRequiredError
	return errors.As(err, &authErr)
}

func TestAuthAdapterConfig_NoSilentRefresh(t *testing.T) {
	t.Run("creates adapter with NoSilentRefresh enabled", func(t *testing.T) {
		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
			NoSilentRefresh: true,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		if !adapter.noSilentRefresh {
			t.Error("expected noSilentRefresh to be true")
		}
	})

	t.Run("creates adapter with NoSilentRefresh disabled by default", func(t *testing.T) {
		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		if adapter.noSilentRefresh {
			t.Error("expected noSilentRefresh to be false by default")
		}
	})
}

func TestAuthAdapter_SetNoSilentRefresh(t *testing.T) {
	tmpDir := t.TempDir()
	adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
		TokenStorageDir: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer adapter.Close()

	// Initially false
	if adapter.noSilentRefresh {
		t.Error("expected noSilentRefresh to be false initially")
	}

	// Set to true
	adapter.SetNoSilentRefresh(true)
	if !adapter.noSilentRefresh {
		t.Error("expected noSilentRefresh to be true after setting")
	}

	// Set back to false
	adapter.SetNoSilentRefresh(false)
	if adapter.noSilentRefresh {
		t.Error("expected noSilentRefresh to be false after unsetting")
	}
}

func TestDoTokenRefresh_PreservesIDToken(t *testing.T) {
	// This test verifies that when a refresh response doesn't include an ID token,
	// the original ID token is preserved for SSO forwarding purposes.

	t.Run("preserves ID token when refresh response omits it", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create a mock OAuth server
		var tokenEndpointCalls int
		var serverURL string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/oauth-authorization-server":
				// Return OAuth metadata with the actual server URL
				metadata := map[string]interface{}{
					"issuer":                 serverURL,
					"token_endpoint":         serverURL + "/oauth/token",
					"authorization_endpoint": serverURL + "/oauth/authorize",
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(metadata)

			case "/oauth/token":
				tokenEndpointCalls++
				// Return a refresh response WITHOUT an ID token
				// (per OAuth 2.0 spec, refresh responses typically don't include ID tokens)
				response := map[string]interface{}{
					"access_token":  "new-access-token-12345",
					"token_type":    "Bearer",
					"expires_in":    3600,
					"refresh_token": "new-refresh-token-67890",
					// Note: no id_token in response
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)

			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()
		serverURL = server.URL

		// Create token store
		store, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
			StorageDir: tmpDir,
			FileMode:   true,
		})
		if err != nil {
			t.Fatalf("failed to create token store: %v", err)
		}

		// Store a token WITH an ID token
		originalIDToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyMTIzIiwiZW1haWwiOiJ1c2VyQGV4YW1wbGUuY29tIiwiZXhwIjoxNzA5MjQ1MjAwfQ.signature"
		normalizedEndpoint := "https://muster.example.com"

		// Create a stored token with an ID token
		storedToken := &oauth.StoredToken{
			AccessToken:  "old-access-token",
			RefreshToken: "old-refresh-token",
			TokenType:    "Bearer",
			IDToken:      originalIDToken,
			ServerURL:    normalizedEndpoint,
			IssuerURL:    server.URL,
		}

		// Store the token using oauth2.Token conversion (simulating how it would be stored)
		oauth2Token := storedToken.ToOAuth2Token()
		if err := store.StoreToken(normalizedEndpoint, server.URL, oauth2Token); err != nil {
			t.Fatalf("failed to store token: %v", err)
		}

		// Verify the stored token has the ID token
		retrievedBefore := store.GetTokenIncludingExpiring(normalizedEndpoint)
		if retrievedBefore == nil {
			t.Fatal("expected to retrieve stored token")
		}
		if retrievedBefore.IDToken != originalIDToken {
			t.Errorf("stored token should have ID token, got: %q", retrievedBefore.IDToken)
		}

		// Create adapter and perform refresh
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		// Call doTokenRefresh directly
		ctx := context.Background()
		err = adapter.doTokenRefresh(ctx, store, retrievedBefore, normalizedEndpoint)
		if err != nil {
			t.Fatalf("doTokenRefresh failed: %v", err)
		}

		// Verify token endpoint was called
		if tokenEndpointCalls != 1 {
			t.Errorf("expected 1 token endpoint call, got %d", tokenEndpointCalls)
		}

		// Retrieve the refreshed token and verify ID token is preserved
		refreshedToken := store.GetTokenIncludingExpiring(normalizedEndpoint)
		if refreshedToken == nil {
			t.Fatal("expected to retrieve refreshed token")
		}

		// The access token should be updated
		if refreshedToken.AccessToken != "new-access-token-12345" {
			t.Errorf("access token should be updated, got: %q", refreshedToken.AccessToken)
		}

		// The ID token should be preserved (this is what we're testing)
		if refreshedToken.IDToken != originalIDToken {
			t.Errorf("ID token should be preserved after refresh.\nExpected: %q\nGot: %q",
				originalIDToken, refreshedToken.IDToken)
		}

		// The refresh token should also be updated
		if refreshedToken.RefreshToken != "new-refresh-token-67890" {
			t.Errorf("refresh token should be updated, got: %q", refreshedToken.RefreshToken)
		}
	})

	t.Run("uses new ID token when refresh response includes one", func(t *testing.T) {
		tmpDir := t.TempDir()

		newIDToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyMTIzIiwiZW1haWwiOiJ1c2VyQGV4YW1wbGUuY29tIiwiZXhwIjoxNzA5MzMxNjAwfQ.newsignature"

		// Create a mock OAuth server that DOES return an ID token
		var serverURL string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/oauth-authorization-server":
				metadata := map[string]interface{}{
					"issuer":                 serverURL,
					"token_endpoint":         serverURL + "/oauth/token",
					"authorization_endpoint": serverURL + "/oauth/authorize",
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(metadata)

			case "/oauth/token":
				// Return a refresh response WITH a new ID token
				response := map[string]interface{}{
					"access_token":  "new-access-token",
					"token_type":    "Bearer",
					"expires_in":    3600,
					"refresh_token": "new-refresh-token",
					"id_token":      newIDToken,
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)

			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()
		serverURL = server.URL

		// Create token store
		store, err := oauth.NewTokenStore(oauth.TokenStoreConfig{
			StorageDir: tmpDir,
			FileMode:   true,
		})
		if err != nil {
			t.Fatalf("failed to create token store: %v", err)
		}

		originalIDToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyMTIzIiwiZW1haWwiOiJ1c2VyQGV4YW1wbGUuY29tIiwiZXhwIjoxNzA5MjQ1MjAwfQ.oldsignature"
		normalizedEndpoint := "https://muster.example.com"

		storedToken := &oauth.StoredToken{
			AccessToken:  "old-access-token",
			RefreshToken: "old-refresh-token",
			TokenType:    "Bearer",
			IDToken:      originalIDToken,
			ServerURL:    normalizedEndpoint,
			IssuerURL:    server.URL,
		}

		oauth2Token := storedToken.ToOAuth2Token()
		if err := store.StoreToken(normalizedEndpoint, server.URL, oauth2Token); err != nil {
			t.Fatalf("failed to store token: %v", err)
		}

		retrievedBefore := store.GetTokenIncludingExpiring(normalizedEndpoint)
		if retrievedBefore == nil {
			t.Fatal("expected to retrieve stored token")
		}

		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		ctx := context.Background()
		err = adapter.doTokenRefresh(ctx, store, retrievedBefore, normalizedEndpoint)
		if err != nil {
			t.Fatalf("doTokenRefresh failed: %v", err)
		}

		refreshedToken := store.GetTokenIncludingExpiring(normalizedEndpoint)
		if refreshedToken == nil {
			t.Fatal("expected to retrieve refreshed token")
		}

		// When a new ID token is provided, it should be used instead of the old one
		if refreshedToken.IDToken != newIDToken {
			t.Errorf("ID token should be updated when provided in refresh response.\nExpected: %q\nGot: %q",
				newIDToken, refreshedToken.IDToken)
		}
	})
}
