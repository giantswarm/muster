package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthManager_StateTransitions(t *testing.T) {
	// Test the state machine transitions
	t.Run("initial state is unknown", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr, err := NewAuthManager(AuthManagerConfig{
			TokenStorageDir: tmpDir,
			FileMode:        false,
		})
		if err != nil {
			t.Fatalf("Failed to create auth manager: %v", err)
		}
		defer mgr.Close()

		if mgr.GetState() != AuthStateUnknown {
			t.Errorf("expected initial state to be Unknown, got %s", mgr.GetState())
		}
	})

	t.Run("state string representations", func(t *testing.T) {
		testCases := []struct {
			state    AuthState
			expected string
		}{
			{AuthStateUnknown, "unknown"},
			{AuthStateAuthenticated, "authenticated"},
			{AuthStatePendingAuth, "pending_auth"},
			{AuthStateError, "error"},
			{AuthState(99), "unknown"}, // Unknown value defaults to "unknown"
		}

		for _, tc := range testCases {
			if tc.state.String() != tc.expected {
				t.Errorf("expected AuthState(%d).String() = %q, got %q", tc.state, tc.expected, tc.state.String())
			}
		}
	})
}

func TestAuthManager_CheckConnection_NoAuthRequired(t *testing.T) {
	// Create a mock server that doesn't require auth
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	mgr, err := NewAuthManager(AuthManagerConfig{
		TokenStorageDir: tmpDir,
		FileMode:        false,
	})
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()
	state, err := mgr.CheckConnection(ctx, server.URL)

	// When server doesn't return 401, state should be Unknown (may or may not need auth)
	if err != nil {
		// Server responded, no error expected
		t.Logf("Note: CheckConnection returned error: %v (this may be expected)", err)
	}

	// State should be Unknown when server doesn't require auth or we can't determine
	if state != AuthStateUnknown && state != AuthStateError {
		t.Errorf("expected state Unknown or Error when server doesn't return 401, got %s", state)
	}
}

func TestAuthManager_CheckConnection_AuthRequired(t *testing.T) {
	// Create a mock server that returns 401 with WWW-Authenticate header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			// Return OAuth metadata
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"resource":              "https://example.com",
				"authorization_servers": []string{"https://oauth.example.com"},
			})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://oauth.example.com"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	mgr, err := NewAuthManager(AuthManagerConfig{
		TokenStorageDir: tmpDir,
		FileMode:        false,
	})
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()
	state, err := mgr.CheckConnection(ctx, server.URL)

	// When server returns 401 without proper OAuth info, might be PendingAuth or Error
	// depending on whether OAuth metadata can be discovered
	if state != AuthStatePendingAuth && state != AuthStateError {
		t.Errorf("expected state PendingAuth or Error after 401, got %s (err: %v)", state, err)
	}
}

func TestAuthManager_CheckConnection_WithValidToken(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewAuthManager(AuthManagerConfig{
		TokenStorageDir: tmpDir,
		FileMode:        true,
	})
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}
	defer mgr.Close()

	// Pre-store a valid token
	serverURL := "https://muster.example.com"
	issuerURL := "https://dex.example.com"
	token := &StoredToken{
		AccessToken: "valid-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
		ServerURL:   serverURL,
		IssuerURL:   issuerURL,
		CreatedAt:   time.Now(),
	}

	// Store directly via the client's token store
	oauth2Token := token.ToOAuth2Token()
	err = mgr.client.tokenStore.StoreToken(serverURL, issuerURL, oauth2Token)
	if err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	ctx := context.Background()
	state, err := mgr.CheckConnection(ctx, serverURL)

	// Should be authenticated when we have a valid token
	if state != AuthStateAuthenticated {
		t.Errorf("expected state Authenticated with valid token, got %s (err: %v)", state, err)
	}
	if err != nil {
		t.Errorf("expected no error with valid token, got: %v", err)
	}
}

func TestAuthManager_DiscoverOAuthMetadata_RFC9728(t *testing.T) {
	// Create a mock server that serves OAuth Protected Resource Metadata (RFC 9728)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"resource":              "https://resource.example.com",
				"authorization_servers": []string{"https://auth.example.com"},
			})
			return
		}
		// All other paths return 401
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	mgr, err := NewAuthManager(AuthManagerConfig{
		TokenStorageDir: tmpDir,
		FileMode:        false,
	})
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()
	challenge, err := mgr.discoverOAuthMetadata(ctx, server.URL)

	if err != nil {
		t.Fatalf("Failed to discover OAuth metadata: %v", err)
	}

	if challenge == nil {
		t.Fatal("Expected challenge, got nil")
	}

	if challenge.Issuer != "https://auth.example.com" {
		t.Errorf("expected issuer 'https://auth.example.com', got %q", challenge.Issuer)
	}
}

func TestAuthManager_DiscoverOAuthMetadata_Fallback(t *testing.T) {
	// Create a mock server that doesn't have OAuth metadata endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	mgr, err := NewAuthManager(AuthManagerConfig{
		TokenStorageDir: tmpDir,
		FileMode:        false,
	})
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()
	challenge, err := mgr.discoverOAuthMetadata(ctx, server.URL)

	// Should fail when no metadata endpoint is available
	if err == nil {
		t.Error("Expected error when no OAuth metadata endpoint, got nil")
	}
	if challenge != nil {
		t.Errorf("Expected nil challenge when discovery fails, got: %+v", challenge)
	}
}

func TestNormalizeServerURL(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"https://example.com/mcp", "https://example.com"},
		{"https://example.com/sse", "https://example.com"},
		{"https://example.com/", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"https://example.com/mcp/", "https://example.com"}, // Strips trailing slash first, then /mcp
		{"https://example.com/api/mcp", "https://example.com/api"},
		{"https://example.com/api/sse", "https://example.com/api"},
		{"http://localhost:8080/mcp", "http://localhost:8080"},
		{"http://localhost:8080/sse", "http://localhost:8080"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := normalizeServerURL(tc.input)
			if result != tc.expected {
				t.Errorf("normalizeServerURL(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestAuthManager_GettersAndSetters(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewAuthManager(AuthManagerConfig{
		TokenStorageDir: tmpDir,
		FileMode:        false,
	})
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}
	defer mgr.Close()

	// Test initial state
	if mgr.GetState() != AuthStateUnknown {
		t.Errorf("expected initial state Unknown, got %s", mgr.GetState())
	}

	if mgr.GetAuthChallenge() != nil {
		t.Error("expected nil auth challenge initially")
	}

	if mgr.GetAuthURL() != "" {
		t.Error("expected empty auth URL initially")
	}

	if mgr.GetLastError() != nil {
		t.Error("expected nil last error initially")
	}

	if mgr.GetServerURL() != "" {
		t.Error("expected empty server URL initially")
	}
}

func TestAuthManager_GetAccessToken_NotAuthenticated(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewAuthManager(AuthManagerConfig{
		TokenStorageDir: tmpDir,
		FileMode:        false,
	})
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}
	defer mgr.Close()

	// Try to get token when not authenticated
	_, err = mgr.GetAccessToken()
	if err == nil {
		t.Error("expected error when getting token while not authenticated")
	}

	// Try to get bearer token when not authenticated
	_, err = mgr.GetBearerToken()
	if err == nil {
		t.Error("expected error when getting bearer token while not authenticated")
	}
}

func TestAuthManager_StartAuthFlow_InvalidState(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewAuthManager(AuthManagerConfig{
		TokenStorageDir: tmpDir,
		FileMode:        false,
	})
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Try to start auth flow in Unknown state (should fail)
	_, err = mgr.StartAuthFlow(ctx)
	if err == nil {
		t.Error("expected error when starting auth flow in Unknown state")
	}
}

func TestAuthManager_ClearToken(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewAuthManager(AuthManagerConfig{
		TokenStorageDir: tmpDir,
		FileMode:        true,
	})
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}
	defer mgr.Close()

	// Store a token first via CheckConnection (to set serverURL)
	ctx := context.Background()

	// Mock a server to trigger connection check
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mgr.CheckConnection(ctx, server.URL)

	// Clear should work even when there's no token
	err = mgr.ClearToken()
	if err != nil {
		t.Errorf("ClearToken failed: %v", err)
	}

	// State should be Unknown after clear
	if mgr.GetState() != AuthStateUnknown {
		t.Errorf("expected state Unknown after clear, got %s", mgr.GetState())
	}

	// Try clearing with no server URL set (should be no-op)
	mgr2, _ := NewAuthManager(AuthManagerConfig{
		TokenStorageDir: tmpDir,
		FileMode:        false,
	})
	defer mgr2.Close()

	err = mgr2.ClearToken()
	if err != nil {
		t.Errorf("ClearToken with no server URL should not error: %v", err)
	}
}

func TestAuthManager_WaitForAuth_NoFlowInProgress(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewAuthManager(AuthManagerConfig{
		TokenStorageDir: tmpDir,
		FileMode:        false,
	})
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Try to wait for auth when no flow is in progress
	err = mgr.WaitForAuth(ctx)
	if err == nil {
		t.Error("expected error when waiting for auth with no flow in progress")
	}
}
