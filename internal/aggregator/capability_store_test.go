package aggregator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryCapabilityStore_GetSetRoundTrip(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	caps := &Capabilities{
		Tools:     []mcp.Tool{{Name: "tool1"}},
		Resources: []mcp.Resource{{Name: "res1"}},
		Prompts:   []mcp.Prompt{{Name: "prompt1"}},
	}

	err := store.Set(ctx, "session1", "server1", caps)
	require.NoError(t, err)

	got, err := store.Get(ctx, "session1", "server1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, caps.Tools, got.Tools)
	assert.Equal(t, caps.Resources, got.Resources)
	assert.Equal(t, caps.Prompts, got.Prompts)
}

func TestInMemoryCapabilityStore_GetNonexistent(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	got, err := store.Get(ctx, "nouser", "noserver")
	assert.NoError(t, err)
	assert.Nil(t, got)
}

func TestInMemoryCapabilityStore_SetOverwritesPrevious(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.Set(ctx, "session1", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "old"}}})
	_ = store.Set(ctx, "session1", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "new"}}})

	got, err := store.Get(ctx, "session1", "server1")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Tools, 1)
	assert.Equal(t, "new", got.Tools[0].Name)
}

func TestInMemoryCapabilityStore_TTLExpiry(t *testing.T) {
	ttl := 50 * time.Millisecond
	store := NewInMemoryCapabilityStore(ttl)
	defer store.Stop()
	ctx := context.Background()

	_ = store.Set(ctx, "session1", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t1"}}})

	// Fresh entry
	got, err := store.Get(ctx, "session1", "server1")
	require.NoError(t, err)
	require.NotNil(t, got)

	// Wait past TTL - entry should be nil (expired)
	require.Eventually(t, func() bool {
		got, _ := store.Get(ctx, "session1", "server1")
		return got == nil
	}, 5*time.Second, 5*time.Millisecond, "entry should expire after TTL")
}

func TestInMemoryCapabilityStore_TTLResetOnSet(t *testing.T) {
	ttl := 100 * time.Millisecond
	store := NewInMemoryCapabilityStore(ttl)
	defer store.Stop()
	ctx := context.Background()

	_ = store.Set(ctx, "session1", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t1"}}})

	// Wait 70% of TTL, then set another server for the same session (resets TTL)
	time.Sleep(70 * time.Millisecond)
	_ = store.Set(ctx, "session1", "server2", &Capabilities{Tools: []mcp.Tool{{Name: "t2"}}})

	// Wait another 70% of original TTL - session should still be alive because
	// the second Set reset the TTL
	time.Sleep(70 * time.Millisecond)
	got, err := store.Get(ctx, "session1", "server1")
	require.NoError(t, err)
	require.NotNil(t, got, "server1 should still be alive after TTL reset by server2 Set")
}

func TestInMemoryCapabilityStore_Delete(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.Set(ctx, "session-A", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t1"}}})
	_ = store.Set(ctx, "session-A", "server2", &Capabilities{Tools: []mcp.Tool{{Name: "t2"}}})
	_ = store.Set(ctx, "session-B", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t3"}}})

	err := store.Delete(ctx, "session-A")
	require.NoError(t, err)

	got, _ := store.Get(ctx, "session-A", "server1")
	assert.Nil(t, got, "session-A/server1 should be deleted")

	got, _ = store.Get(ctx, "session-A", "server2")
	assert.Nil(t, got, "session-A/server2 should be deleted")

	got, _ = store.Get(ctx, "session-B", "server1")
	assert.NotNil(t, got, "session-B should not be affected")
	assert.Equal(t, "t3", got.Tools[0].Name)
}

func TestInMemoryCapabilityStore_DeleteEntry(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.Set(ctx, "session-A", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t1"}}})
	_ = store.Set(ctx, "session-A", "server2", &Capabilities{Tools: []mcp.Tool{{Name: "t2"}}})

	err := store.DeleteEntry(ctx, "session-A", "server1")
	require.NoError(t, err)

	got, _ := store.Get(ctx, "session-A", "server1")
	assert.Nil(t, got, "session-A/server1 should be deleted")

	got, _ = store.Get(ctx, "session-A", "server2")
	assert.NotNil(t, got, "session-A/server2 should not be affected")
}

func TestInMemoryCapabilityStore_DeleteServer(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.Set(ctx, "session-A", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t1"}}})
	_ = store.Set(ctx, "session-B", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t2"}}})
	_ = store.Set(ctx, "session-A", "server2", &Capabilities{Tools: []mcp.Tool{{Name: "t3"}}})

	err := store.DeleteServer(ctx, "server1")
	require.NoError(t, err)

	got, _ := store.Get(ctx, "session-A", "server1")
	assert.Nil(t, got, "session-A/server1 should be deleted")

	got, _ = store.Get(ctx, "session-B", "server1")
	assert.Nil(t, got, "session-B/server1 should be deleted")

	got, _ = store.Get(ctx, "session-A", "server2")
	assert.NotNil(t, got, "server2 should not be affected")
	assert.Equal(t, "t3", got.Tools[0].Name)
}

func TestInMemoryCapabilityStore_Exists(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	exists, err := store.Exists(ctx, "session-A", "server1")
	assert.NoError(t, err)
	assert.False(t, exists)

	_ = store.Set(ctx, "session-A", "server1", &Capabilities{})

	exists, err = store.Exists(ctx, "session-A", "server1")
	assert.NoError(t, err)
	assert.True(t, exists)
}

func TestInMemoryCapabilityStore_ListSessions(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	empty, err := store.ListSessions(ctx)
	require.NoError(t, err)
	assert.Empty(t, empty)

	require.NoError(t, store.Set(ctx, "s1", "svr1", &Capabilities{}))
	require.NoError(t, store.Set(ctx, "s2", "svr1", &Capabilities{}))
	require.NoError(t, store.Set(ctx, "s2", "svr2", &Capabilities{}))

	got, err := store.ListSessions(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"s1", "s2"}, got)
}

func TestInMemoryCapabilityStore_ListSessions_skipsExpired(t *testing.T) {
	// Short TTL — expired entries must not be returned to the admin UI.
	store := NewInMemoryCapabilityStore(10 * time.Millisecond)
	defer store.Stop()
	ctx := context.Background()

	require.NoError(t, store.Set(ctx, "fresh", "svr", &Capabilities{}))
	// Force an expired entry by rewinding its expireAt.
	store.mu.Lock()
	store.sessions["stale"] = &inMemorySession{
		servers:  map[string]*Capabilities{"svr": {}},
		expireAt: time.Now().Add(-time.Minute),
	}
	store.mu.Unlock()

	got, err := store.ListSessions(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"fresh"}, got,
		"expired sessions must be filtered from ListSessions")
}

func TestInMemoryCapabilityStore_GetAll(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.Set(ctx, "session-A", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t1"}}})
	_ = store.Set(ctx, "session-A", "server2", &Capabilities{Tools: []mcp.Tool{{Name: "t2"}}})

	all, err := store.GetAll(ctx, "session-A")
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.NotNil(t, all["server1"])
	assert.NotNil(t, all["server2"])

	// Nonexistent session
	all, err = store.GetAll(ctx, "no-session")
	assert.NoError(t, err)
	assert.Nil(t, all)
}

func TestInMemoryCapabilityStore_ConcurrentAccess(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	var wg sync.WaitGroup
	const goroutines = 50

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
			_ = store.Set(ctx, sessionID, server, &Capabilities{Tools: []mcp.Tool{{Name: "tool"}}})
		}(i)
	}

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.Get(ctx, "session-A", "server")
			_, _ = store.Get(ctx, "session-B", "server2")
		}()
	}

	for range goroutines / 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.DeleteEntry(ctx, "session-A", "server")
			_ = store.Delete(ctx, "session-A")
			_ = store.DeleteServer(ctx, "server2")
		}()
	}

	wg.Wait()
}

func TestInMemoryCapabilityStore_StopCleansTimers(t *testing.T) {
	store := NewInMemoryCapabilityStore(50 * time.Millisecond)
	ctx := context.Background()

	_ = store.Set(ctx, "s1", "srv1", &Capabilities{})
	_ = store.Set(ctx, "s2", "srv2", &Capabilities{})

	// Stop should clean up promptly.
	done := make(chan struct{})
	go func() {
		store.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return in time")
	}
}

func TestInMemoryCapabilityStore_NewServerPopulatesAfterExpiry(t *testing.T) {
	ttl := 50 * time.Millisecond
	store := NewInMemoryCapabilityStore(ttl)
	defer store.Stop()
	ctx := context.Background()

	// Set initial capabilities
	_ = store.Set(ctx, "session1", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t1"}}})

	// Wait past TTL
	require.Eventually(t, func() bool {
		got, _ := store.Get(ctx, "session1", "server1")
		return got == nil
	}, 5*time.Second, 5*time.Millisecond, "entry should expire")

	// Re-populate after expiry
	_ = store.Set(ctx, "session1", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t1-refreshed"}}})

	got, err := store.Get(ctx, "session1", "server1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "t1-refreshed", got.Tools[0].Name)
}

func TestInMemoryCapabilityStore_DeepCopyOnSet(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	tools := []mcp.Tool{{Name: "original"}}
	_ = store.Set(ctx, "s1", "srv1", &Capabilities{Tools: tools})

	// Mutating the original slice should not affect the stored copy
	tools[0].Name = "mutated"

	got, _ := store.Get(ctx, "s1", "srv1")
	require.NotNil(t, got)
	assert.Equal(t, "original", got.Tools[0].Name, "store should deep copy on Set")
}

func TestInMemoryCapabilityStore_TouchExtendsTTL(t *testing.T) {
	ttl := 100 * time.Millisecond
	store := NewInMemoryCapabilityStore(ttl)
	defer store.Stop()
	ctx := context.Background()

	_ = store.Set(ctx, "session1", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t1"}}})

	// Wait 70% of TTL, then Touch to extend it
	time.Sleep(70 * time.Millisecond)
	touched, err := store.Touch(ctx, "session1")
	require.NoError(t, err)
	assert.True(t, touched, "Touch should return true for existing session")

	// Wait another 70% of original TTL - session should still be alive
	// because Touch reset the TTL
	time.Sleep(70 * time.Millisecond)
	got, err := store.Get(ctx, "session1", "server1")
	require.NoError(t, err)
	require.NotNil(t, got, "entry should still be alive after Touch")
	assert.Equal(t, "t1", got.Tools[0].Name)
}

func TestInMemoryCapabilityStore_TouchNonexistent(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	touched, err := store.Touch(ctx, "nonexistent")
	assert.NoError(t, err)
	assert.False(t, touched, "Touch should return false for nonexistent session")
}

func TestInMemoryCapabilityStore_TouchExpiredSession(t *testing.T) {
	ttl := 50 * time.Millisecond
	store := NewInMemoryCapabilityStore(ttl)
	defer store.Stop()
	ctx := context.Background()

	_ = store.Set(ctx, "session1", "server1", &Capabilities{Tools: []mcp.Tool{{Name: "t1"}}})

	// Wait past TTL
	require.Eventually(t, func() bool {
		got, _ := store.Get(ctx, "session1", "server1")
		return got == nil
	}, 5*time.Second, 5*time.Millisecond, "entry should expire")

	// Touch on expired session should return false
	touched, err := store.Touch(ctx, "session1")
	assert.NoError(t, err)
	assert.False(t, touched, "Touch should return false for expired session")
}

func TestInMemoryCapabilityStore_DeepCopyOnGet(t *testing.T) {
	store := NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	ctx := context.Background()

	_ = store.Set(ctx, "s1", "srv1", &Capabilities{Tools: []mcp.Tool{{Name: "original"}}})

	// Mutating the returned slice should not affect the stored copy
	got1, _ := store.Get(ctx, "s1", "srv1")
	require.NotNil(t, got1)
	got1.Tools[0].Name = "mutated-by-caller"

	got2, _ := store.Get(ctx, "s1", "srv1")
	require.NotNil(t, got2)
	assert.Equal(t, "original", got2.Tools[0].Name, "store should deep copy on Get")
}
