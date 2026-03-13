package aggregator

import (
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapabilityCache_GetSetRoundTrip(t *testing.T) {
	cache := NewCapabilityCache(5 * time.Minute)
	defer cache.Stop()

	tools := []mcp.Tool{{Name: "tool1"}}
	resources := []mcp.Resource{{Name: "res1"}}
	prompts := []mcp.Prompt{{Name: "prompt1"}}

	cache.Set("user1", "server1", "test-user", tools, resources, prompts)

	entry, ok := cache.Get("user1", "server1")
	require.True(t, ok)
	assert.Equal(t, tools, entry.Tools)
	assert.Equal(t, resources, entry.Resources)
	assert.Equal(t, prompts, entry.Prompts)
	assert.False(t, entry.IsExpired())
	assert.False(t, entry.IsStale())
}

func TestCapabilityCache_GetNonexistent(t *testing.T) {
	cache := NewCapabilityCache(5 * time.Minute)
	defer cache.Stop()

	entry, ok := cache.Get("nouser", "noserver")
	assert.False(t, ok)
	assert.Nil(t, entry)
}

func TestCapabilityCache_SetOverwritesPrevious(t *testing.T) {
	cache := NewCapabilityCache(5 * time.Minute)
	defer cache.Stop()

	cache.Set("user1", "server1", "test-user", []mcp.Tool{{Name: "old"}}, nil, nil)
	cache.Set("user1", "server1", "test-user", []mcp.Tool{{Name: "new"}}, nil, nil)

	entry, ok := cache.Get("user1", "server1")
	require.True(t, ok)
	require.Len(t, entry.Tools, 1)
	assert.Equal(t, "new", entry.Tools[0].Name)
}

func TestCapabilityCache_TTLExpiry(t *testing.T) {
	ttl := 50 * time.Millisecond
	cache := NewCapabilityCache(ttl)
	defer cache.Stop()

	cache.Set("user1", "server1", "test-user", []mcp.Tool{{Name: "t1"}}, nil, nil)

	// Fresh entry
	entry, ok := cache.Get("user1", "server1")
	require.True(t, ok)
	assert.False(t, entry.IsExpired())
	assert.False(t, entry.IsStale())

	// Wait past TTL but within grace period (2x TTL)
	time.Sleep(ttl + 10*time.Millisecond)

	entry, ok = cache.Get("user1", "server1")
	require.True(t, ok, "expired entry should still be returned (stale-while-revalidate)")
	assert.True(t, entry.IsExpired())
	assert.True(t, entry.IsStale())
	assert.Equal(t, "t1", entry.Tools[0].Name)

	// After the grace period (2x TTL), the background cleanup should evict the entry.
	require.Eventually(t, func() bool {
		_, found := cache.Get("user1", "server1")
		return !found
	}, 5*time.Second, 5*time.Millisecond, "entry should be evicted after grace period")
}

func TestCapabilityCache_SetWithTTL(t *testing.T) {
	cache := NewCapabilityCache(10 * time.Minute)
	defer cache.Stop()

	customTTL := 50 * time.Millisecond
	cache.SetWithTTL("user1", "server1", "test-user", []mcp.Tool{{Name: "t1"}}, nil, nil, customTTL)

	entry, ok := cache.Get("user1", "server1")
	require.True(t, ok)
	assert.False(t, entry.IsExpired())

	// Wait past custom TTL
	time.Sleep(customTTL + 10*time.Millisecond)

	entry, ok = cache.Get("user1", "server1")
	require.True(t, ok)
	assert.True(t, entry.IsExpired())
}

func TestCapabilityCache_InvalidateUser(t *testing.T) {
	cache := NewCapabilityCache(5 * time.Minute)
	defer cache.Stop()

	cache.Set("session-A", "server1", "user1", []mcp.Tool{{Name: "t1"}}, nil, nil)
	cache.Set("session-B", "server2", "user1", []mcp.Tool{{Name: "t2"}}, nil, nil)
	cache.Set("session-C", "server1", "user2", []mcp.Tool{{Name: "t3"}}, nil, nil)

	cache.InvalidateUser("user1")

	_, ok := cache.Get("session-A", "server1")
	assert.False(t, ok, "session-A/server1 should be invalidated (owned by user1)")

	_, ok = cache.Get("session-B", "server2")
	assert.False(t, ok, "session-B/server2 should be invalidated (owned by user1)")

	entry, ok := cache.Get("session-C", "server1")
	assert.True(t, ok, "session-C (user2) should not be affected")
	assert.Equal(t, "t3", entry.Tools[0].Name)
}

func TestCapabilityCache_InvalidateServer(t *testing.T) {
	cache := NewCapabilityCache(5 * time.Minute)
	defer cache.Stop()

	cache.Set("session-A", "server1", "user1", []mcp.Tool{{Name: "t1"}}, nil, nil)
	cache.Set("session-B", "server1", "user2", []mcp.Tool{{Name: "t2"}}, nil, nil)
	cache.Set("session-A", "server2", "user1", []mcp.Tool{{Name: "t3"}}, nil, nil)

	cache.InvalidateServer("server1")

	_, ok := cache.Get("session-A", "server1")
	assert.False(t, ok, "session-A/server1 should be invalidated")

	_, ok = cache.Get("session-B", "server1")
	assert.False(t, ok, "session-B/server1 should be invalidated")

	entry, ok := cache.Get("session-A", "server2")
	assert.True(t, ok, "server2 should not be affected")
	assert.Equal(t, "t3", entry.Tools[0].Name)
}

func TestCapabilityCache_InvalidateSpecific(t *testing.T) {
	cache := NewCapabilityCache(5 * time.Minute)
	defer cache.Stop()

	cache.Set("session-A", "server1", "user1", []mcp.Tool{{Name: "t1"}}, nil, nil)
	cache.Set("session-A", "server2", "user1", []mcp.Tool{{Name: "t2"}}, nil, nil)

	cache.Invalidate("session-A", "server1")

	_, ok := cache.Get("session-A", "server1")
	assert.False(t, ok, "session-A/server1 should be invalidated")

	_, ok = cache.Get("session-A", "server2")
	assert.True(t, ok, "session-A/server2 should not be affected")
}

func TestCapabilityCache_ConcurrentAccess(t *testing.T) {
	cache := NewCapabilityCache(5 * time.Minute)
	defer cache.Stop()

	var wg sync.WaitGroup
	const goroutines = 50

	// Concurrent writers
	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessionID := "session-A"
			server := "server"
			if i%2 == 0 {
				sessionID = "session-B"
				server = "server2"
			}
			cache.Set(sessionID, server, "test-user", []mcp.Tool{{Name: "tool"}}, nil, nil)
		}(i)
	}

	// Concurrent readers
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Get("session-A", "server")
			cache.Get("session-B", "server2")
		}()
	}

	// Concurrent invalidations
	for range goroutines / 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Invalidate("session-A", "server")
			cache.InvalidateUser("test-user")
			cache.InvalidateServer("server2")
		}()
	}

	wg.Wait()
}

func TestCapabilityCache_BackgroundCleanup(t *testing.T) {
	ttl := 50 * time.Millisecond
	cache := NewCapabilityCache(ttl)
	defer cache.Stop()

	cache.Set("user1", "server1", "test-user", []mcp.Tool{{Name: "t1"}}, nil, nil)

	// The grace period is 2x TTL. The cleanup goroutine runs every TTL/2.
	// Use Eventually to avoid flakiness on slow CI runners.
	require.Eventually(t, func() bool {
		_, ok := cache.Get("user1", "server1")
		return !ok
	}, 5*time.Second, 5*time.Millisecond, "entry past grace period should be evicted by background cleanup")
}

func TestCapabilityCache_StopHaltsCleanup(t *testing.T) {
	cache := NewCapabilityCache(50 * time.Millisecond)

	// Stop should return promptly (goroutine exits).
	done := make(chan struct{})
	go func() {
		cache.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success: Stop returned.
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return in time -- possible goroutine leak")
	}

	// Calling Stop again should not block.
	cache.Stop()
}

func TestCacheEntry_IsExpired(t *testing.T) {
	now := time.Now()

	fresh := &CacheEntry{ExpiresAt: now.Add(time.Hour)}
	assert.False(t, fresh.IsExpired())

	expired := &CacheEntry{ExpiresAt: now.Add(-time.Millisecond)}
	assert.True(t, expired.IsExpired())
}

func TestCacheEntry_IsStale(t *testing.T) {
	now := time.Now()

	// Fresh: not expired, not stale
	fresh := &CacheEntry{
		ExpiresAt:     now.Add(time.Hour),
		graceDeadline: now.Add(2 * time.Hour),
	}
	assert.False(t, fresh.IsStale())

	// Stale: expired but within grace
	stale := &CacheEntry{
		ExpiresAt:     now.Add(-time.Millisecond),
		graceDeadline: now.Add(time.Hour),
	}
	assert.True(t, stale.IsStale())

	// Past grace: expired and past grace
	pastGrace := &CacheEntry{
		ExpiresAt:     now.Add(-2 * time.Hour),
		graceDeadline: now.Add(-time.Hour),
	}
	assert.False(t, pastGrace.IsStale())
}
