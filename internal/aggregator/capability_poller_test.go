package aggregator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A registry entry without a service in the service registry counts as
// connected while it has a client (ServerInfo.IsConnected): the state of
// every server these tests register through Register.

// pollTestInterval is the interval the polls of these tests walk their
// listings over: short, so a poll of a few connections returns at once.
const pollTestInterval = 5 * time.Millisecond

// drainRegistryUpdate takes the update Register signalled, so a test can
// tell whether a poll signalled another.
func drainRegistryUpdate(registry *ServerRegistry) {
	select {
	case <-registry.GetUpdateChannel():
	default:
	}
}

// connectedServer registers a shared-client server serving tools and returns
// its client, whose tool list the test changes behind muster's back.
func connectedServer(t *testing.T, registry *ServerRegistry, name string, tools ...mcp.Tool) *notifMockClient {
	t.Helper()
	client := &notifMockClient{tools: tools}
	require.NoError(t, registry.Register(context.Background(), ServerRegistration{Name: name}, client))
	drainRegistryUpdate(registry)
	return client
}

// perSessionServer registers a server every session connects to on its own.
func perSessionServer(t *testing.T, registry *ServerRegistry, name string) {
	t.Helper()
	require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: name},
		URL:                "http://" + name + "/mcp",
		AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
	}))
}

func namesOf(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestPollCapabilities_RelistsAConnectedServer: the backend was redeployed
// with another tool set and told nobody; the poll re-lists it, the registry
// entry follows and the registry's listeners are signalled.
func TestPollCapabilities_RelistsAConnectedServer(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}
	client := connectedServer(t, registry, "srv", mcp.Tool{Name: "probe", Description: "old image"})

	client.setTools([]mcp.Tool{{Name: "report", Description: "new image"}})
	a.pollCapabilities(pollTestInterval)

	info, _ := registry.GetServerInfo("srv")
	info.mu.RLock()
	names := namesOf(info.Tools)
	info.mu.RUnlock()
	assert.Equal(t, []string{"report"}, names)
	select {
	case <-registry.GetUpdateChannel():
	default:
		t.Fatal("a changed tool list must signal the registry's listeners")
	}
}

// TestPollCapabilities_UnchangedListSignalsNothing: a poll costs one tools
// listing per connected server and, with nothing changed, wakes nobody.
func TestPollCapabilities_UnchangedListSignalsNothing(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}
	client := connectedServer(t, registry, "srv", mcp.Tool{Name: "probe"})
	before := atomic.LoadInt32(&client.listToolsCalls)

	a.pollCapabilities(pollTestInterval)

	assert.Equal(t, before+1, atomic.LoadInt32(&client.listToolsCalls), "one listing per poll")
	select {
	case <-registry.GetUpdateChannel():
		t.Fatal("an unchanged tool list must not signal an update")
	default:
	}
}

// TestPollCapabilities_RelistsEachPooledSessionWithoutUsingIt: a per-session
// server is re-listed through every live pooled connection, into that
// session's own capability store entry, and the poll does not count as use
// of the connection.
func TestPollCapabilities_RelistsEachPooledSessionWithoutUsingIt(t *testing.T) {
	ctx := context.Background()
	capStore := oauthstore.NewInMemoryCapabilityStore(time.Hour)
	pool := NewSessionConnectionPool(time.Hour)
	defer pool.Stop()
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry, capabilityStore: capStore, connPool: pool}
	perSessionServer(t, registry, "sso")

	old := &oauthstore.Capabilities{Tools: []mcp.Tool{{Name: "probe"}}}
	require.NoError(t, capStore.Set(ctx, "sess-1", "sso", old))
	require.NoError(t, capStore.Set(ctx, "sess-2", "sso", old))
	pool.Put("sess-1", "sso", &notifMockClient{tools: []mcp.Tool{{Name: "probe"}, {Name: "report"}}})
	pool.Put("sess-2", "sso", &notifMockClient{tools: []mcp.Tool{{Name: "report"}}})
	lastUsed := pool.Snapshot("sess-1")[0].LastUsedAt

	a.pollCapabilities(pollTestInterval)

	caps1, err := capStore.Get(ctx, "sess-1", "sso")
	require.NoError(t, err)
	require.NotNil(t, caps1)
	assert.Equal(t, []string{"probe", "report"}, namesOf(caps1.Tools))
	caps2, err := capStore.Get(ctx, "sess-2", "sso")
	require.NoError(t, err)
	require.NotNil(t, caps2)
	assert.Equal(t, []string{"report"}, namesOf(caps2.Tools))
	assert.Equal(t, lastUsed, pool.Snapshot("sess-1")[0].LastUsedAt, "a poll is not a use of the connection")
}

// TestPollCapabilities_SkipsAnExpiredExchangedToken: a pooled connection
// whose exchanged token has expired is left to the next tool call, which
// re-exchanges; a listing with it would only collect a 401.
func TestPollCapabilities_SkipsAnExpiredExchangedToken(t *testing.T) {
	pool := NewSessionConnectionPool(time.Hour)
	defer pool.Stop()
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry, capabilityStore: oauthstore.NewInMemoryCapabilityStore(time.Hour), connPool: pool}
	perSessionServer(t, registry, "sso")
	expired := &notifMockClient{tools: []mcp.Tool{{Name: "report"}}}
	pool.PutWithExpiry("sess-expired", "sso", expired, time.Now().Add(-time.Minute))
	valid := &notifMockClient{tools: []mcp.Tool{{Name: "report"}}}
	pool.PutWithExpiry("sess-valid", "sso", valid, time.Now().Add(time.Hour))

	a.pollCapabilities(pollTestInterval)

	assert.Zero(t, atomic.LoadInt32(&expired.listToolsCalls), "an expired exchanged token is not used for a listing")
	assert.Equal(t, int32(1), atomic.LoadInt32(&valid.listToolsCalls))
}

// TestPollCapabilities_NothingConnectedCostsNothing: a per-session server no
// session holds a connection to, with or without a pool, is not polled.
func TestPollCapabilities_NothingConnectedCostsNothing(t *testing.T) {
	registry := NewServerRegistry("x")
	perSessionServer(t, registry, "sso")

	(&AggregatorServer{registry: registry}).pollCapabilities(pollTestInterval)

	pool := NewSessionConnectionPool(time.Hour)
	defer pool.Stop()
	(&AggregatorServer{registry: registry, connPool: pool}).pollCapabilities(pollTestInterval)
}

// TestRunCapabilityPoller_PollsOnTheTickAndStopsWithTheContext: every tick
// re-lists the connected servers; the aggregator's context ends the loop.
func TestRunCapabilityPoller_PollsOnTheTickAndStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	registry := NewServerRegistry("x")
	client := connectedServer(t, registry, "srv", mcp.Tool{Name: "probe"})
	base := atomic.LoadInt32(&client.listToolsCalls)
	a := &AggregatorServer{ctx: ctx, registry: registry}

	a.wg.Add(1)
	go a.runCapabilityPoller(10 * time.Millisecond)
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&client.listToolsCalls)-base >= 2
	}, 5*time.Second, time.Millisecond, "two ticks, two listings")

	cancel()
	stopped := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the poller did not stop with the aggregator's context")
	}
}

// TestPollCapabilities_AToolsOnlyServerCostsOneRequest: a server that
// declared tools alone at its handshake is polled with tools/list only; one
// that declared resources and prompts is asked for those too.
func TestPollCapabilities_AToolsOnlyServerCostsOneRequest(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}
	toolsOnly := connectedServer(t, registry, "tools-only", mcp.Tool{Name: "probe"})
	full := connectedServer(t, registry, "full", mcp.Tool{Name: "probe"})
	full.capabilities = declaring(true, true)
	baseTools, baseResources, basePrompts := toolsOnly.listCounts()
	fullTools, fullResources, fullPrompts := full.listCounts()

	a.pollCapabilities(pollTestInterval)

	tools, resources, prompts := toolsOnly.listCounts()
	assert.Equal(t, [3]int32{baseTools + 1, baseResources, basePrompts}, [3]int32{tools, resources, prompts},
		"a tools-only server: one request per poll")
	tools, resources, prompts = full.listCounts()
	assert.Equal(t, [3]int32{fullTools + 1, fullResources + 1, fullPrompts + 1}, [3]int32{tools, resources, prompts},
		"a server that declared resources and prompts: three")
}

// TestPollWalk_SpacesTheListingsOverTheInterval: n listings over an interval
// are due one every interval/n from the tick, the first at the tick and the
// last before the next one; a clock moved past the last slot -- the harness
// advancing it by the interval -- makes every remaining listing due at once.
func TestPollWalk_SpacesTheListingsOverTheInterval(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	walk := pollWalk{start: start, spacing: time.Minute, n: 5} // five listings over five minutes

	assert.Equal(t, 0, walk.due(start.Add(-time.Second)), "nothing before the tick")
	assert.Equal(t, 1, walk.due(start), "the first listing starts with the tick")
	assert.Equal(t, 1, walk.due(start.Add(59*time.Second)))
	assert.Equal(t, 2, walk.due(start.Add(time.Minute)))
	assert.Equal(t, 4, walk.due(start.Add(3*time.Minute+30*time.Second)))
	assert.Equal(t, 5, walk.due(start.Add(4*time.Minute)), "the last listing is due one spacing before the next tick")
	assert.Equal(t, 5, walk.due(start.Add(time.Hour)), "a clock advanced past the interval makes everything due")

	assert.Equal(t, time.Minute, newPollWalk(5, 5*time.Minute).spacing)
	assert.Equal(t, 5*time.Minute, newPollWalk(1, 5*time.Minute).spacing, "one listing: no walk")
	assert.Equal(t, time.Millisecond, newPollWalk(1000, time.Millisecond).spacing, "never finer than a millisecond")
}

// TestPollCapabilities_WalksTheListingsOverTheInterval: three connected
// servers and an interval of 600 ms: the listings are not one burst at the
// tick. In the order of the server names, the second starts no earlier than
// 200 ms and the third no earlier than 400 ms after the poll began, and the
// poll returns after the third is done.
func TestPollCapabilities_WalksTheListingsOverTheInterval(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}
	clients := map[string]*notifMockClient{}
	for _, name := range []string{"beta", "gamma", "alpha"} {
		clients[name] = connectedServer(t, registry, name, mcp.Tool{Name: "probe"})
	}
	const interval = 600 * time.Millisecond

	start := time.Now()
	a.pollCapabilities(interval)
	elapsed := time.Since(start)

	since := func(name string) time.Duration { return clients[name].lastListToolsAt().Sub(start) }
	for name, client := range clients {
		assert.Equal(t, int32(2), atomic.LoadInt32(&client.listToolsCalls), "%s: listed at registration and once by the poll", name)
	}
	assert.GreaterOrEqual(t, since("beta"), 200*time.Millisecond, "the second listing waits one spacing")
	assert.GreaterOrEqual(t, since("gamma"), 400*time.Millisecond, "the third waits two")
	assert.Less(t, since("alpha"), since("beta"), "the walk goes by key")
	assert.GreaterOrEqual(t, elapsed, 400*time.Millisecond, "the poll returns when the last listing is done")
	assert.Less(t, elapsed, interval, "and before the next tick")
}

// TestPollCapabilities_SkipsAConnectionThePoolDropped: a pooled connection
// evicted or replaced between the tick and its slot in the walk is not
// listed through the client the pool no longer holds; the one still held is.
func TestPollCapabilities_SkipsAConnectionThePoolDropped(t *testing.T) {
	pool := NewSessionConnectionPool(time.Hour)
	defer pool.Stop()
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry, capabilityStore: oauthstore.NewInMemoryCapabilityStore(time.Hour), connPool: pool}
	perSessionServer(t, registry, "sso")
	held := &notifMockClient{tools: []mcp.Tool{{Name: "report"}}}
	pool.Put("sess-held", "sso", held)
	dropped := &notifMockClient{tools: []mcp.Tool{{Name: "report"}}}
	pool.Put("sess-dropped", "sso", dropped)
	planned := a.pollJobs()
	require.Len(t, planned, 2)
	pool.Evict("sess-dropped", "sso")

	for _, job := range planned {
		job.run()
	}

	assert.Zero(t, atomic.LoadInt32(&dropped.listToolsCalls), "the evicted connection is not listed")
	assert.Equal(t, int32(1), atomic.LoadInt32(&held.listToolsCalls))
}

// TestPollCapabilities_SkipsAServerThatWentAway: a shared-client server
// deregistered between the tick and its slot is skipped, without the
// "not found" warning a notification for it would earn.
func TestPollCapabilities_SkipsAServerThatWentAway(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}
	client := connectedServer(t, registry, "gone", mcp.Tool{Name: "probe"})
	base := atomic.LoadInt32(&client.listToolsCalls)
	planned := a.pollJobs()
	require.Len(t, planned, 1)
	require.NoError(t, registry.Deregister("gone"))

	planned[0].run()

	assert.Equal(t, base, atomic.LoadInt32(&client.listToolsCalls), "a server that went away is not listed")
}
