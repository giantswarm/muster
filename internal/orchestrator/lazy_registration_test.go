package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
)

// mockMCPServerManager implements api.MCPServerManagerHandler for testing
// lazy registration of MCPServer definitions created after orchestrator boot.
type mockMCPServerManager struct {
	servers map[string]api.MCPServerInfo
}

func (m *mockMCPServerManager) ListMCPServers() []api.MCPServerInfo {
	servers := make([]api.MCPServerInfo, 0, len(m.servers))
	for _, info := range m.servers {
		servers = append(servers, info)
	}
	return servers
}

func (m *mockMCPServerManager) GetMCPServer(name string) (*api.MCPServerInfo, error) {
	info, ok := m.servers[name]
	if !ok {
		return nil, api.NewMCPServerNotFoundError(name)
	}
	return &info, nil
}

func (m *mockMCPServerManager) GetTools() []api.ToolMetadata { return nil }

func (m *mockMCPServerManager) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	return nil, nil
}

// TestStartServiceLazilyRegistersRuntimeMCPServer reproduces issue #680:
// an MCPServer definition that appears after orchestrator boot (e.g. a CR
// applied at runtime) must be registered lazily by StartService instead of
// failing with "service not found" until the process restarts.
func TestStartServiceLazilyRegistersRuntimeMCPServer(t *testing.T) {
	manager := &mockMCPServerManager{servers: map[string]api.MCPServerInfo{}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	o := New(Config{Aggregator: config.AggregatorConfig{}})
	require.NoError(t, o.Start(context.Background()))
	t.Cleanup(func() { _ = o.Stop() })

	// Simulate a CR applied after boot: the definition exists in the manager
	// but was never registered in the orchestrator's service registry.
	manager.servers["new-mcp"] = api.MCPServerInfo{
		Name:      "new-mcp",
		Type:      "streamable-http",
		AutoStart: true,
		URL:       "http://127.0.0.1:1", // closed port: start fails, registration must still happen
		Timeout:   1,
	}

	err := o.StartService("new-mcp")

	// The start attempt itself fails (nothing is listening), but the service
	// must no longer be unknown to the orchestrator.
	if err != nil {
		assert.NotContains(t, err.Error(), "service new-mcp not found")
	}
	_, exists := o.registry.Get("new-mcp")
	assert.True(t, exists, "service must be registered in the registry after StartService")
}

// TestStartServiceUnknownServiceStillErrors verifies that StartService still
// fails cleanly for names that have no service and no MCPServer definition.
func TestStartServiceUnknownServiceStillErrors(t *testing.T) {
	manager := &mockMCPServerManager{servers: map[string]api.MCPServerInfo{}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	o := New(Config{Aggregator: config.AggregatorConfig{}})
	require.NoError(t, o.Start(context.Background()))
	t.Cleanup(func() { _ = o.Stop() })

	err := o.StartService("does-not-exist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
