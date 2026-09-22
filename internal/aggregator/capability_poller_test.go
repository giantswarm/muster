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
	a.pollCapabilities()

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

// TestPollCapabilities_UnchangedListSignalsNothing: a poll costs one listing
// per connected server and, with nothing changed, wakes nobody.
func TestPollCapabilities_UnchangedListSignalsNothing(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}
	client := connectedServer(t, registry, "srv", mcp.Tool{Name: "probe"})
	before := atomic.LoadInt32(&client.listToolsCalls)

	a.pollCapabilities()

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

	a.pollCapabilities()

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

	a.pollCapabilities()

	assert.Zero(t, atomic.LoadInt32(&expired.listToolsCalls), "an expired exchanged token is not used for a listing")
	assert.Equal(t, int32(1), atomic.LoadInt32(&valid.listToolsCalls))
}

// TestPollCapabilities_NothingConnectedCostsNothing: a per-session server no
// session holds a connection to, with or without a pool, is not polled.
func TestPollCapabilities_NothingConnectedCostsNothing(t *testing.T) {
	registry := NewServerRegistry("x")
	perSessionServer(t, registry, "sso")

	(&AggregatorServer{registry: registry}).pollCapabilities()

	pool := NewSessionConnectionPool(time.Hour)
	defer pool.Stop()
	(&AggregatorServer{registry: registry, connPool: pool}).pollCapabilities()
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
