package testing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReadinessTimeoutConfiguration verifies that the readiness deadline is
// taken from the configuration and that the zero value falls back to the
// default, so callers that don't care keep the historical behavior.
func TestReadinessTimeoutConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured time.Duration
		want       time.Duration
	}{
		{name: "zero selects default", configured: 0, want: defaultReadinessTimeout},
		{name: "negative selects default", configured: -time.Second, want: defaultReadinessTimeout},
		{name: "explicit value wins", configured: 60 * time.Second, want: 60 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, err := NewMusterInstanceManagerWithConfig(false, 18000, NewSilentLogger(false, false), false, tc.configured)
			require.NoError(t, err)
			impl, ok := mgr.(*musterInstanceManager)
			require.True(t, ok)
			t.Cleanup(func() { _ = impl.Cleanup() })
			require.Equal(t, tc.want, impl.readinessTimeout)
		})
	}
}

func TestMCPServerStateIsReady(t *testing.T) {
	for _, state := range []string{"Running", "Connected", "Auth Required"} {
		require.True(t, mcpServerStateIsReady(state), "state %q must be ready", state)
	}
	for _, state := range []string{"", "Starting", "Connecting", "Disconnected", "Stopped", "Failed"} {
		require.False(t, mcpServerStateIsReady(state), "state %q must not be ready", state)
	}
}

func TestFindMissingMCPServers(t *testing.T) {
	manager := &musterInstanceManager{}

	missing := manager.findMissingMCPServers(
		[]string{"ready-server", "no-state-server", "connecting-server", "failed-server", "absent-server"},
		map[string]string{
			"ready-server":      "Auth Required",
			"no-state-server":   "",
			"connecting-server": "Connecting",
			"failed-server":     "Failed",
		},
	)

	require.Equal(t, []string{
		"no-state-server (no state reported)",
		"connecting-server (state: Connecting)",
		"failed-server (state: Failed)",
		"absent-server",
	}, missing)
}
