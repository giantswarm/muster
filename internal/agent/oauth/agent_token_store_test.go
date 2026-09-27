package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"

	"golang.org/x/oauth2"
)

func TestAgentTokenStore_GetToken_NoToken(t *testing.T) {
	store := createTestTokenStore(t)
	agentStore := NewAgentTokenStore("https://example.com", store)

	_, err := agentStore.GetToken(context.Background())
	if err != transport.ErrNoToken {
		t.Errorf("expected ErrNoToken, got: %v", err)
	}
}

func TestAgentTokenStore_GetToken_ReturnsStoredToken(t *testing.T) {
	store := createTestTokenStore(t)
	serverURL := "https://example.com"
	issuerURL := "https://issuer.example.com"

	token := &oauth2.Token{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(1 * time.Hour),
	}
	token = token.WithExtra(map[string]interface{}{
		"id_token": "test-id-token",
	})

	if err := store.StoreToken(serverURL, issuerURL, token); err != nil {
		t.Fatalf("StoreToken failed: %v", err)
	}

	agentStore := NewAgentTokenStore(serverURL, store)

	got, err := agentStore.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken failed: %v", err)
	}

	if got.AccessToken != "test-access-token" {
		t.Errorf("expected access token 'test-access-token', got '%s'", got.AccessToken)
	}
	if got.RefreshToken != "test-refresh-token" {
		t.Errorf("expected refresh token 'test-refresh-token', got '%s'", got.RefreshToken)
	}
	if got.TokenType != "Bearer" {
		t.Errorf("expected token type 'Bearer', got '%s'", got.TokenType)
	}
	if got.ExpiresAt.IsZero() {
		t.Error("expected non-zero ExpiresAt")
	}
}

func TestAgentTokenStore_GetToken_CachesIDToken(t *testing.T) {
	store := createTestTokenStore(t)
	serverURL := "https://example.com"

	token := &oauth2.Token{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(1 * time.Hour),
	}
	token = token.WithExtra(map[string]interface{}{
		"id_token": "test-id-token",
	})

	if err := store.StoreToken(serverURL, "https://issuer.example.com", token); err != nil {
		t.Fatalf("StoreToken failed: %v", err)
	}

	agentStore := NewAgentTokenStore(serverURL, store)

	_, err := agentStore.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken failed: %v", err)
	}

	if got := agentStore.GetIDToken(); got != "test-id-token" {
		t.Errorf("expected ID token 'test-id-token', got '%s'", got)
	}
}

func TestAgentTokenStore_SaveToken_Persists(t *testing.T) {
	store := createTestTokenStore(t)
	serverURL := "https://example.com"

	// First store a token to set the issuer URL
	initialToken := &oauth2.Token{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(1 * time.Hour),
	}
	if err := store.StoreToken(serverURL, "https://issuer.example.com", initialToken); err != nil {
		t.Fatalf("initial StoreToken failed: %v", err)
	}

	agentStore := NewAgentTokenStore(serverURL, store)

	// Read to populate the cached issuer
	_, err := agentStore.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken failed: %v", err)
	}

	// Save a new token (simulating mcp-go refresh)
	newToken := &transport.Token{
		AccessToken:  "new-access-token",
		TokenType:    "Bearer",
		RefreshToken: "new-refresh-token",
		ExpiresAt:    time.Now().Add(2 * time.Hour),
	}

	if err := agentStore.SaveToken(context.Background(), newToken); err != nil {
		t.Fatalf("SaveToken failed: %v", err)
	}

	// Verify the token was persisted by reading it back
	got, err := agentStore.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken after SaveToken failed: %v", err)
	}

	if got.AccessToken != "new-access-token" {
		t.Errorf("expected access token 'new-access-token', got '%s'", got.AccessToken)
	}
	if got.RefreshToken != "new-refresh-token" {
		t.Errorf("expected refresh token 'new-refresh-token', got '%s'", got.RefreshToken)
	}
}

func TestAgentTokenStore_SaveToken_PreservesIDToken(t *testing.T) {
	store := createTestTokenStore(t)
	serverURL := "https://example.com"

	// Store initial token with ID token
	initialToken := &oauth2.Token{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(1 * time.Hour),
	}
	initialToken = initialToken.WithExtra(map[string]interface{}{
		"id_token": "my-id-token",
	})
	if err := store.StoreToken(serverURL, "https://issuer.example.com", initialToken); err != nil {
		t.Fatalf("initial StoreToken failed: %v", err)
	}

	agentStore := NewAgentTokenStore(serverURL, store)

	// Read to populate the cached ID token
	_, err := agentStore.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken failed: %v", err)
	}

	// Save a new token WITHOUT ID token (simulating refresh response)
	newToken := &transport.Token{
		AccessToken:  "refreshed-access-token",
		TokenType:    "Bearer",
		RefreshToken: "refreshed-refresh-token",
		ExpiresAt:    time.Now().Add(2 * time.Hour),
	}

	if err := agentStore.SaveToken(context.Background(), newToken); err != nil {
		t.Fatalf("SaveToken failed: %v", err)
	}

	// Verify the ID token is still cached
	if got := agentStore.GetIDToken(); got != "my-id-token" {
		t.Errorf("expected preserved ID token 'my-id-token', got '%s'", got)
	}
}

func TestAgentTokenStore_GetToken_ContextCancelled(t *testing.T) {
	store := createTestTokenStore(t)
	agentStore := NewAgentTokenStore("https://example.com", store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := agentStore.GetToken(ctx)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func createTestTokenStore(t *testing.T) *TokenStore {
	t.Helper()
	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: t.TempDir(),
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("failed to create token store: %v", err)
	}
	return store
}

// TestSetupOAuthConfig_RefreshKeepsTheNewIDToken drives a refresh through
// mcp-go's OAuth handler with the config SetupOAuthConfigWithDir builds: the
// refreshed ID token must reach the store next to the new access token.
func TestSetupOAuthConfig_RefreshKeepsTheNewIDToken(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 server.URL,
				"authorization_endpoint": server.URL + "/authorize",
				"token_endpoint":         server.URL + "/token",
			})
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "new-access-token",
				"token_type":    "Bearer",
				"refresh_token": "new-refresh-token",
				"expires_in":    3600,
				"id_token":      "new-id-token",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg, _, err := SetupOAuthConfigWithDir(server.URL, dir)
	if err != nil {
		t.Fatalf("SetupOAuthConfigWithDir failed: %v", err)
	}
	cfg.AuthServerMetadataURL = server.URL + "/.well-known/oauth-authorization-server"

	store, err := NewTokenStore(TokenStoreConfig{StorageDir: dir, FileMode: true})
	if err != nil {
		t.Fatalf("NewTokenStore failed: %v", err)
	}
	expired := (&oauth2.Token{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(-time.Hour),
	}).WithExtra(map[string]interface{}{"id_token": "old-id-token"})
	if err := store.StoreToken(server.URL, server.URL, expired); err != nil {
		t.Fatalf("StoreToken failed: %v", err)
	}

	handler := transport.NewOAuthHandler(*cfg)
	handler.SetBaseURL(server.URL)
	header, err := handler.GetAuthorizationHeader(context.Background())
	if err != nil {
		t.Fatalf("GetAuthorizationHeader failed: %v", err)
	}
	if header != "Bearer new-access-token" {
		t.Fatalf("expected the refreshed access token, got %q", header)
	}

	reread, err := NewTokenStore(TokenStoreConfig{StorageDir: dir, FileMode: true})
	if err != nil {
		t.Fatalf("NewTokenStore failed: %v", err)
	}
	got := reread.GetToken(server.URL)
	if got == nil {
		t.Fatal("expected the refreshed token in the store")
	}
	if got.IDToken != "new-id-token" {
		t.Errorf("expected the refreshed ID token, got %q", got.IDToken)
	}
	if got.RefreshToken != "new-refresh-token" {
		t.Errorf("expected the rotated refresh token, got %q", got.RefreshToken)
	}
}

func TestAgentTokenStore_SaveToken_KeepsTheCachedIDTokenWithoutARefreshedOne(t *testing.T) {
	store := createTestTokenStore(t)
	serverURL := "https://example.com"
	stored := (&oauth2.Token{
		AccessToken: "access", TokenType: "Bearer", RefreshToken: "refresh",
		Expiry: time.Now().Add(time.Hour),
	}).WithExtra(map[string]interface{}{"id_token": "cached-id-token"})
	if err := store.StoreToken(serverURL, "https://issuer.example.com", stored); err != nil {
		t.Fatalf("StoreToken failed: %v", err)
	}

	agentStore := NewAgentTokenStore(serverURL, store)
	if _, err := agentStore.GetToken(context.Background()); err != nil {
		t.Fatalf("GetToken failed: %v", err)
	}
	if err := agentStore.SaveToken(context.Background(), &transport.Token{
		AccessToken: "access-2", TokenType: "Bearer", RefreshToken: "refresh",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveToken failed: %v", err)
	}
	if got := store.GetToken(serverURL); got == nil || got.IDToken != "cached-id-token" {
		t.Errorf("expected the cached ID token to be kept, got %+v", got)
	}
}
