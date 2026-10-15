package aggregator

import (
	"context"
	"sync"
	"testing"
	"time"

	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// onCallConnects records the SSO connects a call makes and connects the way
// establishSSOConnection does: the client pooled, the session authenticated,
// the server's tools in the session's capability cache.
type onCallConnects struct {
	mu     sync.Mutex
	counts map[string]int
	client *callToolMockClient
}

func (c *onCallConnects) count(server string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[server]
}

// newOnCallTestAggregator builds an aggregator with one token-forwarding
// server per name, each offering the tool "op", and no session fan-out: a
// session's first request is a direct call.
func newOnCallTestAggregator(t *testing.T, servers ...string) (*AggregatorServer, *onCallConnects) {
	t.Helper()
	agg := newBootstrapTestAggregator(t, newConnectGates(), servers...)
	agg.capabilityStore = oauthstore.NewInMemoryCapabilityStore(time.Hour)
	connects := &onCallConnects{
		counts: map[string]int{},
		client: &callToolMockClient{callToolResult: mcp.NewToolResultText("done")},
	}
	agg.ssoConnect = func(ctx context.Context, info *ServerInfo, _ string) ssoConnectOutcome {
		connects.mu.Lock()
		connects.counts[info.Name]++
		connects.mu.Unlock()
		sessionID := getSessionIDFromContext(ctx)
		agg.connPool.Put(sessionID, info.Name, connects.client)
		if err := agg.authStore.MarkAuthenticated(ctx, sessionID, info.Name); err != nil {
			return ssoConnectFailed
		}
		if err := agg.capabilityStore.Set(ctx, sessionID, info.Name, &oauthstore.Capabilities{
			Tools: []mcp.Tool{{Name: "op"}},
		}); err != nil {
			return ssoConnectFailed
		}
		return ssoConnected
	}
	return agg, connects
}

func TestCallToolInternal_SSOToolResolvesOnAFreshSessionWithoutAListing(t *testing.T) {
	agg, connects := newOnCallTestAggregator(t, "alpha", "beta")
	ctx := sessionContext("ext-fresh", "alice")

	result, err := agg.CallToolInternal(ctx, "x_alpha_op", map[string]any{})
	require.NoError(t, err, "a forwardToken server's tool resolves on the session's first call")
	require.Len(t, result.Content, 1)
	assert.Equal(t, "done", result.Content[0].(mcp.TextContent).Text)
	assert.Equal(t, 1, connects.count("alpha"), "the call connected the session to the tool's server")
	assert.Zero(t, connects.count("beta"), "a server that cannot own the name is not connected")

	_, err = agg.CallToolInternal(ctx, "x_alpha_op", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 1, connects.count("alpha"), "a session connected to the server is not connected again")
	assert.Equal(t, 2, connects.client.callCount)
}

func TestCallToolInternal_SSOServerInBackoffIsNotConnectedOnCall(t *testing.T) {
	agg, connects := newOnCallTestAggregator(t, "alpha")
	agg.ssoTracker.MarkSSOFailedWithReason("alice", "alpha", "exchange refused")

	_, err := agg.CallToolInternal(sessionContext("ext-backoff", "alice"), "x_alpha_op", map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool not found")
	assert.Zero(t, connects.count("alpha"), "a failing server retries on its backoff schedule, not on every call")
}

func TestCallToolInternal_UnknownNameConnectsNothing(t *testing.T) {
	agg, connects := newOnCallTestAggregator(t, "alpha")

	_, err := agg.CallToolInternal(sessionContext("ext-unknown", "alice"), "x_gamma_op", map[string]any{})
	require.Error(t, err)
	assert.Zero(t, connects.count("alpha"), "a name in no server's name space connects nothing")
}
