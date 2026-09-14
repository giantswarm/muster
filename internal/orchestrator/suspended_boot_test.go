package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
)

// remoteServerInfo is an auto-start remote definition whose endpoint is a
// closed port: a start attempt fails, but registration is what these tests
// observe.
func remoteServerInfo(name string, suspended bool) api.MCPServerInfo {
	return api.MCPServerInfo{
		Name:      name,
		Type:      "streamable-http",
		AutoStart: true,
		Suspended: suspended,
		URL:       "http://127.0.0.1:1",
		Timeout:   1,
	}
}

// TestBootSkipsSuspendedAutoStartMCPServer pins the path the reconciler does
// not cover: the orchestrator's boot pass creates and starts services for
// auto-start definitions on its own, so a definition with spec.suspended=true
// was started at every boot and stopped again by the reconciler's first pass
// (issue #1216). A suspended definition is neither registered nor started; a
// non-suspended one next to it still is.
func TestBootSkipsSuspendedAutoStartMCPServer(t *testing.T) {
	manager := &mockMCPServerManager{servers: map[string]api.MCPServerInfo{
		"suspended-mcp": remoteServerInfo("suspended-mcp", true),
		"active-mcp":    remoteServerInfo("active-mcp", false),
	}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	o := New(Config{Aggregator: config.AggregatorConfig{}})
	require.NoError(t, o.Start(context.Background()))
	t.Cleanup(func() { _ = o.Stop() })

	_, exists := o.registry.Get("suspended-mcp")
	assert.False(t, exists, "a suspended definition must not be registered as a service at boot")

	_, exists = o.registry.Get("active-mcp")
	assert.True(t, exists, "a non-suspended auto-start definition must still be registered at boot")
}

// TestSuspendedAtBootResumesThroughStartService covers the resume that follows
// such a boot: once spec.suspended goes back to false the reconciler calls
// StartService, which registers the definition lazily and starts it -- the
// boot-time skip must not leave the server unstartable.
func TestSuspendedAtBootResumesThroughStartService(t *testing.T) {
	manager := &mockMCPServerManager{servers: map[string]api.MCPServerInfo{
		"suspended-mcp": remoteServerInfo("suspended-mcp", true),
	}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	o := New(Config{Aggregator: config.AggregatorConfig{}})
	require.NoError(t, o.Start(context.Background()))
	t.Cleanup(func() { _ = o.Stop() })

	_, exists := o.registry.Get("suspended-mcp")
	require.False(t, exists, "precondition: the suspended definition is not registered at boot")

	manager.servers["suspended-mcp"] = remoteServerInfo("suspended-mcp", false)

	// The start itself fails (nothing is listening), but the service must be
	// registered and no longer unknown to the orchestrator.
	if err := o.StartService("suspended-mcp"); err != nil {
		assert.NotContains(t, err.Error(), "service suspended-mcp not found")
	}
	_, exists = o.registry.Get("suspended-mcp")
	assert.True(t, exists, "the resumed definition must be registered by StartService")
}
