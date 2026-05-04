package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/agent/oauth"
)

func TestNewAuthAdapter(t *testing.T) {
	t.Run("creates adapter with default configuration", func(t *testing.T) {
		adapter, err := NewAuthAdapter()
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer func() { _ = adapter.Close() }()

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

func TestAuthAdapter_HasCredentials(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	// Without any tokens, should return false
	if adapter.HasCredentials("https://example.com") {
		t.Error("expected HasCredentials to return false when no token exists")
	}
}

func TestAuthAdapter_GetBearerToken_NotAuthenticated(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

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
	defer func() { _ = adapter.Close() }()

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
	defer func() { _ = adapter.Close() }()

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
	defer func() { _ = adapter.Close() }()

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
	defer func() { _ = adapter.Close() }()

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
		err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("test"), 0644) //nolint:gosec
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
		err := os.WriteFile(tmpFile, []byte("not valid json"), 0644) //nolint:gosec
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
		defer func() { _ = adapter.Close() }()

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
		defer func() { _ = adapter.Close() }()

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
	defer func() { _ = adapter.Close() }()

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

// ---------------------------------------------------------------------------
// Tests for revokeRefreshToken
// ---------------------------------------------------------------------------

func TestRevokeRefreshToken(t *testing.T) {
	t.Run("POSTs to correct revoke URL with form-encoded body", func(t *testing.T) {
		var receivedMethod string
		var receivedPath string
		var receivedBody string
		var receivedContentType string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedMethod = r.Method
			receivedPath = r.URL.Path
			receivedContentType = r.Header.Get("Content-Type")
			data, _ := io.ReadAll(r.Body)
			receivedBody = string(data)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: t.TempDir(),
		}

		adapter.revokeRefreshToken(srv.URL, "my-refresh-token")

		if receivedMethod != http.MethodPost {
			t.Errorf("expected POST, got %s", receivedMethod)
		}
		if receivedPath != "/oauth/revoke" { //nolint:goconst
			t.Errorf("expected path /oauth/revoke, got %s", receivedPath)
		}
		if receivedContentType != "application/x-www-form-urlencoded" {
			t.Errorf("expected Content-Type application/x-www-form-urlencoded, got %s", receivedContentType)
		}
		if !strings.Contains(receivedBody, "token=my-refresh-token") {
			t.Errorf("expected body to contain token=my-refresh-token, got %q", receivedBody)
		}
		if !strings.Contains(receivedBody, "token_type_hint=refresh_token") {
			t.Errorf("expected body to contain token_type_hint=refresh_token, got %q", receivedBody)
		}
	})

	t.Run("handles server error response gracefully", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: t.TempDir(),
		}

		// Should not panic or return an error -- best effort
		adapter.revokeRefreshToken(srv.URL, "some-token")
	})

	t.Run("handles server unreachable gracefully", func(t *testing.T) {
		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: t.TempDir(),
		}

		// Should not panic -- server is unreachable
		adapter.revokeRefreshToken("http://127.0.0.1:1", "some-token")
	})

	t.Run("handles non-200 status without crashing", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer srv.Close()

		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: t.TempDir(),
		}

		// Should not panic -- just log warning
		adapter.revokeRefreshToken(srv.URL, "token")
	})
}

// ---------------------------------------------------------------------------
// Tests for deleteUserTokens
// ---------------------------------------------------------------------------

func TestDeleteUserTokens(t *testing.T) {
	t.Run("sends DELETE with Bearer token and returns true on 204", func(t *testing.T) {
		var receivedMethod string
		var receivedAuth string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedMethod = r.Method
			receivedAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: t.TempDir(),
		}

		result := adapter.deleteUserTokens(srv.URL, "my-access-token")

		if !result {
			t.Error("expected true on 204 response")
		}
		if receivedMethod != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", receivedMethod)
		}
		if receivedAuth != "Bearer my-access-token" {
			t.Errorf("expected Authorization header 'Bearer my-access-token', got %q", receivedAuth)
		}
	})

	t.Run("sends request to /user-tokens path", func(t *testing.T) {
		var receivedPath string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: t.TempDir(),
		}

		adapter.deleteUserTokens(srv.URL, "token")

		if receivedPath != "/user-tokens" { //nolint:goconst
			t.Errorf("expected path /user-tokens, got %s", receivedPath)
		}
	})

	t.Run("returns false on 404 response (old server)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: t.TempDir(),
		}

		result := adapter.deleteUserTokens(srv.URL, "token")

		if result {
			t.Error("expected false on 404 response")
		}
	})

	t.Run("returns false on 401 response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: t.TempDir(),
		}

		result := adapter.deleteUserTokens(srv.URL, "expired-token")

		if result {
			t.Error("expected false on 401 response")
		}
	})

	t.Run("returns false when server is unreachable", func(t *testing.T) {
		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: t.TempDir(),
		}

		result := adapter.deleteUserTokens("http://127.0.0.1:1", "token")

		if result {
			t.Error("expected false when server is unreachable")
		}
	})

	t.Run("returns false on 500 response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: t.TempDir(),
		}

		result := adapter.deleteUserTokens(srv.URL, "token")

		if result {
			t.Error("expected false on 500 response")
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for collectAllEndpoints
// ---------------------------------------------------------------------------

func TestCollectAllEndpoints(t *testing.T) {
	t.Run("returns empty slice when no managers and no token files", func(t *testing.T) {
		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: t.TempDir(),
		}

		endpoints := adapter.collectAllEndpoints()

		if len(endpoints) != 0 {
			t.Errorf("expected 0 endpoints, got %d: %v", len(endpoints), endpoints)
		}
	})

	t.Run("returns endpoints from token files", func(t *testing.T) {
		tmpDir := t.TempDir()
		serverURL1 := "https://server1.example.com"
		serverURL2 := "https://server2.example.com"

		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token": "tok1",
			"token_type":   "Bearer",
			"server_url":   serverURL1,
		}, serverURL1)
		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token": "tok2",
			"token_type":   "Bearer",
			"server_url":   serverURL2,
		}, serverURL2)

		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			tokenStorageDir: tmpDir,
		}

		endpoints := adapter.collectAllEndpoints()

		if len(endpoints) != 2 {
			t.Errorf("expected 2 endpoints, got %d: %v", len(endpoints), endpoints)
		}

		epSet := make(map[string]bool)
		for _, ep := range endpoints {
			epSet[ep] = true
		}
		if !epSet[serverURL1] {
			t.Errorf("expected %s in endpoints", serverURL1)
		}
		if !epSet[serverURL2] {
			t.Errorf("expected %s in endpoints", serverURL2)
		}
	})

	t.Run("deduplicates endpoints present in both managers and token files", func(t *testing.T) {
		tmpDir := t.TempDir()
		serverURL := "https://shared.example.com"

		// Write a token file for the same endpoint
		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token": "tok",
			"token_type":   "Bearer",
			"server_url":   serverURL,
		}, serverURL)

		adapter := &AuthAdapter{
			managers: map[string]*oauth.AuthManager{
				serverURL: nil, // key only needed for dedup test
			},
			tokenStorageDir: tmpDir,
		}

		endpoints := adapter.collectAllEndpoints()

		if len(endpoints) != 1 {
			t.Errorf("expected 1 deduplicated endpoint, got %d: %v", len(endpoints), endpoints)
		}
		if endpoints[0] != serverURL {
			t.Errorf("expected %s, got %s", serverURL, endpoints[0])
		}
	})

	t.Run("normalizes endpoints from token files before deduplication", func(t *testing.T) {
		tmpDir := t.TempDir()
		// ServerURL with /mcp suffix - normalizeEndpoint will strip it
		rawURL := "https://server.example.com/mcp"
		normalizedURL := normalizeEndpoint(rawURL) // "https://server.example.com"

		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token": "tok",
			"token_type":   "Bearer",
			"server_url":   rawURL,
		}, rawURL)

		// Also add the normalized URL in managers
		adapter := &AuthAdapter{
			managers: map[string]*oauth.AuthManager{
				normalizedURL: nil,
			},
			tokenStorageDir: tmpDir,
		}

		endpoints := adapter.collectAllEndpoints()

		if len(endpoints) != 1 {
			t.Errorf("expected 1 deduplicated normalized endpoint, got %d: %v", len(endpoints), endpoints)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for Logout with token revocation
// ---------------------------------------------------------------------------

func TestLogout_RevokesRefreshToken(t *testing.T) {
	t.Run("revokes refresh token before local cleanup", func(t *testing.T) {
		var revokeCallCount atomic.Int32
		var receivedRefreshToken string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/oauth/revoke" {
				revokeCallCount.Add(1)
				data, _ := io.ReadAll(r.Body)
				body := string(data)
				// Extract the token value
				for _, part := range strings.Split(body, "&") {
					if strings.HasPrefix(part, "token=") {
						receivedRefreshToken = strings.TrimPrefix(part, "token=")
					}
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			// Session endpoint (DELETE /session)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer func() { _ = adapter.Close() }()

		// Write a token file with a refresh token for the server
		serverURL := normalizeEndpoint(srv.URL + "/mcp")
		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token":  "access-tok",
			"refresh_token": "refresh-tok-to-revoke",
			"token_type":    "Bearer",
			"server_url":    serverURL,
		}, serverURL)

		err = adapter.Logout(srv.URL + "/mcp")
		if err != nil {
			t.Errorf("unexpected error on logout: %v", err)
		}

		if revokeCallCount.Load() != 1 {
			t.Errorf("expected 1 revoke call, got %d", revokeCallCount.Load())
		}
		if receivedRefreshToken != "refresh-tok-to-revoke" {
			t.Errorf("expected refresh token 'refresh-tok-to-revoke', got %q", receivedRefreshToken)
		}
	})

	t.Run("skips revocation when no refresh token present", func(t *testing.T) {
		var revokeCallCount atomic.Int32

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/oauth/revoke" {
				revokeCallCount.Add(1)
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer func() { _ = adapter.Close() }()

		// Write a token file with NO refresh token
		serverURL := normalizeEndpoint(srv.URL + "/mcp")
		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token": "access-tok",
			"token_type":   "Bearer",
			"server_url":   serverURL,
		}, serverURL)

		err = adapter.Logout(srv.URL + "/mcp")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if revokeCallCount.Load() != 0 {
			t.Errorf("expected 0 revoke calls when no refresh token, got %d", revokeCallCount.Load())
		}
	})

	t.Run("proceeds with local cleanup even when revoke server is unreachable", func(t *testing.T) {
		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer func() { _ = adapter.Close() }()

		unreachableURL := "http://127.0.0.1:1"
		serverURL := normalizeEndpoint(unreachableURL + "/mcp")
		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token":  "tok",
			"refresh_token": "refresh-tok",
			"token_type":    "Bearer",
			"server_url":    serverURL,
		}, serverURL)

		// Should complete without error even though revocation fails
		err = adapter.Logout(unreachableURL + "/mcp")
		if err != nil {
			t.Errorf("unexpected error when revoke server is unreachable: %v", err)
		}
	})

	t.Run("skips revocation when no stored token exists", func(t *testing.T) {
		var revokeCallCount atomic.Int32

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/oauth/revoke" {
				revokeCallCount.Add(1)
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer func() { _ = adapter.Close() }()

		// No token file written -- logout a fresh endpoint
		err = adapter.Logout(srv.URL + "/mcp")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if revokeCallCount.Load() != 0 {
			t.Errorf("expected 0 revoke calls when no token, got %d", revokeCallCount.Load())
		}
	})
}

// ---------------------------------------------------------------------------
// Tests for LogoutAll with token revocation and DELETE /user-tokens
// ---------------------------------------------------------------------------

func TestLogoutAll_RevokesAndDeletesUserTokens(t *testing.T) {
	t.Run("calls DELETE /user-tokens with Bearer token on success", func(t *testing.T) {
		var deleteUserTokensCalled atomic.Bool
		var receivedAuth string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete && r.URL.Path == "/user-tokens" {
				deleteUserTokensCalled.Store(true)
				receivedAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// Revocation and session endpoints
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer func() { _ = adapter.Close() }()

		serverURL := normalizeEndpoint(srv.URL + "/mcp")
		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token":  "my-access-token",
			"refresh_token": "my-refresh-token",
			"token_type":    "Bearer",
			"server_url":    serverURL,
		}, serverURL)

		err = adapter.LogoutAll()
		if err != nil {
			t.Errorf("unexpected error on LogoutAll: %v", err)
		}

		if !deleteUserTokensCalled.Load() {
			t.Error("expected DELETE /user-tokens to be called")
		}
		if receivedAuth != "Bearer my-access-token" {
			t.Errorf("expected Authorization 'Bearer my-access-token', got %q", receivedAuth)
		}
	})

	t.Run("revokes refresh token for each endpoint", func(t *testing.T) {
		var revokeCount atomic.Int32

		srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/oauth/revoke" {
				revokeCount.Add(1)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv1.Close()

		srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/oauth/revoke" {
				revokeCount.Add(1)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv2.Close()

		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer func() { _ = adapter.Close() }()

		url1 := normalizeEndpoint(srv1.URL + "/mcp")
		url2 := normalizeEndpoint(srv2.URL + "/mcp")

		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"token_type":    "Bearer",
			"server_url":    url1,
		}, url1)
		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token":  "access-2",
			"refresh_token": "refresh-2",
			"token_type":    "Bearer",
			"server_url":    url2,
		}, url2)

		err = adapter.LogoutAll()
		if err != nil {
			t.Errorf("unexpected error on LogoutAll: %v", err)
		}

		if revokeCount.Load() != 2 {
			t.Errorf("expected 2 revoke calls (one per endpoint), got %d", revokeCount.Load())
		}
	})

	t.Run("succeeds when DELETE /user-tokens returns 404", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete && r.URL.Path == "/user-tokens" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer func() { _ = adapter.Close() }()

		serverURL := normalizeEndpoint(srv.URL + "/mcp")
		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token": "access-tok",
			"token_type":   "Bearer",
			"server_url":   serverURL,
		}, serverURL)

		err = adapter.LogoutAll()
		if err != nil {
			t.Errorf("unexpected error on LogoutAll: %v", err)
		}
	})

	t.Run("skips DELETE /user-tokens when no access token available", func(t *testing.T) {
		var deleteUserTokensCalled atomic.Bool

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete && r.URL.Path == "/user-tokens" {
				deleteUserTokensCalled.Store(true)
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer func() { _ = adapter.Close() }()

		// No token files written -- nothing to delete
		err = adapter.LogoutAll()
		if err != nil {
			t.Errorf("unexpected error on LogoutAll: %v", err)
		}

		if deleteUserTokensCalled.Load() {
			t.Error("expected DELETE /user-tokens NOT to be called when no access token is available")
		}
	})

	t.Run("clears all local state after logout", func(t *testing.T) {
		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer func() { _ = adapter.Close() }()

		serverURL := "https://server.example.com"
		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token": "tok",
			"token_type":   "Bearer",
			"server_url":   serverURL,
		}, serverURL)

		err = adapter.LogoutAll()
		if err != nil {
			t.Errorf("unexpected error on LogoutAll: %v", err)
		}

		// Managers map should be empty
		adapter.mu.RLock()
		managerCount := len(adapter.managers)
		adapter.mu.RUnlock()
		if managerCount != 0 {
			t.Errorf("expected 0 managers after LogoutAll, got %d", managerCount)
		}
	})
}

// writeTestTokenFile writes a StoredToken as a JSON file in the token store dir
// using the same key derivation the token store uses (SHA256 of server URL).
func writeTestTokenFile(t *testing.T, dir string, tok map[string]interface{}, serverURL string) {
	t.Helper()
	hash := sha256.Sum256([]byte(serverURL))
	key := hex.EncodeToString(hash[:16])
	data, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("failed to marshal token: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, key+".json"), data, 0600); err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}
}

func TestGetStatusFromManager_RefreshExpiresAt(t *testing.T) {
	serverURL := "https://muster.example.com"
	issuerURL := "https://dex.example.com"

	t.Run("sets RefreshExpiresAt when refresh token and CreatedAt are present", func(t *testing.T) {
		tmpDir := t.TempDir()
		createdAt := time.Now().Add(-2 * time.Hour)

		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"token_type":    "Bearer",
			"expiry":        time.Now().Add(25 * time.Minute).Format(time.RFC3339),
			"server_url":    serverURL,
			"issuer_url":    issuerURL,
			"created_at":    createdAt.Format(time.RFC3339),
		}, serverURL)

		mgr, err := oauth.NewAuthManager(oauth.AuthManagerConfig{
			TokenStorageDir: tmpDir,
			FileMode:        true,
		})
		if err != nil {
			t.Fatalf("failed to create auth manager: %v", err)
		}
		defer func() { _ = mgr.Close() }()

		ctx := context.Background()
		_, _ = mgr.CheckConnection(ctx, serverURL)

		adapter := &AuthAdapter{managers: make(map[string]*oauth.AuthManager)}
		status := adapter.getStatusFromManager(serverURL, mgr)

		if !status.Authenticated {
			t.Fatal("expected authenticated status")
		}
		if !status.HasRefreshToken {
			t.Fatal("expected HasRefreshToken to be true")
		}

		expectedExpiry := createdAt.Add(pkgoauth.DefaultSessionDuration)
		diff := status.RefreshExpiresAt.Sub(expectedExpiry)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("RefreshExpiresAt = %v, want ~%v (diff: %v)", status.RefreshExpiresAt, expectedExpiry, diff)
		}
	})

	t.Run("leaves RefreshExpiresAt zero when no refresh token", func(t *testing.T) {
		tmpDir := t.TempDir()

		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token": "access-token",
			"token_type":   "Bearer",
			"expiry":       time.Now().Add(25 * time.Minute).Format(time.RFC3339),
			"server_url":   serverURL,
			"issuer_url":   issuerURL,
			"created_at":   time.Now().Format(time.RFC3339),
		}, serverURL)

		mgr, err := oauth.NewAuthManager(oauth.AuthManagerConfig{
			TokenStorageDir: tmpDir,
			FileMode:        true,
		})
		if err != nil {
			t.Fatalf("failed to create auth manager: %v", err)
		}
		defer func() { _ = mgr.Close() }()

		ctx := context.Background()
		_, _ = mgr.CheckConnection(ctx, serverURL)

		adapter := &AuthAdapter{managers: make(map[string]*oauth.AuthManager)}
		status := adapter.getStatusFromManager(serverURL, mgr)

		if !status.Authenticated {
			t.Fatal("expected authenticated status")
		}
		if status.HasRefreshToken {
			t.Error("expected HasRefreshToken to be false without refresh token")
		}
		if !status.RefreshExpiresAt.IsZero() {
			t.Errorf("expected RefreshExpiresAt to be zero, got %v", status.RefreshExpiresAt)
		}
	})
}
