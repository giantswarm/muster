package api

import (
	"sync"

	"github.com/giantswarm/muster/pkg/logging"
)

// IDTokenCache provides session-scoped caching of ID tokens.
//
// This bridges the gap between the HTTP middleware (which resolves fresh ID
// tokens from Valkey on every authenticated request) and background closures
// like headerFunc (which run outside the request lifecycle and cannot access
// the request context).
//
// The middleware writes the latest ID token into the cache on every request.
// Background token-resolution functions read from the cache when the request
// context is unavailable (e.g., context.Background()).
type IDTokenCache interface {
	// Store caches an ID token for the given session, replacing any previous value.
	Store(sessionID, idToken string)

	// Get retrieves the cached ID token for the given session.
	// Returns empty string if no token is cached for the session.
	Get(sessionID string) string

	// Delete removes the cached ID token for the given session.
	// This should be called during session revocation / logout.
	Delete(sessionID string)
}

var (
	idTokenCacheHandler IDTokenCache
	idTokenCacheMutex   sync.RWMutex
)

// RegisterIDTokenCache registers the ID token cache implementation.
// This cache provides session-scoped ID token storage that bridges the HTTP
// middleware (which has fresh tokens) and background closures (which need them).
//
// The registration is thread-safe and should be called during system initialization.
// Only one cache can be registered at a time; subsequent registrations replace
// the previous one.
//
// Thread-safe: Yes, protected by idTokenCacheMutex.
func RegisterIDTokenCache(h IDTokenCache) {
	idTokenCacheMutex.Lock()
	defer idTokenCacheMutex.Unlock()
	logging.Debug("API", "Registering ID token cache handler: %v", h != nil)
	idTokenCacheHandler = h
}

// GetIDTokenCache returns the registered ID token cache.
//
// Returns nil if no cache has been registered yet. Callers should always
// check for nil before using the returned cache.
//
// Thread-safe: Yes, protected by idTokenCacheMutex read lock.
func GetIDTokenCache() IDTokenCache {
	idTokenCacheMutex.RLock()
	defer idTokenCacheMutex.RUnlock()
	return idTokenCacheHandler
}
