package aggregator

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
)

// githubUID is the UID the agent rigs give the github MCPServer.
const githubUID = "uid-github-1"

// agentRig is the gate rig with github's MCPServer carrying a UID, and a
// Session of agent kagent/alert-review whose binding carries settings.
func agentRig(t *testing.T) *gateRig {
	t.Helper()
	rig := newGateRig(t)
	manager := api.GetMCPServerManager().(*stubMCPServerManager)
	info := manager.servers["github"]
	info.UID = githubUID
	manager.servers["github"] = info
	return rig
}

func boundAgent(rig *gateRig, settings *api.AgentApproval) context.Context {
	settings.Agent = "kagent/alert-review"
	return api.WithAgentApproval(agentContext(rig, "alert-review-1"), settings)
}

var mergeGrant = api.AgentApprovalGrant{Entry: "tool:x_github_merge_pull_request", Server: "github", UID: githubUID, Tools: []string{"merge_pull_request"}}

// A connector admin's grant lets the agent run the tool the connector asks
// for; without the mark, or with a mark for another server instance, the
// same Allow gives way to the connector's Ask (PRD H16).
func TestAgentSettings_GrantLiftsTheConnectorsAsk(t *testing.T) {
	cases := []struct {
		name   string
		grants []api.AgentApprovalGrant
		runs   bool
	}{
		{"granted", []api.AgentApprovalGrant{mergeGrant}, true},
		{"not granted", nil, false},
		{"grant of a recreated server", []api.AgentApprovalGrant{{Entry: mergeGrant.Entry, Server: "github", UID: "uid-old", Tools: mergeGrant.Tools}}, false},
		{"grant that did not cover the tool", []api.AgentApprovalGrant{{Entry: mergeGrant.Entry, Server: "github", UID: githubUID, Tools: []string{"get_issue"}}}, false},
		{"grant without tools", []api.AgentApprovalGrant{{Entry: mergeGrant.Entry, Server: "github", UID: githubUID}}, false},
		{"grant of another entry", []api.AgentApprovalGrant{{Entry: "server:github", Server: "github", UID: githubUID, Tools: mergeGrant.Tools}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := agentRig(t)
			settings := &api.AgentApproval{Entries: []api.AgentApprovalEntry{
				{Entry: "server:github", Setting: api.ApprovalAllow},
				{Entry: "tool:x_github_merge_pull_request", Setting: api.ApprovalAllow},
			}, Grants: tc.grants}
			ctx := boundAgent(rig, settings)
			result, err := rig.server.CallToolInternal(ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
			require.NoError(t, err)
			if tc.runs {
				assert.False(t, result.IsError)
				assert.Equal(t, 1, rig.client.count("merge_pull_request"))
				return
			}
			approvalIDOf(t, result)
			assert.Zero(t, rig.client.count("merge_pull_request"))
		})
	}
}

// An agent's Block hides the tool and refuses it as not found; its Ask makes
// a tool the connector allows ask; an admin Block holds over an agent Allow.
func TestAgentSettings_TightenAndBlock(t *testing.T) {
	rig := agentRig(t)
	ctx := boundAgent(rig, &api.AgentApproval{Entries: []api.AgentApprovalEntry{
		{Entry: "tool:x_github_get_issue", Setting: api.ApprovalAsk},
		{Entry: "tool:x_github_merge_pull_request", Setting: api.ApprovalBlock},
		{Entry: "tool:x_github_delete_repository", Setting: api.ApprovalAllow},
	}, Grants: []api.AgentApprovalGrant{{Entry: "tool:x_github_delete_repository", Server: "github", UID: githubUID, Tools: []string{"delete_repository"}}}})

	_, err := rig.server.CallToolInternal(ctx, "x_github_merge_pull_request", map[string]any{})
	require.EqualError(t, err, "tool not found: x_github_merge_pull_request")
	_, err = rig.server.CallToolInternal(ctx, "x_github_delete_repository", map[string]any{})
	require.EqualError(t, err, "tool not found: x_github_delete_repository", "a grant never lifts a connector's Block")

	result, err := rig.server.CallToolInternal(ctx, "x_github_get_issue", map[string]any{"id": 1.0})
	require.NoError(t, err)
	id := approvalIDOf(t, result)
	request, err := rig.store.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalSourceAgent, request.Display.Source)
	assert.Equal(t, "kagent/alert-review", request.Agent, "the approval names and binds the agent")
	assert.Equal(t, "Agent kagent/alert-review wants to run Get issue in github.", request.Display.Message)
	assert.Zero(t, rig.client.count("get_issue"))

	tools := rig.server.applyApprovals(ctx, []mcp.Tool{
		approvalSoloTool("github", "get_issue"), approvalSoloTool("github", "merge_pull_request"), approvalSoloTool("github", "delete_repository"),
	})
	require.Len(t, tools, 1, "the agent's Block and the connector's Block are hidden")
	got, _ := approvalOf(t, tools, "x_github_get_issue")
	assert.Equal(t, api.ApprovalSettingAsk, got.Setting)
	assert.Equal(t, api.ApprovalSourceAgent, got.Source)
	assert.Equal(t, "tool:x_github_get_issue", got.Entry)
	require.NotNil(t, got.Admin)
	assert.Equal(t, api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceTool}, *got.Admin)
}

// The prompt names the agent by the display name its sender bound, marked
// like other agent-controlled text and capped; the technical name stays on
// the request and stands in when no display name is bound.
func TestAgentSettings_MessageUsesTheDisplayName(t *testing.T) {
	cases := []struct {
		name, display, message string
	}{
		{"display name", "Alert Review", "Alert Review wants to run Get issue in github."},
		{"fallback to the technical name", "", "Agent kagent/alert-review wants to run Get issue in github."},
		{"marked", "Alert\u202e Review\u200b", "Alert⟨U+202E⟩ Review⟨U+200B⟩ wants to run Get issue in github."},
		{"capped", strings.Repeat("x", api.MaxAgentDisplayName+20), strings.Repeat("x", api.MaxAgentDisplayName) + " wants to run Get issue in github."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := agentRig(t)
			ctx := boundAgent(rig, &api.AgentApproval{AgentDisplayName: tc.display, Entries: []api.AgentApprovalEntry{
				{Entry: "tool:x_github_get_issue", Setting: api.ApprovalAsk},
			}})
			result, err := rig.server.CallToolInternal(ctx, "x_github_get_issue", map[string]any{"id": 1.0})
			require.NoError(t, err)
			request, err := rig.store.Get(context.Background(), approvalIDOf(t, result))
			require.NoError(t, err)
			assert.Equal(t, tc.message, request.Display.Message)
			assert.Equal(t, "kagent/alert-review", request.Agent)
			assert.Equal(t, tc.display, request.AgentDisplayName)
		})
	}
}

// The default applies to tools no entry selects; the most specific entry
// wins over it and over less specific ones.
func TestAgentSettings_DefaultAndSpecificity(t *testing.T) {
	rig := agentRig(t)
	ctx := boundAgent(rig, &api.AgentApproval{
		Entries: []api.AgentApprovalEntry{{Entry: "server:github", Setting: api.ApprovalBlock}, {Entry: "tool:x_github_get_issue", Setting: api.ApprovalAllow}},
		Default: api.ApprovalAsk,
	})
	result, err := rig.server.CallToolInternal(ctx, "x_github_get_issue", map[string]any{})
	require.NoError(t, err)
	assert.False(t, result.IsError, "tool: beats server:")
	_, err = rig.server.CallToolInternal(ctx, "x_github_merge_pull_request", map[string]any{})
	require.EqualError(t, err, "tool not found: x_github_merge_pull_request")

	ctx = boundAgent(rig, &api.AgentApproval{Default: api.ApprovalAsk})
	result, err = rig.server.CallToolInternal(ctx, "x_github_get_issue", map[string]any{})
	require.NoError(t, err)
	approvalIDOf(t, result)
}

// A grant never lifts a floor or the governed default: the
// agent-manager tools stay governed for an agent with a granted Allow.
func TestAgentSettings_GrantNeverLiftsGovernance(t *testing.T) {
	rig, platform := governedRig(t, api.GovernanceGoverned)
	manager := api.GetMCPServerManager().(*stubMCPServerManager)
	info := manager.servers["agent-manager"]
	info.UID = "uid-am"
	info.Approval = &api.MCPServerApproval{Rule: api.ApprovalAsk}
	manager.servers["agent-manager"] = info
	ctx := boundAgent(rig, &api.AgentApproval{
		Entries: []api.AgentApprovalEntry{{Entry: "server:agent-manager", Setting: api.ApprovalAllow}},
		Grants:  []api.AgentApprovalGrant{{Entry: "server:agent-manager", Server: "agent-manager", UID: "uid-am", Tools: []string{"update_agent", "add_organization_member", "list_agents"}}},
	})
	for _, tool := range []string{"update_agent", "add_organization_member"} {
		result, err := rig.server.CallToolInternal(ctx, "x_agent-manager_"+tool, map[string]any{})
		require.NoError(t, err)
		approvalIDOf(t, result)
		assert.Zero(t, platform.count(tool), tool)
	}
	result, err := rig.server.CallToolInternal(ctx, "x_agent-manager_list_agents", map[string]any{})
	require.NoError(t, err)
	assert.False(t, result.IsError, "a safe-listed tool the grant lifts runs")
}

// An agent's settings reach core tools (only tightening) and workflow runs.
func TestAgentSettings_CoreToolsAndWorkflows(t *testing.T) {
	rig := agentRig(t)
	ctx := boundAgent(rig, &api.AgentApproval{Entries: []api.AgentApprovalEntry{{Entry: "tool:core_service_list", Setting: api.ApprovalBlock}}})
	assert.Equal(t, api.ApprovalSettingBlock, coreToolApproval(ctx, "core_service_list", nil).Setting)
	assert.NotEqual(t, api.ApprovalSettingBlock, coreToolApproval(ctx, "core_service_get", nil).Setting, "an unlisted core tool keeps what governance gives it")
	tools := rig.server.applyApprovals(ctx, []mcp.Tool{approvalCoreTool("core_service_list", toolset.OriginKindCore), approvalCoreTool("core_service_get", toolset.OriginKindCore)})
	require.Len(t, tools, 1)
	assert.Equal(t, "core_service_get", tools[0].Name)

	asks := boundAgent(rig, &api.AgentApproval{Entries: []api.AgentApprovalEntry{{Entry: "workflow:release", Setting: api.ApprovalAsk}}})
	got := withAgentWorkflowSetting(asks, api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnreviewed}, "release")
	assert.Equal(t, api.ApprovalSettingAsk, got.Setting)
	assert.Equal(t, "workflow:release", got.Entry)
}

// Reporting for an agent's settings (filter_tools agentTools) reports the
// floors as for an agent caller and keeps the agent's own Blocks listed;
// the connector's Block stays hidden. A person's request without settings
// is unchanged.
func TestAgentSettings_ReportingKeepsTheAgentsBlocks(t *testing.T) {
	rig := agentRig(t)
	report := api.WithAgentApproval(rig.ctx, &api.AgentApproval{Reporting: true, Entries: []api.AgentApprovalEntry{
		{Entry: "tool:x_github_get_issue", Setting: api.ApprovalBlock},
		{Entry: "tool:x_github_merge_pull_request", Setting: api.ApprovalAllow},
	}, Grants: []api.AgentApprovalGrant{mergeGrant}})
	listed := []mcp.Tool{approvalSoloTool("github", "get_issue"), approvalSoloTool("github", "merge_pull_request"), approvalSoloTool("github", "delete_repository")}
	tools := rig.server.applyApprovals(report, listed)
	require.Len(t, tools, 2)
	got, _ := approvalOf(t, tools, "x_github_get_issue")
	assert.Equal(t, api.ToolApproval{Setting: api.ApprovalSettingBlock, Source: api.ApprovalSourceAgent, Entry: "tool:x_github_get_issue",
		Admin: &api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceTool}}, got)
	got, _ = approvalOf(t, tools, "x_github_merge_pull_request")
	assert.Equal(t, api.ApprovalSettingAllow, got.Setting)
	assert.Equal(t, api.ApprovalSourceAgentException, got.Source)
	assert.Equal(t, api.ApprovalSettingAsk, got.Admin.Setting)

	assert.False(t, api.ReportingAsAgent(rig.ctx))

	// Reporting never decides a call: a call in that context asks.
	result, err := rig.server.CallToolInternal(api.WithAgentApproval(rig.ctx, &api.AgentApproval{Reporting: true}), "x_github_merge_pull_request", map[string]any{})
	require.NoError(t, err)
	approvalIDOf(t, result)
}

// The reason an agent gives with a call reaches the page; a person's does
// not, and it is never part of what is bound.
func TestAgentSettings_ReasonIsShownNotBound(t *testing.T) {
	rig := agentRig(t)
	ctx := api.WithAgentReason(boundAgent(rig, &api.AgentApproval{}), "Disk pressure is handled.")
	result, err := rig.server.CallToolInternal(ctx, "x_github_merge_pull_request", map[string]any{"pr": 7.0})
	require.NoError(t, err)
	id := approvalIDOf(t, result)
	request, err := rig.store.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "Disk pressure is handled.", request.Display.AgentSays)

	other := api.WithAgentReason(boundAgent(rig, &api.AgentApproval{}), "another reason")
	result, err = rig.server.CallToolInternal(other, "x_github_merge_pull_request", map[string]any{"pr": 7.0})
	require.NoError(t, err)
	assert.Equal(t, id, approvalIDOf(t, result), "the reason is not bound")

	person := api.WithAgentReason(rig.ctx, "a person's words")
	result, err = rig.server.CallToolInternal(person, "x_github_merge_pull_request", map[string]any{"pr": 8.0})
	require.NoError(t, err)
	request, err = rig.store.Get(context.Background(), approvalIDOf(t, result))
	require.NoError(t, err)
	assert.Empty(t, request.Display.AgentSays)
}
