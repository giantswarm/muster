package server

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/internal/api"
)

// newTestCache creates a SessionIDTokenCache and registers t.Cleanup handlers
// to stop the background sweeper and deregister the global API cache handler.
func newTestCache(t *testing.T) *SessionIDTokenCache {
	t.Helper()
	cache := NewSessionIDTokenCache()
	t.Cleanup(func() {
		cache.Close()
		api.RegisterIDTokenCache(nil)
	})
	return cache
}

func TestSessionIDTokenCache_StoreAndGet(t *testing.T) {
	cache := newTestCache(t)

	cache.Store("session-1", "token-aaa")
	assert.Equal(t, "token-aaa", cache.Get("session-1"))
}

func TestSessionIDTokenCache_GetUnknownSession(t *testing.T) {
	cache := newTestCache(t)

	assert.Equal(t, "", cache.Get("nonexistent"))
}

func TestSessionIDTokenCache_StoreOverwrites(t *testing.T) {
	cache := newTestCache(t)

	cache.Store("session-1", "old-token")
	cache.Store("session-1", "new-token")
	assert.Equal(t, "new-token", cache.Get("session-1"))
}

func TestSessionIDTokenCache_DeleteRemoves(t *testing.T) {
	cache := newTestCache(t)

	cache.Store("session-1", "token-aaa")
	cache.Delete("session-1")
	assert.Equal(t, "", cache.Get("session-1"))
}

func TestSessionIDTokenCache_DeleteNonexistent(t *testing.T) {
	cache := newTestCache(t)
	cache.Delete("nonexistent")
}

func TestSessionIDTokenCache_IsolatesSessions(t *testing.T) {
	cache := newTestCache(t)

	cache.Store("session-1", "token-1")
	cache.Store("session-2", "token-2")

	assert.Equal(t, "token-1", cache.Get("session-1"))
	assert.Equal(t, "token-2", cache.Get("session-2"))

	cache.Delete("session-1")
	assert.Equal(t, "", cache.Get("session-1"))
	assert.Equal(t, "token-2", cache.Get("session-2"))
}

func TestSessionIDTokenCache_ConcurrentAccess(t *testing.T) {
	cache := newTestCache(t)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sessionID := "session-concurrent"
			cache.Store(sessionID, "token-value")
			_ = cache.Get(sessionID)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, "token-value", cache.Get("session-concurrent"))
}

func TestSessionIDTokenCache_RegistersWithAPI(t *testing.T) {
	_ = newTestCache(t)

	retrieved := api.GetIDTokenCache()
	assert.NotNil(t, retrieved)
}

func TestGetIDTokenCache_ReturnsNilWhenNotRegistered(t *testing.T) {
	api.RegisterIDTokenCache(nil)
	assert.Nil(t, api.GetIDTokenCache())
}

func TestSessionIDTokenCache_ExpiredTokenReturnsEmpty(t *testing.T) {
	cache := newTestCache(t)

	// JWT with exp=1 (Jan 1 1970 00:00:01 UTC) -- already expired.
	// Header: {"alg":"RS256"}, Payload: {"exp":1}
	expiredToken := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjF9.sig"
	cache.Store("session-1", expiredToken)

	assert.Equal(t, "", cache.Get("session-1"))
}

func TestSessionIDTokenCache_ValidTokenReturnsToken(t *testing.T) {
	cache := newTestCache(t)

	// JWT with exp=9999999999 (year 2286) -- far future.
	// Header: {"alg":"RS256"}, Payload: {"exp":9999999999}
	futureToken := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjk5OTk5OTk5OTl9.sig"
	cache.Store("session-1", futureToken)

	assert.Equal(t, futureToken, cache.Get("session-1"))
}

func TestSessionIDTokenCache_NoExpClaimUsesDefaultTTL(t *testing.T) {
	cache := newTestCache(t)

	// JWT without exp claim -- should use defaultTokenTTL (1 hour).
	// Header: {"alg":"RS256"}, Payload: {"sub":"test"}
	noExpToken := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.sig"
	cache.Store("session-1", noExpToken)

	assert.Equal(t, noExpToken, cache.Get("session-1"))
}

func TestSessionIDTokenCache_NonJWTTokenUsesDefaultTTL(t *testing.T) {
	cache := newTestCache(t)

	cache.Store("session-1", "not-a-jwt")

	assert.Equal(t, "not-a-jwt", cache.Get("session-1"))
}

func TestSessionIDTokenCache_SweepExpiredRemovesEntries(t *testing.T) {
	cache := newTestCache(t)

	// Store one expired and one valid entry
	expiredToken := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjF9.sig"
	futureToken := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjk5OTk5OTk5OTl9.sig"

	cache.Store("expired-session", expiredToken)
	cache.Store("valid-session", futureToken)

	cache.sweepExpired()

	cache.mu.RLock()
	_, expiredExists := cache.tokens["expired-session"]
	_, validExists := cache.tokens["valid-session"]
	cache.mu.RUnlock()

	assert.False(t, expiredExists, "expired entry should be swept")
	assert.True(t, validExists, "valid entry should remain")
}

func TestSessionIDTokenCache_CloseIsIdempotent(t *testing.T) {
	cache := newTestCache(t)
	cache.Close()
	cache.Close() // second call should not panic
}

func TestExtractTokenExpiry(t *testing.T) {
	t.Run("returns zero for empty string", func(t *testing.T) {
		assert.True(t, extractTokenExpiry("").IsZero())
	})

	t.Run("returns zero for non-JWT", func(t *testing.T) {
		assert.True(t, extractTokenExpiry("not-a-jwt").IsZero())
	})

	t.Run("returns zero when exp is 0", func(t *testing.T) {
		// {"exp":0}
		token := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjB9.sig"
		assert.True(t, extractTokenExpiry(token).IsZero())
	})

	t.Run("returns correct time for valid exp", func(t *testing.T) {
		// {"exp":9999999999}
		token := "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjk5OTk5OTk5OTl9.sig"
		result := extractTokenExpiry(token)
		assert.False(t, result.IsZero())
		assert.Equal(t, int64(9999999999), result.Unix())
	})

	t.Run("returns zero when exp claim is missing", func(t *testing.T) {
		// {"sub":"test"}
		token := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.sig"
		assert.True(t, extractTokenExpiry(token).IsZero())
	})
}
