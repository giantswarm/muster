package mcpserver

import (
	"context"
	"testing"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/events"
	"github.com/giantswarm/muster/internal/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// remoteDefinition is the remote counterpart of stdioDefinition (stdio_mode_test.go).
func remoteDefinition() *api.MCPServer {
	return &api.MCPServer{Name: "remote-server", Type: api.MCPServerTypeStreamableHTTP, URL: "http://127.0.0.1:1/mcp"}
}

// stopFrom puts a fresh service into state, stops it and reports the state it
// settled in, how many state transitions the stop caused and how many
// MCPServerStopped events it generated.
func stopFrom(t *testing.T, def *api.MCPServer, state services.ServiceState) (services.ServiceState, int, int) {
	t.Helper()
	cm := &countingEventManager{counts: map[string]int{}}
	api.RegisterEventManager(cm)
	t.Cleanup(func() { api.RegisterEventManager(nil) })

	svc, err := NewService(def)
	require.NoError(t, err)
	svc.UpdateState(state, services.HealthUnknown, nil)

	transitions := 0
	svc.SetStateChangeCallback(func(string, services.ServiceState, services.ServiceState, services.HealthStatus, error) {
		transitions++
	})

	require.NoError(t, svc.Stop(context.Background()))
	return svc.GetState(), transitions, cm.count(string(events.ReasonMCPServerStopped))
}

// TestStopAlreadyStoppedIsSilent: Stop is a no-op -- no state write, no
// MCPServerStopped event -- when the service already sits where its stop
// settles: Stopped for a local server, Disconnected for a remote one. The
// reconciler's suspend path used to call Stop on a Disconnected remote server
// on every resync tick and the event was re-emitted each time (issue #1212).
func TestStopAlreadyStoppedIsSilent(t *testing.T) {
	cases := []struct {
		name  string
		def   *api.MCPServer
		state services.ServiceState
	}{
		{name: "local server in Stopped", def: stdioDefinition(), state: services.StateStopped},
		{name: "remote server in Disconnected", def: remoteDefinition(), state: services.StateDisconnected},
		{name: "remote server in Stopped", def: remoteDefinition(), state: services.StateStopped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, transitions, stoppedEvents := stopFrom(t, tc.def, tc.state)
			assert.Equal(t, tc.state, state, "the state must be left alone")
			assert.Zero(t, transitions, "an already stopped service must not announce a state change")
			assert.Zero(t, stoppedEvents, "an already stopped service must not emit MCPServerStopped again")
		})
	}
}

// TestStopSettlesIdleServiceWithOneEvent pins the other branch: a service that
// is neither running nor already stopped is moved to its stopped state --
// Disconnected for a remote server, Stopped for a local one -- with exactly one
// MCPServerStopped event.
func TestStopSettlesIdleServiceWithOneEvent(t *testing.T) {
	cases := []struct {
		name  string
		def   *api.MCPServer
		state services.ServiceState
		want  services.ServiceState
	}{
		{name: "local server in Unknown", def: stdioDefinition(), state: services.StateUnknown, want: services.StateStopped},
		{name: "remote server in Unknown", def: remoteDefinition(), state: services.StateUnknown, want: services.StateDisconnected},
		{name: "remote server in Unreachable", def: remoteDefinition(), state: services.StateUnreachable, want: services.StateDisconnected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, transitions, stoppedEvents := stopFrom(t, tc.def, tc.state)
			assert.Equal(t, tc.want, state)
			assert.Equal(t, 1, transitions)
			assert.Equal(t, 1, stoppedEvents)
		})
	}
}
