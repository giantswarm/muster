package aggregator

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

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

func TestSessionConnectionPool_GetPut(t *testing.T) {
	pool := NewSessionConnectionPool()
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
	pool := NewSessionConnectionPool()
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
	pool := NewSessionConnectionPool()
	client := &poolTestClient{}

	pool.Put("s1", "srv-a", client)
	pool.Evict("s1", "srv-a")

	assert.Equal(t, int32(1), client.closeCount.Load())
	_, ok := pool.Get("s1", "srv-a")
	assert.False(t, ok)
	assert.Equal(t, 0, pool.Len())
}

func TestSessionConnectionPool_EvictNonExistent(t *testing.T) {
	pool := NewSessionConnectionPool()
	// Must not panic.
	pool.Evict("s1", "srv-a")
}

func TestSessionConnectionPool_EvictSession(t *testing.T) {
	pool := NewSessionConnectionPool()
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
	pool := NewSessionConnectionPool()
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
	pool := NewSessionConnectionPool()
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
	pool := NewSessionConnectionPool()
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
