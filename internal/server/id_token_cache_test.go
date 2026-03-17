package server

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/internal/api"
)

func TestSessionIDTokenCache_StoreAndGet(t *testing.T) {
	cache := NewSessionIDTokenCache()

	cache.Store("session-1", "token-aaa")
	assert.Equal(t, "token-aaa", cache.Get("session-1"))
}

func TestSessionIDTokenCache_GetUnknownSession(t *testing.T) {
	cache := NewSessionIDTokenCache()

	assert.Equal(t, "", cache.Get("nonexistent"))
}

func TestSessionIDTokenCache_StoreOverwrites(t *testing.T) {
	cache := NewSessionIDTokenCache()

	cache.Store("session-1", "old-token")
	cache.Store("session-1", "new-token")
	assert.Equal(t, "new-token", cache.Get("session-1"))
}

func TestSessionIDTokenCache_DeleteRemoves(t *testing.T) {
	cache := NewSessionIDTokenCache()

	cache.Store("session-1", "token-aaa")
	cache.Delete("session-1")
	assert.Equal(t, "", cache.Get("session-1"))
}

func TestSessionIDTokenCache_DeleteNonexistent(t *testing.T) {
	cache := NewSessionIDTokenCache()
	cache.Delete("nonexistent")
}

func TestSessionIDTokenCache_IsolatesSessions(t *testing.T) {
	cache := NewSessionIDTokenCache()

	cache.Store("session-1", "token-1")
	cache.Store("session-2", "token-2")

	assert.Equal(t, "token-1", cache.Get("session-1"))
	assert.Equal(t, "token-2", cache.Get("session-2"))

	cache.Delete("session-1")
	assert.Equal(t, "", cache.Get("session-1"))
	assert.Equal(t, "token-2", cache.Get("session-2"))
}

func TestSessionIDTokenCache_ConcurrentAccess(t *testing.T) {
	cache := NewSessionIDTokenCache()

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
	defer api.RegisterIDTokenCache(nil)

	_ = NewSessionIDTokenCache()

	retrieved := api.GetIDTokenCache()
	assert.NotNil(t, retrieved)
}

func TestGetIDTokenCache_ReturnsNilWhenNotRegistered(t *testing.T) {
	api.RegisterIDTokenCache(nil)
	assert.Nil(t, api.GetIDTokenCache())
}
