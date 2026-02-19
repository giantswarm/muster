package oauth

import (
	"context"
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
