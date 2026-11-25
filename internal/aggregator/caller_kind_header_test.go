package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
)

// headerProbe is an MCP client whose transport would send the call's
// headers: it records what the call's context carries.
type headerProbe struct {
	countingMCPClient
	seen map[string]string
}

func (p *headerProbe) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	p.seen = internalmcp.CallHeadersForTest(ctx)
	return p.countingMCPClient.CallTool(ctx, name, args)
}

// X-Muster-Caller-Kind reaches a server the governance configuration names
// for agent callers only, on the dispatch path every connection type shares
// (M1-MU-S-05, M1-MU-C-20).
func TestCallerKindHeader(t *testing.T) {
	api.RegisterGovernance(api.Governance{Mode: api.GovernanceTrusted, Readable: true,
		Servers: []api.GovernedServer{{Server: "agent-manager", Role: api.GovernanceRoleAgentManager}}})
	t.Cleanup(func() { api.RegisterGovernance(api.Governance{}) })

	rig := newGateRig(t)
	probe := &headerProbe{countingMCPClient: countingMCPClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: "get_agent"}}}}}
	require.NoError(t, rig.server.RegisterServer(context.Background(), ServerRegistration{Name: "agent-manager"}, probe))
	_ = rig.server.registry.GetAllTools()
	manager := api.GetMCPServerManager().(*stubMCPServerManager)
	manager.servers["agent-manager"] = api.MCPServerInfo{Name: "agent-manager", Type: "streamable-http"}

	agent := api.WithSessionID(rig.ctx, api.ActorSessionPrefix+"session-1")
	_, err := rig.server.CallToolInternal(agent, "x_agent-manager_get_agent", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{HeaderCallerKind: "agent"}, probe.seen, "an actor JWT caller is an agent")

	delegated := api.WithDelegatedCaller(rig.ctx, api.DelegatedCaller{Subject: "alice"})
	_, err = rig.server.CallToolInternal(delegated, "x_agent-manager_get_agent", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{HeaderCallerKind: "agent"}, probe.seen, "a delegated caller is an agent")

	_, err = rig.server.CallToolInternal(rig.ctx, "x_agent-manager_get_agent", map[string]any{})
	require.NoError(t, err)
	assert.Empty(t, probe.seen, "a person sends none")

	assert.Equal(t, agent, withCallerKind(agent, "github"), "only servers the configuration names")
}

// A governance-named server reached over stdio cannot carry the header, so
// agent callers are refused there.
func TestCallerKindHeader_StdioRefusesAgents(t *testing.T) {
	api.RegisterGovernance(api.Governance{Mode: api.GovernanceTrusted, Readable: true,
		Servers: []api.GovernedServer{{Server: "github", Role: api.GovernanceRoleAgentManager}}})
	t.Cleanup(func() { api.RegisterGovernance(api.Governance{}) })
	rig := newGateRig(t)
	manager := api.GetMCPServerManager().(*stubMCPServerManager)
	info := manager.servers["github"]
	info.Type = "stdio"
	manager.servers["github"] = info

	agent := api.WithSessionID(rig.ctx, api.ActorSessionPrefix+"session-1")
	_, err := rig.server.CallToolInternal(agent, "x_github_get_issue", map[string]any{})
	require.ErrorContains(t, err, "over stdio")
	_, err = rig.server.CallToolInternal(rig.ctx, "x_github_get_issue", map[string]any{})
	require.NoError(t, err, "a person is not refused")
}
