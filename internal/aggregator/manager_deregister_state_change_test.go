package aggregator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeregisterUnlessSessionAuth_KeepsPendingAuthEntry pins the registry-level
// guard behind the event handler's deregistration. The entry is registered
// BEFORE the deregistration is timestamped — the order seen in CI when the
// auth-required hook lands between the handler's auth_required check and the
// removal — so the RegisteredAt guard alone would delete it.
func TestDeregisterUnlessSessionAuth_KeepsPendingAuthEntry(t *testing.T) {
	reg := NewServerRegistry("x")
	registerPendingAuthServer(t, reg, "server-alpha")
	requestedAt := time.Now().Add(time.Second) // strictly after the registration

	removed, err := reg.DeregisterUnlessSessionAuth("server-alpha", requestedAt)
	require.NoError(t, err)
	assert.False(t, removed)
	_, exists := reg.GetServerInfo("server-alpha")
	assert.True(t, exists, "a pending-auth entry must survive a state-change-driven deregistration")

	// The unconditional path still removes it (service deleted, prune).
	require.NoError(t, reg.DeregisterRequestedAt("server-alpha", requestedAt))
	_, exists = reg.GetServerInfo("server-alpha")
	assert.False(t, exists)
}

func TestDeregisterUnlessSessionAuth_RemovesGlobalEntry(t *testing.T) {
	reg := NewServerRegistry("x")
	client := &mockMCPClient{}
	require.NoError(t, reg.Register(t.Context(), ServerRegistration{Name: "plain", ToolPrefix: "plain"}, client))

	removed, err := reg.DeregisterUnlessSessionAuth("plain", time.Now().Add(time.Second))
	require.NoError(t, err)
	assert.True(t, removed)
	_, exists := reg.GetServerInfo("plain")
	assert.False(t, exists)

	_, err = reg.DeregisterUnlessSessionAuth("plain", time.Now())
	assert.Error(t, err, "a missing entry still reports not found")
}

// TestDeregisterOnStateChange_KeepsPendingAuth covers the manager callback the
// event handler runs: a pending-auth entry is kept, a global one removed, and
// the unconditional callback used for vanished services removes both.
func TestDeregisterOnStateChange_KeepsPendingAuth(t *testing.T) {
	reg := NewServerRegistry("x")
	registerPendingAuthServer(t, reg, "server-alpha")
	require.NoError(t, reg.Register(t.Context(), ServerRegistration{Name: "plain", ToolPrefix: "plain"}, &mockMCPClient{}))
	am := &AggregatorManager{
		aggregatorServer: &AggregatorServer{registry: reg},
		serviceRegistry:  newStubServiceRegistry("server-alpha", "plain"),
	}

	require.NoError(t, am.deregisterOnStateChange("server-alpha"))
	info, exists := reg.GetServerInfo("server-alpha")
	require.True(t, exists, "the event handler must not drop a pending-auth entry")
	assert.True(t, info.RequiresSessionAuth())

	require.NoError(t, am.deregisterOnStateChange("plain"))
	_, exists = reg.GetServerInfo("plain")
	assert.False(t, exists)

	require.NoError(t, am.deregisterSingleServer("server-alpha"))
	_, exists = reg.GetServerInfo("server-alpha")
	assert.False(t, exists, "the unconditional path (service gone) still removes pending-auth entries")
}
