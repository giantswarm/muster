package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
)

// stdioServerInfo is a definition that would be started as a subprocess of the
// muster process.
func stdioServerInfo() api.MCPServerInfo {
	return api.MCPServerInfo{
		Name:      "stdio-mcp",
		Type:      "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
		AutoStart: true,
	}
}

// TestKubernetesModeRefusesStdioAtBoot pins the path the reconciler does not
// cover: the orchestrator creates and starts services for auto-start
// definitions on its own, so a stdio CR would otherwise spawn at boot
// regardless of what admission or reconciliation decided (issue #1067).
func TestKubernetesModeRefusesStdioAtBoot(t *testing.T) {
	manager := &mockMCPServerManager{servers: map[string]api.MCPServerInfo{"stdio-mcp": stdioServerInfo()}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	o := New(Config{Aggregator: config.AggregatorConfig{}, KubernetesMode: true})
	require.NoError(t, o.Start(context.Background()))
	t.Cleanup(func() { _ = o.Stop() })

	_, exists := o.registry.Get("stdio-mcp")
	assert.False(t, exists, "a refused stdio definition must not be registered as a service")
}

// TestKubernetesModeRefusesStdioOnLazyRegistration covers the runtime path:
// StartService lazily registers definitions that appeared after boot, and must
// refuse a stdio one with the policy error instead of spawning it.
func TestKubernetesModeRefusesStdioOnLazyRegistration(t *testing.T) {
	manager := &mockMCPServerManager{servers: map[string]api.MCPServerInfo{}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	o := New(Config{Aggregator: config.AggregatorConfig{}, KubernetesMode: true})
	require.NoError(t, o.Start(context.Background()))
	t.Cleanup(func() { _ = o.Stop() })

	manager.servers["stdio-mcp"] = stdioServerInfo()

	err := o.StartService("stdio-mcp")
	require.Error(t, err)
	assert.ErrorIs(t, err, api.ErrStdioNotAllowedInKubernetesMode)

	_, exists := o.registry.Get("stdio-mcp")
	assert.False(t, exists, "a refused stdio definition must not be registered as a service")
}

// TestFilesystemModeStillRegistersStdio pins the local-CLI behavior: the same
// definition is registered and started as before.
func TestFilesystemModeStillRegistersStdio(t *testing.T) {
	manager := &mockMCPServerManager{servers: map[string]api.MCPServerInfo{}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	o := New(Config{Aggregator: config.AggregatorConfig{}})
	require.NoError(t, o.Start(context.Background()))
	t.Cleanup(func() { _ = o.Stop() })

	manager.servers["stdio-mcp"] = stdioServerInfo()

	// `echo` is not an MCP server, so the handshake fails — but registration
	// must happen, which is what the gate would have prevented.
	_ = o.StartService("stdio-mcp")

	_, exists := o.registry.Get("stdio-mcp")
	assert.True(t, exists, "filesystem mode must keep registering stdio services")
}
