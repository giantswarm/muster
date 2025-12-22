package oauth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestTokenStore_StoreAndGet(t *testing.T) {
	// Create a temporary directory for token storage
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false, // In-memory only for this test
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	serverURL := "https://muster.example.com"
	issuerURL := "https://dex.example.com"
	token := &oauth2.Token{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(1 * time.Hour),
	}

	// Store the token
	if err := store.StoreToken(serverURL, issuerURL, token); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// Retrieve the token
	storedToken := store.GetToken(serverURL)
	if storedToken == nil {
		t.Fatal("Expected to get stored token, got nil")
	}

	if storedToken.AccessToken != token.AccessToken {
		t.Errorf("Expected access token %q, got %q", token.AccessToken, storedToken.AccessToken)
	}

	if storedToken.RefreshToken != token.RefreshToken {
		t.Errorf("Expected refresh token %q, got %q", token.RefreshToken, storedToken.RefreshToken)
	}

	if storedToken.ServerURL != serverURL {
		t.Errorf("Expected server URL %q, got %q", serverURL, storedToken.ServerURL)
	}

	if storedToken.IssuerURL != issuerURL {
		t.Errorf("Expected issuer URL %q, got %q", issuerURL, storedToken.IssuerURL)
	}
}

func TestTokenStore_ExpiredToken(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	serverURL := "https://muster.example.com"
	issuerURL := "https://dex.example.com"

	// Store an expired token
	expiredToken := &oauth2.Token{
		AccessToken: "expired-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(-1 * time.Hour), // Expired 1 hour ago
	}

	if err := store.StoreToken(serverURL, issuerURL, expiredToken); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// Should not return expired token
	storedToken := store.GetToken(serverURL)
	if storedToken != nil {
		t.Error("Expected nil for expired token, got a token")
	}
}

func TestTokenStore_DeleteToken(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	serverURL := "https://muster.example.com"
	issuerURL := "https://dex.example.com"
	token := &oauth2.Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}

	// Store and then delete
	if err := store.StoreToken(serverURL, issuerURL, token); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	if err := store.DeleteToken(serverURL); err != nil {
		t.Fatalf("Failed to delete token: %v", err)
	}

	// Should return nil after deletion
	storedToken := store.GetToken(serverURL)
	if storedToken != nil {
		t.Error("Expected nil after deletion, got a token")
	}
}

func TestTokenStore_FileMode(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   true, // Enable file persistence
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	serverURL := "https://muster.example.com"
	issuerURL := "https://dex.example.com"
	token := &oauth2.Token{
		AccessToken: "persistent-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}

	// Store the token
	if err := store.StoreToken(serverURL, issuerURL, token); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// Check that a file was created
	files, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to read token directory: %v", err)
	}

	if len(files) != 1 {
		t.Errorf("Expected 1 token file, got %d", len(files))
	}

	if len(files) > 0 && filepath.Ext(files[0].Name()) != ".json" {
		t.Errorf("Expected .json file, got %s", files[0].Name())
	}

	// Create a new store instance and verify token is loaded from file
	store2, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   true,
	})
	if err != nil {
		t.Fatalf("Failed to create second token store: %v", err)
	}

	storedToken := store2.GetToken(serverURL)
	if storedToken == nil {
		t.Fatal("Expected to get token from file, got nil")
	}

	if storedToken.AccessToken != token.AccessToken {
		t.Errorf("Expected access token %q, got %q", token.AccessToken, storedToken.AccessToken)
	}
}

func TestTokenStore_HasValidToken(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	serverURL := "https://muster.example.com"
	issuerURL := "https://dex.example.com"

	// Initially should not have valid token
	if store.HasValidToken(serverURL) {
		t.Error("Expected no valid token initially")
	}

	// Store a valid token
	token := &oauth2.Token{
		AccessToken: "valid-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}

	if err := store.StoreToken(serverURL, issuerURL, token); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// Now should have valid token
	if !store.HasValidToken(serverURL) {
		t.Error("Expected valid token after storing")
	}
}

func TestTokenStore_Clear(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   true,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	// Store multiple tokens
	for i := 0; i < 3; i++ {
		serverURL := "https://muster" + string(rune('0'+i)) + ".example.com"
		token := &oauth2.Token{
			AccessToken: "token-" + string(rune('0'+i)),
			TokenType:   "Bearer",
			Expiry:      time.Now().Add(1 * time.Hour),
		}
		if err := store.StoreToken(serverURL, "https://dex.example.com", token); err != nil {
			t.Fatalf("Failed to store token: %v", err)
		}
	}

	// Clear all tokens
	if err := store.Clear(); err != nil {
		t.Fatalf("Failed to clear tokens: %v", err)
	}

	// Verify all tokens are gone (in-memory)
	for i := 0; i < 3; i++ {
		serverURL := "https://muster" + string(rune('0'+i)) + ".example.com"
		if store.HasValidToken(serverURL) {
			t.Errorf("Expected no token for %s after clear", serverURL)
		}
	}

	// Verify files are deleted
	files, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to read token directory: %v", err)
	}

	jsonFiles := 0
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".json" {
			jsonFiles++
		}
	}

	if jsonFiles != 0 {
		t.Errorf("Expected 0 token files after clear, got %d", jsonFiles)
	}
}

func TestStoredToken_ToOAuth2Token(t *testing.T) {
	expiry := time.Now().Add(1 * time.Hour)
	storedToken := &StoredToken{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		TokenType:    "Bearer",
		Expiry:       expiry,
		IDToken:      "id-token",
	}

	oauth2Token := storedToken.ToOAuth2Token()

	if oauth2Token.AccessToken != storedToken.AccessToken {
		t.Errorf("Expected access token %q, got %q", storedToken.AccessToken, oauth2Token.AccessToken)
	}

	if oauth2Token.RefreshToken != storedToken.RefreshToken {
		t.Errorf("Expected refresh token %q, got %q", storedToken.RefreshToken, oauth2Token.RefreshToken)
	}

	if oauth2Token.TokenType != storedToken.TokenType {
		t.Errorf("Expected token type %q, got %q", storedToken.TokenType, oauth2Token.TokenType)
	}

	// Check ID token in extra
	idToken := oauth2Token.Extra("id_token")
	if idToken == nil {
		t.Error("Expected id_token in Extra, got nil")
	} else if idTokenStr, ok := idToken.(string); !ok || idTokenStr != storedToken.IDToken {
		t.Errorf("Expected id_token %q, got %v", storedToken.IDToken, idToken)
	}
}
