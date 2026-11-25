package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
)

// governedRig is the gate rig plus agent-manager and kagent's session route,
// both governed, their connectors Allow all (as the platform charts ship
// them).
func governedRig(t *testing.T, mode string) (*gateRig, *countingMCPClient) {
	t.Helper()
	api.RegisterGovernance(api.Governance{Mode: mode, Readable: true, Servers: []api.GovernedServer{
		{Server: "agent-manager", Role: api.GovernanceRoleAgentManager, Safe: []string{"list_agents", "get_agent", "unknown_read"}},
		{Server: "kagent", Role: api.GovernanceRoleSessionManager, Safe: []string{"list_sessions"}},
	}})
	t.Cleanup(func() { api.RegisterGovernance(api.Governance{}) })
	rig := newGateRig(t)
	platform := &countingMCPClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{
		{Name: "list_agents"}, {Name: "get_agent"}, {Name: "update_agent"}, {Name: "start_session"},
		{Name: "add_organization_member"}, {Name: "brand_new_tool"},
	}}}
	kagent := &countingMCPClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: "list_sessions"}, {Name: "invoke_session"}, {Name: "fork_session"}}}}
	ctx := context.Background()
	require.NoError(t, rig.server.RegisterServer(ctx, ServerRegistration{Name: "agent-manager"}, platform))
	require.NoError(t, rig.server.RegisterServer(ctx, ServerRegistration{Name: "kagent"}, kagent))
	_ = rig.server.registry.GetAllTools()
	manager := api.GetMCPServerManager().(*stubMCPServerManager)
	allowAll := &api.MCPServerApproval{Rule: api.ApprovalAllow}
	manager.servers["agent-manager"] = api.MCPServerInfo{Name: "agent-manager", Type: "streamable-http", Approval: allowAll}
	manager.servers["kagent"] = api.MCPServerInfo{Name: "kagent", Type: "streamable-http", Approval: allowAll}
	return rig, platform
}

func agentContext(rig *gateRig, session string) context.Context {
	return api.WithSessionID(rig.ctx, api.ActorSessionPrefix+session)
}

// Regression of the PoC for M1-MU-S-02: on Governed an agent is asked before
// every tool of a governed server that is not on its safe list -- today's
// update_agent (the PRD's update_agent_access does not exist), kagent's
// invoke_session and fork_session, a tool the server adds later -- and
// before muster's own pause and delete; safe-listed reads run; a person is
// not asked.
func TestGovernance_GovernedServersAskByDefault(t *testing.T) {
	rig, platform := governedRig(t, api.GovernanceGoverned)
	asks := []string{"x_agent-manager_update_agent", "x_agent-manager_start_session", "x_agent-manager_brand_new_tool", "x_kagent_invoke_session", "x_kagent_fork_session"}
	for i, tool := range asks {
		result, err := rig.server.CallToolInternal(agentContext(rig, string(rune('a'+i))), tool, map[string]any{})
		require.NoError(t, err, tool)
		id := approvalIDOf(t, result)
		request, err := rig.store.Get(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, api.ApprovalSourceGovernance, request.Display.Source, tool)
	}
	assert.Zero(t, platform.count("update_agent")+platform.count("start_session")+platform.count("brand_new_tool"))

	for i, core := range []string{"core_service_stop", "core_mcpserver_delete", "core_service_start"} {
		result, err := rig.server.CallToolInternal(agentContext(rig, "core-"+string(rune('a'+i))), core, map[string]any{"name": "github"})
		require.NoError(t, err, core)
		approvalIDOf(t, result)
	}

	_, err := rig.server.CallToolInternal(agentContext(rig, "reads"), "x_agent-manager_list_agents", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 1, platform.count("list_agents"), "a safe-listed read runs")

	_, err = rig.server.CallToolInternal(rig.ctx, "x_agent-manager_update_agent", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 1, platform.count("update_agent"), "a person is not asked")
}

// On Trusted only the floors ask agents, in both modes.
func TestGovernance_TrustedAsksFloorsOnly(t *testing.T) {
	rig, platform := governedRig(t, api.GovernanceTrusted)
	_, err := rig.server.CallToolInternal(agentContext(rig, "a"), "x_agent-manager_update_agent", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 1, platform.count("update_agent"))

	result, err := rig.server.CallToolInternal(agentContext(rig, "b"), "x_agent-manager_add_organization_member", map[string]any{})
	require.NoError(t, err)
	request, err := rig.store.Get(context.Background(), approvalIDOf(t, result))
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalSourceBuiltin, request.Display.Source, "a floor asks in both modes")
}

// A workflow is no way around the default: its steps on a governed server
// ask an agent up front, a deployed workflow included.
func TestGovernance_WorkflowStepsOnGovernedServersAsk(t *testing.T) {
	rig, platform := governedRig(t, api.GovernanceGoverned)
	workflows := &fakeWorkflows{server: rig.server, defs: map[string]*api.Workflow{
		"widen": {Name: "widen", Steps: []api.WorkflowStep{{ID: "update", Tool: "x_agent-manager_update_agent", Args: map[string]any{}}}},
	}}
	previous := api.GetWorkflow()
	api.RegisterWorkflow(workflows)
	t.Cleanup(func() { api.RegisterWorkflow(previous) })

	result, err := rig.server.CallToolInternal(agentContext(rig, "wf"), "workflow_widen", map[string]any{})
	require.NoError(t, err)
	approvalIDOf(t, result)
	assert.Zero(t, platform.count("update_agent"))
}

// Listings say truthfully what a call will do for the caller: an agent sees
// the governed default and the floors (source governance or builtin), a
// person the connector's own setting; a workflow reports its strictest step
// (M1-MU-C-08).
func TestGovernance_ListingsReportForTheCaller(t *testing.T) {
	rig, _ := governedRig(t, api.GovernanceGoverned)
	workflows := &fakeWorkflows{server: rig.server, defs: map[string]*api.Workflow{
		"widen": {Name: "widen", Steps: []api.WorkflowStep{{ID: "read", Tool: "x_agent-manager_list_agents"}, {ID: "update", Tool: "x_agent-manager_update_agent"}}},
	}}
	previous := api.GetWorkflow()
	api.RegisterWorkflow(workflows)
	t.Cleanup(func() { api.RegisterWorkflow(previous) })

	catalogue := []mcp.Tool{
		approvalSoloTool("agent-manager", "update_agent"),
		approvalSoloTool("agent-manager", "list_agents"),
		approvalSoloTool("agent-manager", "add_organization_member"),
		approvalCoreTool("core_service_stop", toolset.OriginKindCore),
		approvalCoreTool("core_mcpserver_list", toolset.OriginKindCore),
		approvalCoreTool("workflow_widen", toolset.OriginKindWorkflow),
	}
	reported := func(ctx context.Context) map[string]api.ToolApproval {
		out := map[string]api.ToolApproval{}
		for _, tool := range rig.server.applyApprovals(ctx, append([]mcp.Tool(nil), catalogue...)) {
			approval, _ := api.ToolApprovalOf(tool)
			approval.Unavailable = ""
			out[tool.Name] = approval
		}
		return out
	}
	agent := reported(agentContext(rig, "listing"))
	assert.Equal(t, api.ApprovalSourceGovernance, agent["x_agent-manager_update_agent"].Source)
	assert.Equal(t, api.ApprovalSettingAsk, agent["x_agent-manager_update_agent"].Setting)
	assert.Equal(t, api.ApprovalSettingAllow, agent["x_agent-manager_list_agents"].Setting)
	assert.Equal(t, api.ApprovalSourceBuiltin, agent["x_agent-manager_add_organization_member"].Source)
	assert.Equal(t, api.ApprovalSourceGovernance, agent["core_service_stop"].Source)
	assert.Equal(t, api.ApprovalSettingAllow, agent["core_mcpserver_list"].Setting)
	assert.Equal(t, api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceGovernance}, agent["workflow_widen"], "a workflow reports its strictest step")

	person := reported(rig.ctx)
	assert.Equal(t, api.ApprovalSettingAllow, person["x_agent-manager_update_agent"].Setting)
	assert.Equal(t, api.ApprovalSettingAllow, person["core_service_stop"].Setting)
	assert.Equal(t, api.ApprovalSettingAllow, person["workflow_widen"].Setting)
}

// A safe-list entry that names none of the governed server's tools is
// reported, not ignored.
func TestGovernance_UnmatchedSafeListIsReported(t *testing.T) {
	rig, _ := governedRig(t, api.GovernanceGoverned)
	rig.server.applyApprovals(rig.ctx, []mcp.Tool{approvalSoloTool("agent-manager", "list_agents")})
	assert.Equal(t, map[string][]string{"agent-manager": {"unknown_read"}}, rig.server.UnmatchedSafeLists())
}
