package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
)

// TestAggregatorServer_CallToolInternal_FamilyToolWithoutListing is the
// regression guard for #1204: a session that is authenticated to family
// members must be able to call a family tool without having listed tools.
//
// The family routing index is process-global and in memory, and tools of
// members that require per-session authentication enter it only when a
// session authenticated to them lists tools. A muster restart therefore
// left every session that held on across the rollout with "tool not found"
// for each family tool -- their tokens and cached capabilities had survived
// in the session stores, the index had not -- until they listed again.
func TestAggregatorServer_CallToolInternal_FamilyToolWithoutListing(t *testing.T) {
	const (
		sessionID = "session-1204"
		memberA   = "mc1-mcp-kubernetes"
		memberB   = "mc2-mcp-kubernetes"
		toolName  = "x_kubernetes_list_pods"
	)
	ctx := api.WithSessionID(context.Background(), sessionID)
	listPods := mcp.Tool{Name: "list_pods", Description: "List pods"}

	// newRestartedAggregator models the process right after a muster restart:
	// a fresh registry in which the family members are registered pending
	// auth (their 401 arrives before any session talks to them), while the
	// session's authentication and cached capabilities -- the state the
	// session stores persist -- are intact. Nothing has listed tools yet, so
	// the routing index is empty. The session's pooled connection to member A
	// stands for the connection the first call would otherwise establish.
	newRestartedAggregator := func(t *testing.T) (*AggregatorServer, *recordingMCPClient) {
		t.Helper()
		a := NewAggregatorServer(AggregatorConfig{Host: "localhost", Port: 0}, nil)
		registerAuthFamilyMember(t, a.registry, memberA, "kubernetes", "management_cluster")
		registerAuthFamilyMember(t, a.registry, memberB, "kubernetes", "management_cluster")
		for _, member := range []string{memberA, memberB} {
			require.NoError(t, a.capabilityStore.Set(ctx, sessionID, member,
				&oauthstore.Capabilities{Tools: []mcp.Tool{listPods}}))
			require.NoError(t, a.authStore.MarkAuthenticated(ctx, sessionID, member))
		}
		client := &recordingMCPClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{listPods}}}
		require.NoError(t, client.Initialize(ctx))
		a.connPool.Put(sessionID, memberA, client)
		require.False(t, a.registry.IsFamilyTool(toolName),
			"precondition: no listing has filled the routing index")
		return a, client
	}

	t.Run("the call routes to the selected member without a prior list_tools", func(t *testing.T) {
		a, client := newRestartedAggregator(t)

		_, err := a.CallToolInternal(ctx, toolName, map[string]any{
			"management_cluster": memberA,
			"namespace":          "default",
		})
		require.NoError(t, err)
		assert.Equal(t, "list_pods", client.lastName)
		assert.Equal(t, map[string]any{"namespace": "default"}, client.lastArgs,
			"the routing arg is stripped, as on the listed path")
		assert.True(t, a.registry.IsFamilyTool(toolName),
			"the call filled the routing index from the session's view")
		assert.Equal(t, []string{memberA, memberB}, a.registry.GetToolServerNames(toolName))
	})

	t.Run("a missing routing arg is reported as such, not as tool not found", func(t *testing.T) {
		a, _ := newRestartedAggregator(t)

		_, err := a.CallToolInternal(ctx, toolName, map[string]any{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"management_cluster" parameter is required`)
	})

	t.Run("a name the family does not offer names the family and the members the session sees", func(t *testing.T) {
		a, _ := newRestartedAggregator(t)

		_, err := a.CallToolInternal(ctx, "x_kubernetes_list_podz", map[string]any{"management_cluster": memberA})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `not exposed under family "kubernetes"`)
		assert.Contains(t, err.Error(), memberA)
		assert.Contains(t, err.Error(), memberB)
		assert.NotContains(t, err.Error(), "tool not found")
	})

	t.Run("a session connected to no member is pointed at core_auth_login", func(t *testing.T) {
		a, _ := newRestartedAggregator(t)
		strangerCtx := api.WithSessionID(context.Background(), "session-without-access")

		_, err := a.CallToolInternal(strangerCtx, toolName, map[string]any{"management_cluster": memberA})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `name space of family "kubernetes"`)
		assert.Contains(t, err.Error(), "connected to none of its members")
		assert.Contains(t, err.Error(), "core_auth_login")
		assert.NotContains(t, err.Error(), "tool not found")
		assert.False(t, a.registry.IsFamilyTool(toolName),
			"a session without capabilities contributes nothing to the index")
	})

	t.Run("a name outside every family still reports tool not found", func(t *testing.T) {
		a, _ := newRestartedAggregator(t)

		_, err := a.CallToolInternal(ctx, "x_storage_list_volumes", map[string]any{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tool not found: x_storage_list_volumes")
	})
}
