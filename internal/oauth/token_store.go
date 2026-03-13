package oauth

import (
	"sync"
	"time"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/pkg/logging"
)

// tokenExpiryMargin is the margin added when checking token expiration.
// This accounts for clock skew between systems and network latency.
const tokenExpiryMargin = 30 * time.Second

// tokenEntry wraps a token with its owning user ID for reverse-lookup by user.
type tokenEntry struct {
	token  *pkgoauth.Token
	userID string
}

// TokenStore provides thread-safe in-memory storage for OAuth tokens.
// Tokens are indexed by session ID (token family), issuer, and scope to support
// per-login-session isolation. Each entry also records the owning user ID to
// support bulk operations like "sign out everywhere".
//
// IMPORTANT: TokenStore starts a background goroutine for cleanup. Callers MUST
// call Stop() when done to prevent goroutine leaks. Typically this is done via
// defer after creating the store, or in a shutdown hook for long-lived stores.
type TokenStore struct {
	mu     sync.RWMutex
	tokens map[TokenKey]*tokenEntry

	// Cleanup configuration
	cleanupInterval time.Duration
	stopCleanup     chan struct{}
}

// NewTokenStore creates a new in-memory token store.
// It starts a background goroutine for periodic cleanup of expired tokens.
func NewTokenStore() *TokenStore {
	ts := &TokenStore{
		tokens:          make(map[TokenKey]*tokenEntry),
		cleanupInterval: 5 * time.Minute,
		stopCleanup:     make(chan struct{}),
	}

	// Start background cleanup goroutine
	go ts.cleanupLoop()

	return ts
}

// Store saves a token in the store, indexed by the given key.
// The userID is stored alongside for reverse-lookup by user (e.g., "sign out everywhere").
func (ts *TokenStore) Store(key TokenKey, token *pkgoauth.Token, userID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	token.SetExpiresAtFromExpiresIn()

	ts.tokens[key] = &tokenEntry{token: token, userID: userID}
	logging.Debug("OAuth", "Stored token for session=%s issuer=%s scope=%s (expires: %v)",
		logging.TruncateIdentifier(key.SessionID), key.Issuer, key.Scope, token.ExpiresAt)
}

// Get retrieves a token from the store by key.
// Returns nil if the token doesn't exist or has expired.
func (ts *TokenStore) Get(key TokenKey) *pkgoauth.Token {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	entry, exists := ts.tokens[key]
	if !exists {
		return nil
	}

	if entry.token.IsExpiredWithMargin(tokenExpiryMargin) {
		logging.Debug("OAuth", "Token expired for session=%s issuer=%s", logging.TruncateIdentifier(key.SessionID), key.Issuer)
		return nil
	}

	return entry.token
}

// GetByIssuer finds a token for the given session and issuer, regardless of scope.
// This enables SSO when the exact scope doesn't match but the issuer does.
func (ts *TokenStore) GetByIssuer(sessionID, issuer string) *pkgoauth.Token {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	for key, entry := range ts.tokens {
		if key.SessionID == sessionID && key.Issuer == issuer {
			if !entry.token.IsExpiredWithMargin(tokenExpiryMargin) {
				return entry.token
			}
		}
	}
	return nil
}

// GetTokenKeyByIssuer finds the token key for a given session and issuer.
func (ts *TokenStore) GetTokenKeyByIssuer(sessionID, issuer string) *TokenKey {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	for key, entry := range ts.tokens {
		if key.SessionID == sessionID && key.Issuer == issuer {
			if !entry.token.IsExpiredWithMargin(tokenExpiryMargin) {
				keyCopy := key
				return &keyCopy
			}
		}
	}
	return nil
}

// GetAllForSession returns all valid tokens for a session.
func (ts *TokenStore) GetAllForSession(sessionID string) map[TokenKey]*pkgoauth.Token {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	result := make(map[TokenKey]*pkgoauth.Token)
	for key, entry := range ts.tokens {
		if key.SessionID == sessionID && !entry.token.IsExpiredWithMargin(tokenExpiryMargin) {
			result[key] = entry.token
		}
	}
	return result
}

// GetAllForUser returns all valid tokens for a user across all sessions.
// This iterates all entries and filters by the stored user ID.
func (ts *TokenStore) GetAllForUser(userID string) map[TokenKey]*pkgoauth.Token {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	result := make(map[TokenKey]*pkgoauth.Token)
	for key, entry := range ts.tokens {
		if entry.userID == userID && !entry.token.IsExpiredWithMargin(tokenExpiryMargin) {
			result[key] = entry.token
		}
	}
	return result
}

// Delete removes a token from the store.
func (ts *TokenStore) Delete(key TokenKey) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	delete(ts.tokens, key)
	logging.Debug("OAuth", "Deleted token for session=%s issuer=%s", logging.TruncateIdentifier(key.SessionID), key.Issuer)
}

// DeleteByUser removes all tokens for a given user across all sessions.
// This is used during "sign out everywhere" to clear all server-side token state.
func (ts *TokenStore) DeleteByUser(userID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	count := 0
	for key, entry := range ts.tokens {
		if entry.userID == userID {
			delete(ts.tokens, key)
			count++
		}
	}
	logging.Debug("OAuth", "Deleted %d tokens for user=%s", count, logging.TruncateIdentifier(userID))
}

// DeleteBySession removes all tokens for a given session.
// This is used during per-session logout via token family revocation.
func (ts *TokenStore) DeleteBySession(sessionID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	count := 0
	for key := range ts.tokens {
		if key.SessionID == sessionID {
			delete(ts.tokens, key)
			count++
		}
	}
	logging.Debug("OAuth", "Deleted %d tokens for session=%s", count, logging.TruncateIdentifier(sessionID))
}

// DeleteByIssuer removes all tokens for a given session and issuer.
// This is used to clear invalid/expired tokens before requesting fresh authentication.
func (ts *TokenStore) DeleteByIssuer(sessionID, issuer string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	count := 0
	for key := range ts.tokens {
		if key.SessionID == sessionID && key.Issuer == issuer {
			delete(ts.tokens, key)
			count++
		}
	}
	logging.Debug("OAuth", "Deleted %d tokens for session=%s issuer=%s", count, logging.TruncateIdentifier(sessionID), issuer)
}

// Count returns the number of tokens in the store.
func (ts *TokenStore) Count() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.tokens)
}

// Stop stops the background cleanup goroutine.
func (ts *TokenStore) Stop() {
	close(ts.stopCleanup)
}

// cleanupLoop periodically removes expired tokens from the store.
func (ts *TokenStore) cleanupLoop() {
	ticker := time.NewTicker(ts.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ts.cleanup()
		case <-ts.stopCleanup:
			return
		}
	}
}

// cleanup removes all expired tokens from the store.
func (ts *TokenStore) cleanup() {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	count := 0
	for key, entry := range ts.tokens {
		if entry.token.IsExpiredWithMargin(0) {
			delete(ts.tokens, key)
			count++
		}
	}

	if count > 0 {
		logging.Debug("OAuth", "Cleaned up %d expired tokens", count)
	}
}
