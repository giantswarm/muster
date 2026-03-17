package server

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

const (
	// sweepInterval is how often the background goroutine removes expired entries.
	sweepInterval = 5 * time.Minute

	// defaultTokenTTL is applied when the token's exp claim cannot be parsed.
	defaultTokenTTL = 1 * time.Hour
)

// cacheEntry pairs a token with its expiry time for TTL-based eviction.
type cacheEntry struct {
	token     string
	expiresAt time.Time
}

// SessionIDTokenCache implements api.IDTokenCache with a typed map and RWMutex
// for concurrent-safe session-scoped ID token storage.
//
// The HTTP middleware (createAccessTokenInjectorMiddleware) stores the latest
// ID token on every authenticated request. Background closures like headerFunc
// read from this cache when they cannot access the request context (because
// they run with context.Background()).
//
// Entries are automatically evicted:
//   - Lazily on Get: expired entries return empty string.
//   - Proactively by a background goroutine that sweeps every 5 minutes.
//   - Explicitly via Delete on session revocation.
//
// Call Close to stop the background sweeper when the cache is no longer needed.
type SessionIDTokenCache struct {
	mu        sync.RWMutex
	tokens    map[string]cacheEntry
	stopCh    chan struct{}
	closeOnce sync.Once
}

// NewSessionIDTokenCache creates a new SessionIDTokenCache, registers it
// with the API layer, and starts a background sweeper goroutine.
func NewSessionIDTokenCache() *SessionIDTokenCache {
	cache := &SessionIDTokenCache{
		tokens: make(map[string]cacheEntry),
		stopCh: make(chan struct{}),
	}
	api.RegisterIDTokenCache(cache)
	go cache.sweepLoop()
	logging.Info("IDTokenCache", "Session-scoped ID token cache initialized")
	return cache
}

// Close stops the background sweeper goroutine. Safe to call multiple times.
func (c *SessionIDTokenCache) Close() {
	c.closeOnce.Do(func() { close(c.stopCh) })
}

// Store caches an ID token for the given session, replacing any previous value.
// The token's JWT exp claim is parsed to determine the eviction time; if the
// claim is missing or unparseable, a default TTL of 1 hour is used.
func (c *SessionIDTokenCache) Store(sessionID, idToken string) {
	expiresAt := extractTokenExpiry(idToken)
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(defaultTokenTTL)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[sessionID] = cacheEntry{token: idToken, expiresAt: expiresAt}
}

// Get retrieves the cached ID token for the given session.
// Returns empty string if no token is cached or if the cached token has expired.
func (c *SessionIDTokenCache) Get(sessionID string) string {
	c.mu.RLock()
	entry, ok := c.tokens[sessionID]
	c.mu.RUnlock()
	if !ok {
		return ""
	}
	if time.Now().After(entry.expiresAt) {
		return ""
	}
	return entry.token
}

// Delete removes the cached ID token for the given session.
func (c *SessionIDTokenCache) Delete(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tokens, sessionID)
	logging.Debug("IDTokenCache", "Deleted cached ID token for session %s",
		logging.TruncateIdentifier(sessionID))
}

// sweepLoop runs in a background goroutine, periodically removing expired entries.
func (c *SessionIDTokenCache) sweepLoop() {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.sweepExpired()
		case <-c.stopCh:
			return
		}
	}
}

// sweepExpired removes all expired entries from the cache.
func (c *SessionIDTokenCache) sweepExpired() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	swept := 0
	for sessionID, entry := range c.tokens {
		if now.After(entry.expiresAt) {
			delete(c.tokens, sessionID)
			swept++
		}
	}
	if swept > 0 {
		logging.Debug("IDTokenCache", "Swept %d expired entries", swept)
	}
}

// extractTokenExpiry parses the JWT payload to extract the exp claim.
// Returns zero time if the token is malformed or the exp claim is absent.
func extractTokenExpiry(idToken string) time.Time {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return time.Time{}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(parts[1])
		if err != nil {
			return time.Time{}
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}
