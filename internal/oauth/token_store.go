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

// TokenStorer is the interface for OAuth token storage. Implementations must
// be safe for concurrent use. The aggregator-side OAuth proxy stores tokens
// here after successful authentication flows.
type TokenStorer interface {
	// Store saves a token indexed by the given key, recording userID for
	// reverse-lookup operations (e.g., "sign out everywhere").
	Store(key TokenKey, token *pkgoauth.Token, userID string)

	// Get retrieves a token by exact key. Returns nil if not found or expired.
	Get(key TokenKey) *pkgoauth.Token

	// GetByIssuer finds a token for the given session and issuer, regardless
	// of scope. Returns nil if not found or expired.
	GetByIssuer(sessionID, issuer string) *pkgoauth.Token

	// GetAllForSession returns all valid (non-expired) tokens for a session.
	GetAllForSession(sessionID string) map[TokenKey]*pkgoauth.Token

	// Delete removes a single token by key.
	Delete(key TokenKey)

	// DeleteByUser removes all tokens for a user across all sessions.
	DeleteByUser(userID string)

	// DeleteByUserAndIssuer removes the user's tokens for one issuer across
	// all sessions, including a subject-scoped grant: signing out of a
	// server as the person disconnects every session of that person.
	DeleteByUserAndIssuer(userID, issuer string)

	// GetByIssuerIncludingExpired is GetByIssuer without the expiry filter:
	// it returns the session's token for the issuer even when it has expired,
	// so a refresh path can redeem the refresh token it carries. Tokens with
	// an access token are preferred, then the one that expires last. Returns
	// nil when the session holds nothing for the issuer.
	GetByIssuerIncludingExpired(sessionID, issuer string) *pkgoauth.Token

	// ReplaceByUserAndIssuer overwrites the user's tokens for one issuer in
	// every session that holds one -- the subject-scoped grant included --
	// with token, keeping each entry's key. It reports how many entries were
	// replaced. Used after a refresh so every session of the person sees the
	// rotated token and nobody redeems the old refresh token again.
	ReplaceByUserAndIssuer(userID, issuer string, token *pkgoauth.Token) int

	// DeleteBySession removes all tokens for a session.
	DeleteBySession(sessionID string)

	// DeleteByIssuer removes all tokens for a session+issuer combination.
	DeleteByIssuer(sessionID, issuer string)

	// Count returns the total number of tokens in the store.
	Count() int

	// Stop releases resources (background goroutines, connections, etc.).
	Stop()
}

// refreshableTokenRetention is how long an expired token that still carries a
// refresh token stays in the in-memory store. The refresh path redeems the
// refresh token on the next lookup, so sweeping such an entry as soon as its
// access token expires would end a grant that could have lived on (GitHub's
// refresh tokens are good for six months). Matches DefaultTokenStoreTTL, the
// Valkey key TTL that bounds the same entries there.
const refreshableTokenRetention = 30 * 24 * time.Hour

// preferToken reports whether candidate is the better token to return for an
// issuer when several entries match: one with an access token beats one
// without, then the one that expires last (a token without expiry counts as
// expiring last).
func preferToken(current, candidate *pkgoauth.Token) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	if (candidate.AccessToken != "") != (current.AccessToken != "") {
		return candidate.AccessToken != ""
	}
	if candidate.ExpiresAt.IsZero() {
		return !current.ExpiresAt.IsZero()
	}
	return !current.ExpiresAt.IsZero() && candidate.ExpiresAt.After(current.ExpiresAt)
}

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
//
// When multiple tokens match (e.g., an ID-only token from SetSessionCreationHandler
// and a full token from a downstream OAuth callback), tokens with an AccessToken
// are preferred. This prevents non-deterministic map iteration from returning an
// ID-only token that would cause DynamicAuthClient to report ErrNoToken.
func (ts *TokenStore) GetByIssuer(sessionID, issuer string) *pkgoauth.Token {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	var fallback *pkgoauth.Token
	for key, entry := range ts.tokens {
		if key.SessionID == sessionID && key.Issuer == issuer {
			if !entry.token.IsExpiredWithMargin(tokenExpiryMargin) {
				if entry.token.AccessToken != "" {
					return entry.token
				}
				fallback = entry.token
			}
		}
	}
	return fallback
}

// GetByIssuerIncludingExpired returns the session's token for the issuer
// regardless of expiry, see TokenStorer.
func (ts *TokenStore) GetByIssuerIncludingExpired(sessionID, issuer string) *pkgoauth.Token {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	var best *pkgoauth.Token
	for key, entry := range ts.tokens {
		if key.SessionID == sessionID && key.Issuer == issuer && preferToken(best, entry.token) {
			best = entry.token
		}
	}
	return best
}

// ReplaceByUserAndIssuer overwrites the user's tokens for one issuer in every
// session that holds one, see TokenStorer.
func (ts *TokenStore) ReplaceByUserAndIssuer(userID, issuer string, token *pkgoauth.Token) int {
	if token == nil {
		return 0
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()

	token.SetExpiresAtFromExpiresIn()
	count := 0
	for key, entry := range ts.tokens {
		if entry.userID == userID && key.Issuer == issuer {
			replacement := *token
			entry.token = &replacement
			count++
		}
	}
	logging.Debug("OAuth", "Replaced %d tokens for user=%s issuer=%s", count, logging.TruncateIdentifier(userID), issuer)
	return count
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
// DeleteByUserAndIssuer removes the user's tokens for one issuer across all
// sessions.
func (ts *TokenStore) DeleteByUserAndIssuer(userID, issuer string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	count := 0
	for key, entry := range ts.tokens {
		if entry.userID == userID && key.Issuer == issuer {
			delete(ts.tokens, key)
			count++
		}
	}
	logging.Debug("OAuth", "Deleted %d tokens for user=%s issuer=%s", count, logging.TruncateIdentifier(userID), issuer)
}

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

// cleanup removes expired tokens from the store. A token that still carries
// a refresh token is refreshable, not dead: it is kept for
// refreshableTokenRetention past its expiry so the refresh path can redeem
// it on the next lookup.
func (ts *TokenStore) cleanup() {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	count := 0
	for key, entry := range ts.tokens {
		if !entry.token.IsExpiredWithMargin(0) {
			continue
		}
		if entry.token.RefreshToken != "" && !entry.token.IsExpiredWithMargin(-refreshableTokenRetention) {
			continue
		}
		delete(ts.tokens, key)
		count++
	}

	if count > 0 {
		logging.Debug("OAuth", "Cleaned up %d expired tokens", count)
	}
}
