package oauth

import (
	"testing"
	"time"

	pkgoauth "muster/pkg/oauth"
)

func TestTokenStore_StoreAndGet(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	key := TokenKey{
		SessionID: "session-123",
		Issuer:    "https://auth.example.com",
		Scope:     "openid profile",
	}

	token := &pkgoauth.Token{
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
	token := &pkgoauth.Token{
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

	token := &pkgoauth.Token{
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

	token := &pkgoauth.Token{
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

	token := &pkgoauth.Token{
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
		token := &pkgoauth.Token{
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
	ts.Store(otherKey, &pkgoauth.Token{
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
		ts.Store(key, &pkgoauth.Token{AccessToken: "token", ExpiresIn: 3600})
	}

	if ts.Count() != 5 {
		t.Errorf("Expected 5 tokens, got %d", ts.Count())
	}
}

func TestTokenStore_DeleteByIssuer(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	sessionID := "session-123"
	issuerToDelete := "https://issuer-to-delete.com"
	issuerToKeep := "https://issuer-to-keep.com"

	// Store tokens for the same session with different issuers
	key1 := TokenKey{
		SessionID: sessionID,
		Issuer:    issuerToDelete,
		Scope:     "openid",
	}
	ts.Store(key1, &pkgoauth.Token{
		AccessToken: "token-1",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Issuer:      issuerToDelete,
	})

	key2 := TokenKey{
		SessionID: sessionID,
		Issuer:    issuerToDelete,
		Scope:     "profile", // Same issuer, different scope
	}
	ts.Store(key2, &pkgoauth.Token{
		AccessToken: "token-2",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Issuer:      issuerToDelete,
	})

	key3 := TokenKey{
		SessionID: sessionID,
		Issuer:    issuerToKeep,
		Scope:     "openid",
	}
	ts.Store(key3, &pkgoauth.Token{
		AccessToken: "token-3",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Issuer:      issuerToKeep,
	})

	// Store token for different session (should not be affected)
	key4 := TokenKey{
		SessionID: "other-session",
		Issuer:    issuerToDelete,
		Scope:     "openid",
	}
	ts.Store(key4, &pkgoauth.Token{
		AccessToken: "token-4",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Issuer:      issuerToDelete,
	})

	// Verify we have 4 tokens
	if ts.Count() != 4 {
		t.Errorf("Expected 4 tokens, got %d", ts.Count())
	}

	// Delete tokens for session-123 and issuerToDelete
	ts.DeleteByIssuer(sessionID, issuerToDelete)

	// Verify we have 2 tokens remaining
	if ts.Count() != 2 {
		t.Errorf("Expected 2 tokens after deletion, got %d", ts.Count())
	}

	// Verify the correct tokens were deleted
	if ts.Get(key1) != nil {
		t.Error("Token 1 should have been deleted")
	}
	if ts.Get(key2) != nil {
		t.Error("Token 2 should have been deleted")
	}

	// Verify the correct tokens remain
	if ts.Get(key3) == nil {
		t.Error("Token 3 (different issuer) should still exist")
	}
	if ts.Get(key4) == nil {
		t.Error("Token 4 (different session) should still exist")
	}
}

func TestTokenStore_DeleteByIssuer_NoMatch(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	// Store a token
	key := TokenKey{
		SessionID: "session-123",
		Issuer:    "https://auth.example.com",
		Scope:     "openid",
	}
	ts.Store(key, &pkgoauth.Token{
		AccessToken: "token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Issuer:      "https://auth.example.com",
	})

	// Try to delete with non-matching session
	ts.DeleteByIssuer("non-existent-session", "https://auth.example.com")

	// Token should still exist
	if ts.Count() != 1 {
		t.Errorf("Expected 1 token, got %d", ts.Count())
	}

	// Try to delete with non-matching issuer
	ts.DeleteByIssuer("session-123", "https://non-existent-issuer.com")

	// Token should still exist
	if ts.Count() != 1 {
		t.Errorf("Expected 1 token, got %d", ts.Count())
	}
}

func TestToken_IsExpired(t *testing.T) {
	tests := []struct {
		name     string
		token    pkgoauth.Token
		margin   time.Duration
		expected bool
	}{
		{
			name:     "not expired",
			token:    pkgoauth.Token{ExpiresAt: time.Now().Add(time.Hour)},
			margin:   0,
			expected: false,
		},
		{
			name:     "expired",
			token:    pkgoauth.Token{ExpiresAt: time.Now().Add(-time.Hour)},
			margin:   0,
			expected: true,
		},
		{
			name:     "not expired but within margin",
			token:    pkgoauth.Token{ExpiresAt: time.Now().Add(10 * time.Second)},
			margin:   30 * time.Second,
			expected: true,
		},
		{
			name:     "no expiration set",
			token:    pkgoauth.Token{},
			margin:   0,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.token.IsExpiredWithMargin(tc.margin)
			if result != tc.expected {
				t.Errorf("Expected IsExpiredWithMargin to be %v, got %v", tc.expected, result)
			}
		})
	}
}

// TestTokenStore_SessionIsolation verifies that tokens from different sessions
// are completely isolated. This is critical for multi-user security - users
// MUST NOT be able to access each other's OAuth tokens.
func TestTokenStore_SessionIsolation(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	// Simulate two different users with different session IDs
	user1Session := "uuid-user1-session-abc123"
	user2Session := "uuid-user2-session-def456"
	commonIssuer := "https://auth.example.com"
	commonScope := "openid profile"

	// User 1 stores their token
	user1Key := TokenKey{
		SessionID: user1Session,
		Issuer:    commonIssuer,
		Scope:     commonScope,
	}
	user1Token := &pkgoauth.Token{
		AccessToken: "user1-secret-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Issuer:      commonIssuer,
		Scope:       commonScope,
	}
	ts.Store(user1Key, user1Token)

	// User 2 stores their token (same issuer and scope, different session)
	user2Key := TokenKey{
		SessionID: user2Session,
		Issuer:    commonIssuer,
		Scope:     commonScope,
	}
	user2Token := &pkgoauth.Token{
		AccessToken: "user2-secret-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Issuer:      commonIssuer,
		Scope:       commonScope,
	}
	ts.Store(user2Key, user2Token)

	// CRITICAL SECURITY CHECK: User 1 should only get their own token
	retrievedUser1Token := ts.Get(user1Key)
	if retrievedUser1Token == nil {
		t.Fatal("User 1 should be able to retrieve their own token")
	}
	if retrievedUser1Token.AccessToken != "user1-secret-token" {
		t.Errorf("User 1 got wrong token: expected user1-secret-token, got %s", retrievedUser1Token.AccessToken)
	}

	// CRITICAL SECURITY CHECK: User 2 should only get their own token
	retrievedUser2Token := ts.Get(user2Key)
	if retrievedUser2Token == nil {
		t.Fatal("User 2 should be able to retrieve their own token")
	}
	if retrievedUser2Token.AccessToken != "user2-secret-token" {
		t.Errorf("User 2 got wrong token: expected user2-secret-token, got %s", retrievedUser2Token.AccessToken)
	}

	// CRITICAL SECURITY CHECK: User 1's session should not retrieve User 2's token
	wrongKey := TokenKey{
		SessionID: user1Session,
		Issuer:    commonIssuer,
		Scope:     "different-scope", // Even with same issuer, different scope = no token
	}
	if ts.Get(wrongKey) != nil {
		t.Error("Should not get token for non-matching scope")
	}

	// CRITICAL SECURITY CHECK: GetByIssuer respects session boundaries
	user1IssuerToken := ts.GetByIssuer(user1Session, commonIssuer)
	if user1IssuerToken == nil || user1IssuerToken.AccessToken != "user1-secret-token" {
		t.Error("GetByIssuer should return User 1's token for User 1's session")
	}

	user2IssuerToken := ts.GetByIssuer(user2Session, commonIssuer)
	if user2IssuerToken == nil || user2IssuerToken.AccessToken != "user2-secret-token" {
		t.Error("GetByIssuer should return User 2's token for User 2's session")
	}

	// CRITICAL: Verify token count (exactly 2 tokens, one per user)
	if ts.Count() != 2 {
		t.Errorf("Expected exactly 2 tokens (one per user), got %d", ts.Count())
	}
}

func TestTokenStore_Cleanup(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	// Store a token
	key := TokenKey{
		SessionID: "session-123",
		Issuer:    "https://auth.example.com",
		Scope:     "openid",
	}
	token := &pkgoauth.Token{
		AccessToken: "access-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Issuer:      "https://auth.example.com",
	}
	ts.Store(key, token)

	// Call cleanup directly - since no tokens are expired, nothing should happen
	ts.cleanup()

	// Token should still exist
	if ts.Get(key) == nil {
		t.Error("Non-expired token should still exist after cleanup")
	}
}

func TestTokenStore_CleanupExpiredTokens(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()

	// Store an expired token
	key := TokenKey{
		SessionID: "session-123",
		Issuer:    "https://auth.example.com",
		Scope:     "openid",
	}
	token := &pkgoauth.Token{
		AccessToken: "expired-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(-time.Hour), // Expired an hour ago
		Issuer:      "https://auth.example.com",
	}
	ts.Store(key, token)

	// Verify token was stored (but Get will return nil because it's expired)
	if ts.Count() != 1 {
		t.Errorf("Expected 1 token stored, got %d", ts.Count())
	}

	// Call cleanup - should remove the expired token
	ts.cleanup()

	// Token should be removed
	if ts.Count() != 0 {
		t.Errorf("Expected 0 tokens after cleanup, got %d", ts.Count())
	}
}
