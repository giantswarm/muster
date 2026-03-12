package cli

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/giantswarm/muster/internal/agent/oauth"
	"github.com/giantswarm/muster/internal/api"
)

func TestInvalidateServerSession(t *testing.T) {
	t.Run("sends DELETE request with session ID header", func(t *testing.T) {
		var receivedSessionID string
		var receivedMethod string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedMethod = r.Method
			receivedSessionID = r.Header.Get(api.ClientSessionIDHeader)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			sessionIDs:      make(map[string]string),
			tokenStorageDir: t.TempDir(),
		}

		adapter.invalidateServerSession(server.URL, "test-session-id")

		if receivedMethod != http.MethodDelete {
			t.Errorf("expected DELETE method, got %s", receivedMethod)
		}
		if receivedSessionID != "test-session-id" {
			t.Errorf("expected session ID 'test-session-id', got %q", receivedSessionID)
		}
	})

	t.Run("handles server unreachable gracefully", func(t *testing.T) {
		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			sessionIDs:      make(map[string]string),
			tokenStorageDir: t.TempDir(),
		}

		// Should not panic or return error -- just logs a warning
		adapter.invalidateServerSession("http://127.0.0.1:1", "test-session-id")
	})

	t.Run("handles non-204 response gracefully", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		adapter := &AuthAdapter{
			managers:        make(map[string]*oauth.AuthManager),
			sessionIDs:      make(map[string]string),
			tokenStorageDir: t.TempDir(),
		}

		// Should not panic -- just logs a warning
		adapter.invalidateServerSession(server.URL, "test-session-id")
	})
}

func TestLogout_InvalidatesServerSession(t *testing.T) {
	t.Run("sends invalidation request before local cleanup", func(t *testing.T) {
		var callCount atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount.Add(1)
			if r.Method != http.MethodDelete || r.URL.Path != "/session" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		// Store a session ID for the server endpoint
		adapter.UpdateSessionID(server.URL+"/mcp", "test-session-for-logout")

		err = adapter.Logout(server.URL + "/mcp")
		if err != nil {
			t.Errorf("unexpected error on logout: %v", err)
		}

		if callCount.Load() != 1 {
			t.Errorf("expected 1 invalidation call, got %d", callCount.Load())
		}

		// Session ID should be cleared locally
		if got := adapter.GetSessionIDForEndpoint(server.URL + "/mcp"); got != "" {
			t.Errorf("expected empty session ID after logout, got %q", got)
		}
	})

	t.Run("completes local cleanup when server is unreachable", func(t *testing.T) {
		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		// Store a session ID for an unreachable server
		adapter.UpdateSessionID("http://127.0.0.1:1/mcp", "test-session-unreachable")

		err = adapter.Logout("http://127.0.0.1:1/mcp")
		if err != nil {
			t.Errorf("unexpected error on logout with unreachable server: %v", err)
		}

		// Session ID should still be cleared locally
		if got := adapter.GetSessionIDForEndpoint("http://127.0.0.1:1/mcp"); got != "" {
			t.Errorf("expected empty session ID after logout, got %q", got)
		}
	})

	t.Run("skips server invalidation when no session ID exists", func(t *testing.T) {
		var callCount atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		// Logout without any session ID stored
		err = adapter.Logout(server.URL + "/mcp")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if callCount.Load() != 0 {
			t.Errorf("expected 0 invalidation calls when no session exists, got %d", callCount.Load())
		}
	})
}

func TestLogoutAll_InvalidatesAllServerSessions(t *testing.T) {
	// With the new LogoutAll, when access tokens are present the primary path
	// is DELETE /user-tokens (which clears all server-side downstream state).
	// Per-endpoint DELETE /session invalidation is only used as a fallback when
	// DELETE /user-tokens fails (e.g., old server that returns 404).
	t.Run("calls DELETE /user-tokens when access token available (primary path)", func(t *testing.T) {
		var deleteUserTokensCalled atomic.Int32

		server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete && r.URL.Path == "/user-tokens" {
				deleteUserTokensCalled.Add(1)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server1.Close()

		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		adapter.UpdateSessionID(server1.URL+"/mcp", "session-1")

		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token": "tok1",
			"token_type":   "Bearer",
			"server_url":   normalizeEndpoint(server1.URL + "/mcp"),
		}, normalizeEndpoint(server1.URL+"/mcp"))

		err = adapter.LogoutAll()
		if err != nil {
			t.Errorf("unexpected error on logout all: %v", err)
		}

		if deleteUserTokensCalled.Load() == 0 {
			t.Error("expected DELETE /user-tokens to be called as primary path")
		}
	})

	t.Run("falls back to per-endpoint invalidation when DELETE /user-tokens returns 404", func(t *testing.T) {
		receivedSessions := make(map[string]bool)
		server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete && r.URL.Path == "/user-tokens" {
				// Simulate old server that does not support DELETE /user-tokens
				w.WriteHeader(http.StatusNotFound)
				return
			}
			sid := r.Header.Get(api.ClientSessionIDHeader)
			if sid != "" {
				receivedSessions[sid] = true
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server1.Close()

		server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete && r.URL.Path == "/user-tokens" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			sid := r.Header.Get(api.ClientSessionIDHeader)
			if sid != "" {
				receivedSessions[sid] = true
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server2.Close()

		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		// Store session IDs for both servers
		adapter.UpdateSessionID(server1.URL+"/mcp", "session-1")
		adapter.UpdateSessionID(server2.URL+"/mcp", "session-2")

		// Write token files so collectEndpointSessionPairs can discover the endpoints.
		// In production, token files always exist for authenticated endpoints.
		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token": "tok1",
			"token_type":   "Bearer",
			"server_url":   normalizeEndpoint(server1.URL + "/mcp"),
		}, normalizeEndpoint(server1.URL+"/mcp"))
		writeTestTokenFile(t, tmpDir, map[string]interface{}{
			"access_token": "tok2",
			"token_type":   "Bearer",
			"server_url":   normalizeEndpoint(server2.URL + "/mcp"),
		}, normalizeEndpoint(server2.URL+"/mcp"))

		err = adapter.LogoutAll()
		if err != nil {
			t.Errorf("unexpected error on logout all: %v", err)
		}

		if !receivedSessions["session-1"] {
			t.Error("expected session-1 to be invalidated via fallback path")
		}
		if !receivedSessions["session-2"] {
			t.Error("expected session-2 to be invalidated via fallback path")
		}
	})
}
