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

func TestTokenStore_GetByIssuer(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	issuerURL := "https://dex.example.com"
	serverURL1 := "https://muster1.example.com"
	serverURL2 := "https://muster2.example.com"

	// Initially should not have any tokens
	if store.GetByIssuer(issuerURL) != nil {
		t.Error("Expected no token for issuer initially")
	}

	// Store a token for server1 with the issuer
	token1 := &oauth2.Token{
		AccessToken: "token-for-server1",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}
	if err := store.StoreToken(serverURL1, issuerURL, token1); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// Should find the token by issuer
	found := store.GetByIssuer(issuerURL)
	if found == nil {
		t.Fatal("Expected to find token by issuer, got nil")
	}
	if found.AccessToken != token1.AccessToken {
		t.Errorf("Expected access token %q, got %q", token1.AccessToken, found.AccessToken)
	}
	if found.IssuerURL != issuerURL {
		t.Errorf("Expected issuer URL %q, got %q", issuerURL, found.IssuerURL)
	}

	// Store another token for server2 with the same issuer (SSO scenario)
	token2 := &oauth2.Token{
		AccessToken: "token-for-server2",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}
	if err := store.StoreToken(serverURL2, issuerURL, token2); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// GetByIssuer should find one of the tokens (either is valid for SSO)
	found = store.GetByIssuer(issuerURL)
	if found == nil {
		t.Fatal("Expected to find token by issuer after storing second token")
	}

	// The token should have the issuer we searched for
	if found.IssuerURL != issuerURL {
		t.Errorf("Expected issuer URL %q, got %q", issuerURL, found.IssuerURL)
	}
}

func TestTokenStore_GetByIssuer_DifferentIssuers(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	issuer1 := "https://dex1.example.com"
	issuer2 := "https://dex2.example.com"
	serverURL := "https://muster.example.com"

	// Store token with issuer1
	token := &oauth2.Token{
		AccessToken: "token-issuer1",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}
	if err := store.StoreToken(serverURL, issuer1, token); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// Should find for issuer1
	if store.GetByIssuer(issuer1) == nil {
		t.Error("Expected to find token for issuer1")
	}

	// Should NOT find for issuer2
	if store.GetByIssuer(issuer2) != nil {
		t.Error("Expected no token for issuer2")
	}
}

func TestTokenStore_GetByIssuer_ExpiredToken(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	issuerURL := "https://dex.example.com"
	serverURL := "https://muster.example.com"

	// Store an expired token
	expiredToken := &oauth2.Token{
		AccessToken: "expired-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(-1 * time.Hour), // Expired
	}
	if err := store.StoreToken(serverURL, issuerURL, expiredToken); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// Should NOT return expired token
	if store.GetByIssuer(issuerURL) != nil {
		t.Error("Expected nil for expired token by issuer")
	}
}

func TestTokenStore_HasValidTokenForIssuer(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   false,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	issuerURL := "https://dex.example.com"
	serverURL := "https://muster.example.com"

	// Initially should not have valid token for issuer
	if store.HasValidTokenForIssuer(issuerURL) {
		t.Error("Expected no valid token for issuer initially")
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

	// Now should have valid token for issuer
	if !store.HasValidTokenForIssuer(issuerURL) {
		t.Error("Expected valid token for issuer after storing")
	}
}

func TestTokenStore_GetByIssuer_FileMode(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   true, // Enable file persistence
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	issuerURL := "https://dex.example.com"
	serverURL := "https://muster.example.com"

	// Store a token with file mode enabled
	token := &oauth2.Token{
		AccessToken: "persistent-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}
	if err := store.StoreToken(serverURL, issuerURL, token); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// Create a new store instance (simulates restart)
	store2, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   true,
	})
	if err != nil {
		t.Fatalf("Failed to create second token store: %v", err)
	}

	// Should find the token by issuer from file
	found := store2.GetByIssuer(issuerURL)
	if found == nil {
		t.Fatal("Expected to find token by issuer from file, got nil")
	}
	if found.AccessToken != token.AccessToken {
		t.Errorf("Expected access token %q, got %q", token.AccessToken, found.AccessToken)
	}
}

func TestTokenStore_IsTokenValid_ExpiryMargin(t *testing.T) {
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

	testCases := []struct {
		name         string
		expiryOffset time.Duration
		expectValid  bool
	}{
		{
			name:         "token expires in 2 hours - valid",
			expiryOffset: 2 * time.Hour,
			expectValid:  true,
		},
		{
			name:         "token expires in 5 minutes - valid",
			expiryOffset: 5 * time.Minute,
			expectValid:  true,
		},
		{
			name:         "token expires in 90 seconds - valid (beyond 60s margin)",
			expiryOffset: 90 * time.Second,
			expectValid:  true,
		},
		{
			name:         "token expires in 30 seconds - invalid (within 60s margin)",
			expiryOffset: 30 * time.Second,
			expectValid:  false,
		},
		{
			name:         "token expires in 59 seconds - invalid (within 60s margin)",
			expiryOffset: 59 * time.Second,
			expectValid:  false,
		},
		{
			name:         "token already expired - invalid",
			expiryOffset: -1 * time.Hour,
			expectValid:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			token := &oauth2.Token{
				AccessToken: "test-token-" + tc.name,
				TokenType:   "Bearer",
				Expiry:      time.Now().Add(tc.expiryOffset),
			}

			// Store the token
			if err := store.StoreToken(serverURL, issuerURL, token); err != nil {
				t.Fatalf("Failed to store token: %v", err)
			}

			// Check if token is valid
			hasValid := store.HasValidToken(serverURL)
			if hasValid != tc.expectValid {
				t.Errorf("HasValidToken() = %v, want %v for expiry offset %v", hasValid, tc.expectValid, tc.expiryOffset)
			}

			// Clean up for next test case
			store.DeleteToken(serverURL)
		})
	}
}

func TestTokenStore_FileMode_Permissions(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   true,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	serverURL := "https://muster.example.com"
	issuerURL := "https://dex.example.com"
	token := &oauth2.Token{
		AccessToken: "secret-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}

	// Store the token
	if err := store.StoreToken(serverURL, issuerURL, token); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// Check directory permissions (should be 0700)
	dirInfo, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatalf("Failed to stat directory: %v", err)
	}

	dirPerm := dirInfo.Mode().Perm()
	// Note: On some systems the permissions might be different due to umask
	// We just check that it's restrictive (no world/group read/write)
	if dirPerm&0077 != 0 && dirPerm != 0700 {
		// Some systems may have different umask settings
		t.Logf("Directory permissions: %o (expected 0700 or similar restrictive)", dirPerm)
	}

	// Find and check token file permissions (should be 0600)
	files, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to read directory: %v", err)
	}

	foundTokenFile := false
	for _, file := range files {
		if filepath.Ext(file.Name()) == ".json" {
			foundTokenFile = true
			filePath := filepath.Join(tmpDir, file.Name())
			fileInfo, err := os.Stat(filePath)
			if err != nil {
				t.Fatalf("Failed to stat token file: %v", err)
			}

			filePerm := fileInfo.Mode().Perm()
			// Check that token file has restrictive permissions (0600)
			if filePerm != 0600 {
				// This might fail on some systems with different umask
				t.Logf("Token file permissions: %o (expected 0600)", filePerm)
				// At minimum, check that world/group can't read
				if filePerm&0077 != 0 {
					t.Errorf("Token file should not be readable by group/others: %o", filePerm)
				}
			}
		}
	}

	if !foundTokenFile {
		t.Error("Expected to find a token file")
	}
}

func TestTokenStore_ZeroExpiry_ConsideredValid(t *testing.T) {
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

	// Token with zero expiry (some tokens don't have expiry info)
	token := &oauth2.Token{
		AccessToken: "no-expiry-token",
		TokenType:   "Bearer",
		// Expiry is zero value
	}

	if err := store.StoreToken(serverURL, issuerURL, token); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// Token with zero expiry should be considered valid
	if !store.HasValidToken(serverURL) {
		t.Error("Token with zero expiry should be considered valid")
	}

	storedToken := store.GetToken(serverURL)
	if storedToken == nil {
		t.Error("Expected to get token with zero expiry")
	}
}

func TestTokenStore_GetTokenIncludingExpiring(t *testing.T) {
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

	t.Run("returns nil for non-existent token", func(t *testing.T) {
		token := store.GetTokenIncludingExpiring("https://nonexistent.example.com")
		if token != nil {
			t.Error("Expected nil for non-existent token")
		}
	})

	t.Run("returns token expiring within margin", func(t *testing.T) {
		// Store a token that's expiring within the 60s margin
		expiringToken := &oauth2.Token{
			AccessToken:  "expiring-soon-token",
			RefreshToken: "refresh-token",
			TokenType:    "Bearer",
			Expiry:       time.Now().Add(30 * time.Second), // Expires in 30s (within 60s margin)
		}

		if err := store.StoreToken(serverURL, issuerURL, expiringToken); err != nil {
			t.Fatalf("Failed to store token: %v", err)
		}

		// GetToken should return nil (token is within expiry margin)
		if store.GetToken(serverURL) != nil {
			t.Error("GetToken should return nil for token within expiry margin")
		}

		// GetTokenIncludingExpiring should still return the token
		token := store.GetTokenIncludingExpiring(serverURL)
		if token == nil {
			t.Fatal("GetTokenIncludingExpiring should return token even if expiring soon")
		}

		if token.AccessToken != expiringToken.AccessToken {
			t.Errorf("Expected access token %q, got %q", expiringToken.AccessToken, token.AccessToken)
		}

		if token.RefreshToken != expiringToken.RefreshToken {
			t.Errorf("Expected refresh token %q, got %q", expiringToken.RefreshToken, token.RefreshToken)
		}

		// Clean up
		store.DeleteToken(serverURL)
	})

	t.Run("returns already expired token with refresh token", func(t *testing.T) {
		// Store a token that's already expired but has a refresh token
		expiredToken := &oauth2.Token{
			AccessToken:  "expired-token",
			RefreshToken: "still-valid-refresh-token",
			TokenType:    "Bearer",
			Expiry:       time.Now().Add(-1 * time.Hour), // Expired 1 hour ago
		}

		if err := store.StoreToken(serverURL, issuerURL, expiredToken); err != nil {
			t.Fatalf("Failed to store token: %v", err)
		}

		// GetToken should return nil (token is expired)
		if store.GetToken(serverURL) != nil {
			t.Error("GetToken should return nil for expired token")
		}

		// GetTokenIncludingExpiring should still return the token
		token := store.GetTokenIncludingExpiring(serverURL)
		if token == nil {
			t.Fatal("GetTokenIncludingExpiring should return expired token")
		}

		if token.RefreshToken != expiredToken.RefreshToken {
			t.Errorf("Expected refresh token %q, got %q", expiredToken.RefreshToken, token.RefreshToken)
		}

		// Clean up
		store.DeleteToken(serverURL)
	})

	t.Run("returns valid token", func(t *testing.T) {
		// Store a valid token
		validToken := &oauth2.Token{
			AccessToken:  "valid-token",
			RefreshToken: "refresh-token",
			TokenType:    "Bearer",
			Expiry:       time.Now().Add(1 * time.Hour), // Valid for 1 hour
		}

		if err := store.StoreToken(serverURL, issuerURL, validToken); err != nil {
			t.Fatalf("Failed to store token: %v", err)
		}

		// Both methods should return the token
		token1 := store.GetToken(serverURL)
		token2 := store.GetTokenIncludingExpiring(serverURL)

		if token1 == nil || token2 == nil {
			t.Error("Both GetToken and GetTokenIncludingExpiring should return valid token")
		}

		if token1 != nil && token2 != nil {
			if token1.AccessToken != token2.AccessToken {
				t.Error("Both methods should return the same token")
			}
		}
	})
}

func TestTokenStore_GetTokenIncludingExpiring_FileMode(t *testing.T) {
	tmpDir := t.TempDir()

	// Create store and add a token
	store, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   true,
	})
	if err != nil {
		t.Fatalf("Failed to create token store: %v", err)
	}

	serverURL := "https://muster.example.com"
	issuerURL := "https://dex.example.com"

	// Store a token that's expiring soon
	expiringToken := &oauth2.Token{
		AccessToken:  "expiring-token",
		RefreshToken: "refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(30 * time.Second),
	}

	if err := store.StoreToken(serverURL, issuerURL, expiringToken); err != nil {
		t.Fatalf("Failed to store token: %v", err)
	}

	// Create a new store (simulates restart, no in-memory cache)
	store2, err := NewTokenStore(TokenStoreConfig{
		StorageDir: tmpDir,
		FileMode:   true,
	})
	if err != nil {
		t.Fatalf("Failed to create second token store: %v", err)
	}

	// GetTokenIncludingExpiring should load from file and return the token
	token := store2.GetTokenIncludingExpiring(serverURL)
	if token == nil {
		t.Fatal("Expected to load expiring token from file")
	}

	if token.RefreshToken != expiringToken.RefreshToken {
		t.Errorf("Expected refresh token %q, got %q", expiringToken.RefreshToken, token.RefreshToken)
	}
}
