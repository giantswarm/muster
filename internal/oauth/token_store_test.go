package oauth

import (
	"testing"
	"time"
)

func TestTokenStore_StoreAndGet(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	key := TokenKey{
		SessionID: "session-123",
		Issuer:    "https://auth.example.com",
		Scope:     "openid profile",
	}

	token := &Token{
		AccessToken: "access-token-abc",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       "openid profile",
		Issuer:      "https://auth.example.com",
	}

	// Store the token
	ts.Store(key, token)

	// Retrieve the token
	retrieved := ts.Get(key)
	if retrieved == nil {
		t.Fatal("Expected to retrieve stored token, got nil")
	}

	if retrieved.AccessToken != token.AccessToken {
		t.Errorf("Expected access token %q, got %q", token.AccessToken, retrieved.AccessToken)
	}

	if retrieved.TokenType != token.TokenType {
		t.Errorf("Expected token type %q, got %q", token.TokenType, retrieved.TokenType)
	}
}

func TestTokenStore_GetNonExistent(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	key := TokenKey{
		SessionID: "non-existent",
		Issuer:    "https://auth.example.com",
		Scope:     "openid",
	}

	retrieved := ts.Get(key)
	if retrieved != nil {
		t.Errorf("Expected nil for non-existent token, got %v", retrieved)
	}
}

func TestTokenStore_GetExpiredToken(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	key := TokenKey{
		SessionID: "session-123",
		Issuer:    "https://auth.example.com",
		Scope:     "openid",
	}

	// Create an expired token
	token := &Token{
		AccessToken: "expired-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(-time.Hour), // Expired an hour ago
		Issuer:      "https://auth.example.com",
	}

	ts.Store(key, token)

	// Should return nil for expired token
	retrieved := ts.Get(key)
	if retrieved != nil {
		t.Errorf("Expected nil for expired token, got %v", retrieved)
	}
}

func TestTokenStore_GetByIssuer(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	// Store a token
	key := TokenKey{
		SessionID: "session-123",
		Issuer:    "https://auth.example.com",
		Scope:     "openid profile email",
	}

	token := &Token{
		AccessToken: "access-token-xyz",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       "openid profile email",
		Issuer:      "https://auth.example.com",
	}

	ts.Store(key, token)

	// Retrieve by issuer (different scope should still find it)
	retrieved := ts.GetByIssuer("session-123", "https://auth.example.com")
	if retrieved == nil {
		t.Fatal("Expected to retrieve token by issuer, got nil")
	}

	if retrieved.AccessToken != token.AccessToken {
		t.Errorf("Expected access token %q, got %q", token.AccessToken, retrieved.AccessToken)
	}
}

func TestTokenStore_GetByIssuerNotFound(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	// Store a token for different session
	key := TokenKey{
		SessionID: "session-123",
		Issuer:    "https://auth.example.com",
		Scope:     "openid",
	}

	token := &Token{
		AccessToken: "access-token-xyz",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Issuer:      "https://auth.example.com",
	}

	ts.Store(key, token)

	// Try to retrieve with different session
	retrieved := ts.GetByIssuer("different-session", "https://auth.example.com")
	if retrieved != nil {
		t.Errorf("Expected nil for different session, got %v", retrieved)
	}

	// Try to retrieve with different issuer
	retrieved = ts.GetByIssuer("session-123", "https://different-issuer.com")
	if retrieved != nil {
		t.Errorf("Expected nil for different issuer, got %v", retrieved)
	}
}

func TestTokenStore_Delete(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	key := TokenKey{
		SessionID: "session-123",
		Issuer:    "https://auth.example.com",
		Scope:     "openid",
	}

	token := &Token{
		AccessToken: "access-token-xyz",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Issuer:      "https://auth.example.com",
	}

	ts.Store(key, token)

	// Verify token exists
	if ts.Get(key) == nil {
		t.Fatal("Token should exist before deletion")
	}

	// Delete the token
	ts.Delete(key)

	// Verify token is deleted
	if ts.Get(key) != nil {
		t.Error("Token should be deleted")
	}
}

func TestTokenStore_DeleteBySession(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	sessionID := "session-to-delete"

	// Store multiple tokens for the same session
	for i, issuer := range []string{"https://issuer1.com", "https://issuer2.com", "https://issuer3.com"} {
		key := TokenKey{
			SessionID: sessionID,
			Issuer:    issuer,
			Scope:     "openid",
		}
		token := &Token{
			AccessToken: "token-" + string(rune('a'+i)),
			TokenType:   "Bearer",
			ExpiresIn:   3600,
			Issuer:      issuer,
		}
		ts.Store(key, token)
	}

	// Store a token for different session
	otherKey := TokenKey{
		SessionID: "other-session",
		Issuer:    "https://issuer1.com",
		Scope:     "openid",
	}
	ts.Store(otherKey, &Token{
		AccessToken: "other-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Issuer:      "https://issuer1.com",
	})

	// Verify we have 4 tokens
	if ts.Count() != 4 {
		t.Errorf("Expected 4 tokens, got %d", ts.Count())
	}

	// Delete all tokens for the session
	ts.DeleteBySession(sessionID)

	// Verify only 1 token remains (the other session)
	if ts.Count() != 1 {
		t.Errorf("Expected 1 token after deletion, got %d", ts.Count())
	}

	// Verify the remaining token is from the other session
	if ts.Get(otherKey) == nil {
		t.Error("Token from other session should still exist")
	}
}

func TestTokenStore_Count(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	// Initially empty
	if ts.Count() != 0 {
		t.Errorf("Expected 0 tokens initially, got %d", ts.Count())
	}

	// Add tokens
	for i := 0; i < 5; i++ {
		key := TokenKey{
			SessionID: "session",
			Issuer:    "issuer",
			Scope:     string(rune('a' + i)),
		}
		ts.Store(key, &Token{AccessToken: "token", ExpiresIn: 3600})
	}

	if ts.Count() != 5 {
		t.Errorf("Expected 5 tokens, got %d", ts.Count())
	}
}

func TestToken_IsExpired(t *testing.T) {
	tests := []struct {
		name     string
		token    Token
		margin   time.Duration
		expected bool
	}{
		{
			name:     "not expired",
			token:    Token{ExpiresAt: time.Now().Add(time.Hour)},
			margin:   0,
			expected: false,
		},
		{
			name:     "expired",
			token:    Token{ExpiresAt: time.Now().Add(-time.Hour)},
			margin:   0,
			expected: true,
		},
		{
			name:     "not expired but within margin",
			token:    Token{ExpiresAt: time.Now().Add(10 * time.Second)},
			margin:   30 * time.Second,
			expected: true,
		},
		{
			name:     "no expiration set",
			token:    Token{},
			margin:   0,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.token.IsExpired(tc.margin)
			if result != tc.expected {
				t.Errorf("Expected IsExpired to be %v, got %v", tc.expected, result)
			}
		})
	}
}
