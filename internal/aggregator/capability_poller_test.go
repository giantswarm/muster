package aggregator

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPollNonAuthServer_RefreshesCapabilities(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}

	initial := []mcp.Tool{{Name: "old-tool", Description: "v1"}}
	client := &notifMockClient{tools: initial}

	ctx := context.Background()
	require.NoError(t, registry.Register(ctx, "srv", client, ""))

	updated := []mcp.Tool{
		{Name: "old-tool", Description: "v1"},
		{Name: "new-tool", Description: "v2"},
	}
	client.setTools(updated)

	a.pollNonAuthServer("srv")

	info, _ := registry.GetServerInfo("srv")
	info.mu.RLock()
	assert.Len(t, info.Tools, 2)
	info.mu.RUnlock()
}

func TestPollNonAuthServer_NoChangeSkipsUpdate(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}

	tools := []mcp.Tool{{Name: "tool", Description: "d"}}
	client := &notifMockClient{tools: tools}

	ctx := context.Background()
	require.NoError(t, registry.Register(ctx, "srv", client, ""))

	select {
	case <-registry.GetUpdateChannel():
	default:
	}

	a.pollNonAuthServer("srv")

	select {
	case <-registry.GetUpdateChannel():
		t.Fatal("expected no update when tools haven't changed")
	default:
	}
}

func TestPollNonAuthServer_SingleflightDedup(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}

	tools := []mcp.Tool{{Name: "t1"}}
	client := &notifMockClient{tools: tools}

	ctx := context.Background()
	require.NoError(t, registry.Register(ctx, "srv", client, ""))

	baseCount := atomic.LoadInt32(&client.listToolsCalls)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.pollNonAuthServer("srv")
		}()
	}
	wg.Wait()

	calls := atomic.LoadInt32(&client.listToolsCalls) - baseCount
	assert.LessOrEqual(t, calls, int32(5),
		"singleflight should deduplicate concurrent calls, got %d", calls)
}

func TestPollAuthServer_IteratesActiveSessions(t *testing.T) {
	capStore := NewInMemoryCapabilityStore(time.Hour)
	pool := NewSessionConnectionPool(time.Hour)
	defer pool.Stop()

	registry := NewServerRegistry("x")
	a := &AggregatorServer{
		registry:        registry,
		capabilityStore: capStore,
		connPool:        pool,
	}

	oldCaps := &Capabilities{Tools: []mcp.Tool{{Name: "old"}}}
	require.NoError(t, capStore.Set(context.Background(), "sess-1", "auth-srv", oldCaps))

	updated := []mcp.Tool{{Name: "old"}, {Name: "new"}}
	client := &notifMockClient{tools: updated}
	pool.Put("sess-1", "auth-srv", client)

	a.pollAuthServer("auth-srv")

	caps, err := capStore.Get(context.Background(), "sess-1", "auth-srv")
	require.NoError(t, err)
	require.NotNil(t, caps)
	assert.Len(t, caps.Tools, 2)
}

func TestPollAuthServer_SkipsWhenNoActiveSessions(t *testing.T) {
	pool := NewSessionConnectionPool(time.Hour)
	defer pool.Stop()

	a := &AggregatorServer{
		registry: NewServerRegistry("x"),
		connPool: pool,
	}

	a.pollAuthServer("no-sessions-server")
}

func TestPollAuthServer_NilConnPool(t *testing.T) {
	a := &AggregatorServer{
		registry: NewServerRegistry("x"),
		connPool: nil,
	}

	a.pollAuthServer("srv")
}

func TestPollAllServers_ConnectedAndAuthRequired(t *testing.T) {
	capStore := NewInMemoryCapabilityStore(time.Hour)
	pool := NewSessionConnectionPool(time.Hour)
	defer pool.Stop()

	registry := NewServerRegistry("x")
	a := &AggregatorServer{
		registry:        registry,
		capabilityStore: capStore,
		connPool:        pool,
	}

	nonAuthClient := &notifMockClient{tools: []mcp.Tool{{Name: "t1"}}}
	require.NoError(t, registry.Register(context.Background(), "connected-srv", nonAuthClient, ""))

	_ = registry.RegisterPendingAuth("auth-srv", "http://localhost:9999", "", nil)

	authClient := &notifMockClient{tools: []mcp.Tool{{Name: "auth-t1"}}}
	pool.Put("sess-1", "auth-srv", authClient)
	require.NoError(t, capStore.Set(context.Background(), "sess-1", "auth-srv",
		&Capabilities{Tools: []mcp.Tool{{Name: "auth-t1"}}}))

	baseNonAuth := atomic.LoadInt32(&nonAuthClient.listToolsCalls)

	a.pollAllServers()

	assert.Equal(t, baseNonAuth+1, atomic.LoadInt32(&nonAuthClient.listToolsCalls),
		"pollAllServers should trigger exactly one ListTools call for the non-auth server")
}

func TestRunCapabilityPoller_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	registry := NewServerRegistry("x")
	client := &notifMockClient{tools: []mcp.Tool{{Name: "t1"}}}
	require.NoError(t, registry.Register(context.Background(), "srv", client, ""))

	baseCount := atomic.LoadInt32(&client.listToolsCalls)

	a := &AggregatorServer{
		ctx:      ctx,
		registry: registry,
		config:   AggregatorConfig{CapabilityPollInterval: 50 * time.Millisecond},
		connPool: NewSessionConnectionPool(time.Hour),
	}
	defer a.connPool.Stop()

	a.wg.Add(1)
	go a.runCapabilityPoller()

	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&client.listToolsCalls)-baseCount >= 1
	}, 5*time.Second, 10*time.Millisecond,
		"poller should have polled at least once before cancel")

	cancel()

	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not stop within timeout after context cancel")
	}
}

func TestRunCapabilityPoller_PollsOnTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registry := NewServerRegistry("x")
	client := &notifMockClient{tools: []mcp.Tool{{Name: "t1"}}}
	require.NoError(t, registry.Register(context.Background(), "srv", client, ""))

	baseCount := atomic.LoadInt32(&client.listToolsCalls)

	a := &AggregatorServer{
		ctx:      ctx,
		registry: registry,
		config:   AggregatorConfig{CapabilityPollInterval: 100 * time.Millisecond},
		connPool: NewSessionConnectionPool(time.Hour),
	}
	defer a.connPool.Stop()

	a.wg.Add(1)
	go a.runCapabilityPoller()

	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&client.listToolsCalls)-baseCount >= 2
	}, 5*time.Second, 50*time.Millisecond,
		"poller should have re-fetched tools at least twice")

	cancel()
	a.wg.Wait()
}

func TestDefaultCapabilityPollInterval(t *testing.T) {
	assert.Equal(t, 5*time.Minute, DefaultCapabilityPollInterval)
}

func TestRunCapabilityPoller_UsesDefaultInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	a := &AggregatorServer{
		ctx:      ctx,
		registry: NewServerRegistry("x"),
		config:   AggregatorConfig{},
		connPool: NewSessionConnectionPool(time.Hour),
	}
	defer a.connPool.Stop()

	a.wg.Add(1)
	go a.runCapabilityPoller()
	a.wg.Wait()
}
