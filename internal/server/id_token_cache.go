package server

import (
	"sync"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// SessionIDTokenCache implements api.IDTokenCache with a typed map and RWMutex
// for concurrent-safe session-scoped ID token storage.
//
// The HTTP middleware (createAccessTokenInjectorMiddleware) stores the latest
// ID token on every authenticated request. Background closures like headerFunc
// read from this cache when they cannot access the request context (because
// they run with context.Background()).
//
// This is the minimal bridge that lets token-forwarding headerFunc closures
// resolve fresh ID tokens after the initial token expires (~30 min), without
// architectural changes to the token store hierarchy.
//
// TODO: Consider adding TTL-based eviction keyed to each token's exp claim
// so that entries are cleaned up even if the session revocation callback
// does not fire (e.g., after a process restart).
type SessionIDTokenCache struct {
	mu     sync.RWMutex
	tokens map[string]string
}

// NewSessionIDTokenCache creates a new SessionIDTokenCache and registers it
// with the API layer via api.RegisterIDTokenCache.
func NewSessionIDTokenCache() *SessionIDTokenCache {
	cache := &SessionIDTokenCache{
		tokens: make(map[string]string),
	}
	api.RegisterIDTokenCache(cache)
	logging.Info("IDTokenCache", "Session-scoped ID token cache initialized")
	return cache
}

// Store caches an ID token for the given session, replacing any previous value.
func (c *SessionIDTokenCache) Store(sessionID, idToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[sessionID] = idToken
}

// Get retrieves the cached ID token for the given session.
// Returns empty string if no token is cached.
func (c *SessionIDTokenCache) Get(sessionID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tokens[sessionID]
}

// Delete removes the cached ID token for the given session.
func (c *SessionIDTokenCache) Delete(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tokens, sessionID)
	logging.Debug("IDTokenCache", "Deleted cached ID token for session %s",
		logging.TruncateIdentifier(sessionID))
}
