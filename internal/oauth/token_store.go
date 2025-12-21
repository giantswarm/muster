package oauth

import (
	"sync"
	"time"

	"muster/pkg/logging"
)

// tokenExpiryMargin is the margin added when checking token expiration.
// This accounts for clock skew between systems and network latency.
const tokenExpiryMargin = 30 * time.Second

// TokenStore provides thread-safe in-memory storage for OAuth tokens.
// Tokens are indexed by session ID, issuer, and scope to support SSO.
type TokenStore struct {
	mu     sync.RWMutex
	tokens map[TokenKey]*Token

	// Cleanup configuration
	cleanupInterval time.Duration
	stopCleanup     chan struct{}
}

// NewTokenStore creates a new in-memory token store.
// It starts a background goroutine for periodic cleanup of expired tokens.
func NewTokenStore() *TokenStore {
	ts := &TokenStore{
		tokens:          make(map[TokenKey]*Token),
		cleanupInterval: 5 * time.Minute,
		stopCleanup:     make(chan struct{}),
	}

	// Start background cleanup goroutine
	go ts.cleanupLoop()

	return ts
}

// Store saves a token in the store, indexed by the given key.
func (ts *TokenStore) Store(key TokenKey, token *Token) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Calculate expiration time if not set
	if token.ExpiresAt.IsZero() && token.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}

	ts.tokens[key] = token
	logging.Debug("OAuth", "Stored token for session=%s issuer=%s scope=%s (expires: %v)",
		key.SessionID, key.Issuer, key.Scope, token.ExpiresAt)
}

// Get retrieves a token from the store by key.
// Returns nil if the token doesn't exist or has expired.
func (ts *TokenStore) Get(key TokenKey) *Token {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	token, exists := ts.tokens[key]
	if !exists {
		return nil
	}

	// Check if token is expired (with margin for clock skew)
	if token.IsExpired(tokenExpiryMargin) {
		logging.Debug("OAuth", "Token expired for session=%s issuer=%s", key.SessionID, key.Issuer)
		return nil
	}

	return token
}

// GetByIssuer finds a token for the given session and issuer, regardless of scope.
// This enables SSO when the exact scope doesn't match but the issuer does.
func (ts *TokenStore) GetByIssuer(sessionID, issuer string) *Token {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	for key, token := range ts.tokens {
		if key.SessionID == sessionID && key.Issuer == issuer {
			if !token.IsExpired(tokenExpiryMargin) {
				return token
			}
		}
	}
	return nil
}

// Delete removes a token from the store.
func (ts *TokenStore) Delete(key TokenKey) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	delete(ts.tokens, key)
	logging.Debug("OAuth", "Deleted token for session=%s issuer=%s", key.SessionID, key.Issuer)
}

// DeleteBySession removes all tokens for a given session.
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
	logging.Debug("OAuth", "Deleted %d tokens for session=%s", count, sessionID)
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
	for key, token := range ts.tokens {
		if token.IsExpired(0) {
			delete(ts.tokens, key)
			count++
		}
	}

	if count > 0 {
		logging.Debug("OAuth", "Cleaned up %d expired tokens", count)
	}
}
