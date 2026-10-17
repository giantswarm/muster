package aggregator

import (
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// A backend whose health probes fail is moved to Failed with a reconnect due;
// the state-change handler removes its registry entry, and the reconnect
// registers it again with a new client. A call in that window used to be
// answered "server not found", as if the backend had never existed.

func setReconnectWait(t *testing.T, d time.Duration) {
	t.Helper()
	previous := reconnectWait
	reconnectWait = d
	t.Cleanup(func() { reconnectWait = previous })
}

func newReconnectTestServer(t *testing.T) (*AggregatorServer, string) {
	t.Helper()
	server, err := NewAggregatorServer(t.Context(), AggregatorConfig{Host: "localhost", Port: 0}, nil)
	require.NoError(t, err)
	require.NoError(t, server.RegisterServer(t.Context(), ServerRegistration{Name: "pagerduty", ToolPrefix: "pd"}, pagerdutyBackend()))
	return server, server.registry.ExposedToolName("pagerduty", "list_incidents")
}

func pagerdutyBackend() *mockMCPClient {
	return &mockMCPClient{tools: []mcp.Tool{{Name: "list_incidents"}}}
}

func TestCallDuringReconnect_WaitsForReRegistration(t *testing.T) {
	setReconnectWait(t, time.Minute)
	server, exposed := newReconnectTestServer(t)

	removed, err := server.DeregisterServerOnStateChange("pagerduty", true)
	require.NoError(t, err)
	require.True(t, removed)

	called := make(chan error, 1)
	go func() {
		_, err := server.CallToolInternal(t.Context(), exposed, nil)
		called <- err
	}()

	require.NoError(t, server.RegisterServer(t.Context(), ServerRegistration{Name: "pagerduty", ToolPrefix: "pd"}, pagerdutyBackend()))
	require.NoError(t, <-called, "the call is dispatched to the reconnected backend")
	assert.Nil(t, server.reconnects.lookup("pagerduty"), "the re-registration ends the tracking")
}

func TestCallDuringReconnect_RetryableAnswerAfterWait(t *testing.T) {
	setReconnectWait(t, 0)
	server, exposed := newReconnectTestServer(t)

	_, err := server.DeregisterServerOnStateChange("pagerduty", true)
	require.NoError(t, err)
	// The reconnect's own state changes (starting, another failed attempt)
	// find no entry and keep the tracking.
	_, err = server.DeregisterServerOnStateChange("pagerduty", true)
	require.Error(t, err)

	_, err = server.CallToolInternal(t.Context(), exposed, nil)
	var reconnecting *reconnectingError
	require.ErrorAs(t, err, &reconnecting)
	assert.NotContains(t, err.Error(), "server not found")
	assert.Contains(t, err.Error(), "server pagerduty is reconnecting")
	assert.Contains(t, err.Error(), "retry")
}

func TestCallAfterStop_ServerNotFound(t *testing.T) {
	setReconnectWait(t, time.Minute)
	server, exposed := newReconnectTestServer(t)

	_, err := server.DeregisterServerOnStateChange("pagerduty", false)
	require.NoError(t, err)

	_, err = server.CallToolInternal(t.Context(), exposed, nil)
	require.EqualError(t, err, "server not found: pagerduty", "a stopped server is not waited for")
}

func TestCallDuringReconnect_RemovalEndsTheWait(t *testing.T) {
	setReconnectWait(t, time.Minute)
	server, exposed := newReconnectTestServer(t)

	_, err := server.DeregisterServerOnStateChange("pagerduty", true)
	require.NoError(t, err)

	called := make(chan error, 1)
	go func() {
		_, err := server.CallToolInternal(t.Context(), exposed, nil)
		called <- err
	}()

	// The service is deleted while it reconnects: nothing to deregister, but
	// the waiting call learns the server is gone.
	require.Error(t, server.DeregisterServer("pagerduty"))
	require.EqualError(t, <-called, "server not found: pagerduty")
}

func TestReconnectTracker_KeepsFirstLoss(t *testing.T) {
	var tracker reconnectTracker
	tracker.lost("a")
	first := tracker.lookup("a")
	require.NotNil(t, first)
	tracker.lost("a")
	assert.Same(t, first, tracker.lookup("a"))

	tracker.settled("a")
	assert.Nil(t, tracker.lookup("a"))
	_, open := <-first.back
	assert.False(t, open, "settling wakes the waiters")
	tracker.settled("a") // settling an untracked server is a no-op
}

// stateServiceRegistry answers Get with a service in a given state.
type stateServiceRegistry struct {
	stubServiceRegistry
	states map[string]api.ServiceState
}

type stateService struct {
	api.ServiceInfo
	state api.ServiceState
}

func (s stateService) GetState() api.ServiceState { return s.state }

func (r *stateServiceRegistry) Get(name string) (api.ServiceInfo, bool) {
	state, ok := r.states[name]
	if !ok {
		return nil, false
	}
	return stateService{state: state}, true
}

func TestServiceReconnecting(t *testing.T) {
	am := &AggregatorManager{serviceRegistry: &stateServiceRegistry{states: map[string]api.ServiceState{
		"failed":   api.StateFailed,
		"starting": api.StateStarting,
		"stopped":  api.StateStopped,
		"stopping": api.StateStopping,
	}}}
	assert.True(t, am.serviceReconnecting("failed"), "a server whose probes failed has a reconnect due")
	assert.True(t, am.serviceReconnecting("starting"), "a starting server is that reconnect")
	assert.False(t, am.serviceReconnecting("stopped"))
	assert.False(t, am.serviceReconnecting("stopping"))
	assert.False(t, am.serviceReconnecting("deleted"))
	assert.False(t, (&AggregatorManager{}).serviceReconnecting("failed"))
}

func TestReconnectingError_IsNotNotFound(t *testing.T) {
	err := error(&reconnectingError{server: "pagerduty", since: 1500 * time.Millisecond})
	assert.False(t, api.IsNotFound(err))
	assert.Equal(t, "server pagerduty is reconnecting: its connection was lost 2s ago and muster is re-establishing it; retry the call shortly", err.Error())
}
