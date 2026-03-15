package aggregator

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// poolTestClient is a minimal MCPClient mock for connection pool tests.
// Unlike the shared mockMCPClient, it allows inspecting close-count safely
// from concurrent goroutines.
type poolTestClient struct {
	closeCount atomic.Int32
}

func (c *poolTestClient) Initialize(context.Context) error              { return nil }
func (c *poolTestClient) Close() error                                  { c.closeCount.Add(1); return nil }
func (c *poolTestClient) ListTools(context.Context) ([]mcp.Tool, error) { return nil, nil }
func (c *poolTestClient) CallTool(context.Context, string, map[string]interface{}) (*mcp.CallToolResult, error) {
	return nil, nil
}
func (c *poolTestClient) ListResources(context.Context) ([]mcp.Resource, error) { return nil, nil }
func (c *poolTestClient) ReadResource(context.Context, string) (*mcp.ReadResourceResult, error) {
	return nil, nil
}
func (c *poolTestClient) ListPrompts(context.Context) ([]mcp.Prompt, error) { return nil, nil }
func (c *poolTestClient) GetPrompt(context.Context, string, map[string]interface{}) (*mcp.GetPromptResult, error) {
	return nil, nil
}
func (c *poolTestClient) Ping(context.Context) error { return nil }

const testPoolMaxAge = 30 * time.Minute

func newTestPool() *SessionConnectionPool {
	pool := NewSessionConnectionPool(testPoolMaxAge)
	return pool
}

func TestSessionConnectionPool_GetPut(t *testing.T) {
	pool := newTestPool()
	defer pool.Stop()
	client := &poolTestClient{}

	// Get on empty pool returns false.
	got, ok := pool.Get("s1", "srv-a")
	assert.False(t, ok)
	assert.Nil(t, got)

	// Put and Get.
	pool.Put("s1", "srv-a", client)
	got, ok = pool.Get("s1", "srv-a")
	require.True(t, ok)
	assert.Equal(t, client, got)

	// Different key returns miss.
	_, ok = pool.Get("s1", "srv-b")
	assert.False(t, ok)

	assert.Equal(t, 1, pool.Len())
}

func TestSessionConnectionPool_PutReplacesOldEntry(t *testing.T) {
	pool := newTestPool()
	defer pool.Stop()
	old := &poolTestClient{}
	replacement := &poolTestClient{}

	pool.Put("s1", "srv-a", old)
	pool.Put("s1", "srv-a", replacement)

	// Old client must have been closed exactly once.
	assert.Equal(t, int32(1), old.closeCount.Load())

	// New client is returned.
	got, ok := pool.Get("s1", "srv-a")
	require.True(t, ok)
	assert.Equal(t, replacement, got)

	assert.Equal(t, 1, pool.Len())
}

func TestSessionConnectionPool_Evict(t *testing.T) {
	pool := newTestPool()
	defer pool.Stop()
	client := &poolTestClient{}

	pool.Put("s1", "srv-a", client)
	pool.Evict("s1", "srv-a")

	assert.Equal(t, int32(1), client.closeCount.Load())
	_, ok := pool.Get("s1", "srv-a")
	assert.False(t, ok)
	assert.Equal(t, 0, pool.Len())
}

func TestSessionConnectionPool_EvictNonExistent(t *testing.T) {
	pool := newTestPool()
	defer pool.Stop()
	// Must not panic.
	pool.Evict("s1", "srv-a")
}

func TestSessionConnectionPool_EvictSession(t *testing.T) {
	pool := newTestPool()
	defer pool.Stop()
	c1 := &poolTestClient{}
	c2 := &poolTestClient{}
	c3 := &poolTestClient{}

	pool.Put("s1", "srv-a", c1)
	pool.Put("s1", "srv-b", c2)
	pool.Put("s2", "srv-a", c3) // different session

	pool.EvictSession("s1")

	assert.Equal(t, int32(1), c1.closeCount.Load())
	assert.Equal(t, int32(1), c2.closeCount.Load())
	assert.Equal(t, int32(0), c3.closeCount.Load()) // untouched

	_, ok := pool.Get("s1", "srv-a")
	assert.False(t, ok)
	_, ok = pool.Get("s1", "srv-b")
	assert.False(t, ok)

	got, ok := pool.Get("s2", "srv-a")
	require.True(t, ok)
	assert.Equal(t, c3, got)

	assert.Equal(t, 1, pool.Len())
}

func TestSessionConnectionPool_EvictServer(t *testing.T) {
	pool := newTestPool()
	defer pool.Stop()
	c1 := &poolTestClient{}
	c2 := &poolTestClient{}
	c3 := &poolTestClient{}

	pool.Put("s1", "srv-a", c1)
	pool.Put("s2", "srv-a", c2)
	pool.Put("s1", "srv-b", c3) // different server

	pool.EvictServer("srv-a")

	assert.Equal(t, int32(1), c1.closeCount.Load())
	assert.Equal(t, int32(1), c2.closeCount.Load())
	assert.Equal(t, int32(0), c3.closeCount.Load())

	_, ok := pool.Get("s1", "srv-a")
	assert.False(t, ok)
	_, ok = pool.Get("s2", "srv-a")
	assert.False(t, ok)

	got, ok := pool.Get("s1", "srv-b")
	require.True(t, ok)
	assert.Equal(t, c3, got)

	assert.Equal(t, 1, pool.Len())
}

func TestSessionConnectionPool_DrainAll(t *testing.T) {
	pool := newTestPool()
	defer pool.Stop()
	clients := make([]*poolTestClient, 5)
	for i := range clients {
		clients[i] = &poolTestClient{}
		pool.Put("s1", "srv-"+string(rune('a'+i)), clients[i])
	}
	require.Equal(t, 5, pool.Len())

	pool.DrainAll()

	for i, c := range clients {
		assert.Equal(t, int32(1), c.closeCount.Load(), "client %d not closed", i)
	}
	assert.Equal(t, 0, pool.Len())
}

func TestSessionConnectionPool_ConcurrentAccess(t *testing.T) {
	pool := newTestPool()
	defer pool.Stop()
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			sessionID := "session"
			serverName := "server"

			client := &poolTestClient{}
			pool.Put(sessionID, serverName, client)

			pool.Get(sessionID, serverName)

			if idx%5 == 0 {
				pool.Evict(sessionID, serverName)
			}
		}(i)
	}

	wg.Wait()

	// Pool should still be in a consistent state.
	pool.DrainAll()
	assert.Equal(t, 0, pool.Len())
}

func TestSessionConnectionPool_PutWithExpiry(t *testing.T) {
	pool := newTestPool()
	defer pool.Stop()
	client := &poolTestClient{}
	expiry := time.Now().Add(10 * time.Minute)

	pool.PutWithExpiry("s1", "srv-a", client, expiry)

	got, ok := pool.Get("s1", "srv-a")
	require.True(t, ok)
	assert.Equal(t, client, got)
}

func TestSessionConnectionPool_IsTokenExpiringSoon(t *testing.T) {
	pool := newTestPool()
	defer pool.Stop()

	t.Run("returns false when no pool entry exists", func(t *testing.T) {
		assert.False(t, pool.IsTokenExpiringSoon("s1", "srv-a", 30*time.Second))
	})

	t.Run("returns false when no expiry is tracked", func(t *testing.T) {
		pool.Put("s1", "srv-a", &poolTestClient{})
		assert.False(t, pool.IsTokenExpiringSoon("s1", "srv-a", 30*time.Second))
	})

	t.Run("returns false when token has enough remaining lifetime", func(t *testing.T) {
		pool.PutWithExpiry("s1", "srv-b", &poolTestClient{}, time.Now().Add(10*time.Minute))
		assert.False(t, pool.IsTokenExpiringSoon("s1", "srv-b", 30*time.Second))
	})

	t.Run("returns true when token expires within margin", func(t *testing.T) {
		pool.PutWithExpiry("s1", "srv-c", &poolTestClient{}, time.Now().Add(15*time.Second))
		assert.True(t, pool.IsTokenExpiringSoon("s1", "srv-c", 30*time.Second))
	})

	t.Run("returns true when token is already expired", func(t *testing.T) {
		pool.PutWithExpiry("s1", "srv-d", &poolTestClient{}, time.Now().Add(-5*time.Second))
		assert.True(t, pool.IsTokenExpiringSoon("s1", "srv-d", 30*time.Second))
	})
}

func TestSessionConnectionPool_PutWithExpiryReplacesOld(t *testing.T) {
	pool := newTestPool()
	defer pool.Stop()
	old := &poolTestClient{}
	replacement := &poolTestClient{}

	pool.PutWithExpiry("s1", "srv-a", old, time.Now().Add(5*time.Minute))
	pool.PutWithExpiry("s1", "srv-a", replacement, time.Now().Add(10*time.Minute))

	assert.Equal(t, int32(1), old.closeCount.Load())

	got, ok := pool.Get("s1", "srv-a")
	require.True(t, ok)
	assert.Equal(t, replacement, got)
}

func TestSessionConnectionPool_EvictIdleReapsStaleEntries(t *testing.T) {
	maxAge := 100 * time.Millisecond
	pool := NewSessionConnectionPool(maxAge)
	defer pool.Stop()

	idle := &poolTestClient{}
	pool.Put("s1", "srv-idle", idle)

	// Backdate the entry's LastUsedAt to be older than maxAge.
	pool.mu.Lock()
	key := poolKey{SessionID: "s1", ServerName: "srv-idle"}
	pool.pool[key].LastUsedAt = time.Now().Add(-2 * maxAge)
	pool.mu.Unlock()

	// Run the reaper directly (no need to wait for the ticker).
	pool.evictIdle()

	assert.Equal(t, 0, pool.Len(), "idle entry should have been reaped")
	assert.Equal(t, int32(1), idle.closeCount.Load(), "idle client should have been closed")
}

func TestSessionConnectionPool_EvictIdleKeepsActiveEntries(t *testing.T) {
	maxAge := 100 * time.Millisecond
	pool := NewSessionConnectionPool(maxAge)
	defer pool.Stop()

	active := &poolTestClient{}
	pool.Put("s1", "srv-active", active)

	// Entry was just created so LastUsedAt is recent -- evictIdle should keep it.
	pool.evictIdle()

	assert.Equal(t, 1, pool.Len(), "active entry should not be reaped")
	assert.Equal(t, int32(0), active.closeCount.Load(), "active client should not be closed")
}

func TestSessionConnectionPool_GetResetsIdleTimer(t *testing.T) {
	maxAge := 100 * time.Millisecond
	pool := NewSessionConnectionPool(maxAge)
	defer pool.Stop()

	client := &poolTestClient{}
	pool.Put("s1", "srv-a", client)

	// Backdate the entry to make it stale.
	pool.mu.Lock()
	key := poolKey{SessionID: "s1", ServerName: "srv-a"}
	pool.pool[key].LastUsedAt = time.Now().Add(-2 * maxAge)
	pool.mu.Unlock()

	// Access the entry via Get -- this should reset LastUsedAt.
	_, ok := pool.Get("s1", "srv-a")
	require.True(t, ok)

	// Now evictIdle should NOT reap it because Get refreshed LastUsedAt.
	pool.evictIdle()

	assert.Equal(t, 1, pool.Len(), "entry accessed via Get should not be reaped")
	assert.Equal(t, int32(0), client.closeCount.Load(), "client should not be closed after Get refresh")
}

func TestSessionConnectionPool_ReaperRunsAutomatically(t *testing.T) {
	maxAge := 50 * time.Millisecond
	pool := NewSessionConnectionPool(maxAge)
	defer pool.Stop()

	clients := make([]*poolTestClient, 3)
	for i := range clients {
		clients[i] = &poolTestClient{}
		pool.Put("s1", fmt.Sprintf("srv-%d", i), clients[i])
	}
	require.Equal(t, 3, pool.Len())

	// Backdate all entries so the reaper will pick them up.
	pool.mu.Lock()
	for key, entry := range pool.pool {
		_ = key
		entry.LastUsedAt = time.Now().Add(-2 * maxAge)
	}
	pool.mu.Unlock()

	// The reaper interval is maxAge/2 = 25ms (clamped to 1s minimum).
	// Since 25ms < 1s, the interval is 1s. We call evictIdle directly
	// to avoid waiting for the ticker in a unit test.
	pool.evictIdle()

	assert.Equal(t, 0, pool.Len(), "all idle entries should be reaped")
	for i, c := range clients {
		assert.Equal(t, int32(1), c.closeCount.Load(), "client %d should be closed", i)
	}
}

func TestSessionConnectionPool_StopIsIdempotent(t *testing.T) {
	pool := NewSessionConnectionPool(time.Minute)

	// Calling Stop multiple times must not panic.
	pool.Stop()
	pool.Stop()
	pool.Stop()
}

func TestSessionConnectionPool_EvictIdleMixedEntries(t *testing.T) {
	maxAge := 100 * time.Millisecond
	pool := NewSessionConnectionPool(maxAge)
	defer pool.Stop()

	idle := &poolTestClient{}
	active := &poolTestClient{}

	pool.Put("s1", "srv-idle", idle)
	pool.Put("s1", "srv-active", active)

	// Make only the idle entry stale.
	pool.mu.Lock()
	pool.pool[poolKey{SessionID: "s1", ServerName: "srv-idle"}].LastUsedAt = time.Now().Add(-2 * maxAge)
	pool.mu.Unlock()

	pool.evictIdle()

	assert.Equal(t, 1, pool.Len(), "only the idle entry should be reaped")
	assert.Equal(t, int32(1), idle.closeCount.Load())
	assert.Equal(t, int32(0), active.closeCount.Load())

	got, ok := pool.Get("s1", "srv-active")
	require.True(t, ok)
	assert.Equal(t, active, got)
}
