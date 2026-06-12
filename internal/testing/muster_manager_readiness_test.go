package testing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
