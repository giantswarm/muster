package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// platform is a governed platform: agent-manager with a safe list of reads,
// kagent's session route and the model manager with roles.
func platform(mode string) Governance {
	return Governance{Mode: mode, Readable: true, Servers: []GovernedServer{
		{Server: "agent-manager", Role: GovernanceRoleAgentManager, Safe: []string{"list_agents", "get_agent"}},
		{Server: "kagent", Role: GovernanceRoleSessionManager, Safe: []string{"list_sessions"}},
		{Server: "model-manager", Role: GovernanceRoleModelManager, Safe: []string{"list_models"}},
	}}
}

var (
	askGovernance = ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceGovernance}
	askFloor      = ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceBuiltin}
)

// On Governed a governed server asks agents before every tool that is not on
// its safe list -- a renamed or new tool included -- and its role's floors
// ask in both modes (M1-MU-S-02).
func TestGovernance_ServerEntry(t *testing.T) {
	governed, trusted := platform(GovernanceGoverned), platform(GovernanceTrusted)
	for _, tc := range []struct {
		name, server, tool  string
		governed, trusted   ToolApproval
		applyG, applyT, ref bool
	}{
		{name: "safe-listed read", server: "agent-manager", tool: "list_agents"},
		{name: "not listed: today's update_agent", server: "agent-manager", tool: "update_agent", governed: askGovernance, applyG: true},
		{name: "renamed tool", server: "agent-manager", tool: "list_agents_v2", governed: askGovernance, applyG: true},
		{name: "new tool", server: "agent-manager", tool: "update_agent_access", governed: askGovernance, applyG: true},
		{name: "kagent invoke_session", server: "kagent", tool: "invoke_session", governed: askGovernance, applyG: true},
		{name: "kagent fork_session", server: "kagent", tool: "fork_session", governed: askGovernance, applyG: true},
		{name: "floor, both modes", server: "agent-manager", tool: "add_organization_member", governed: askFloor, trusted: askFloor, applyG: true, applyT: true},
		{name: "model floor", server: "model-manager", tool: "add_backend", governed: askFloor, trusted: askFloor, applyG: true, applyT: true},
		{name: "unknown server", server: "github", tool: "merge_pull_request"},
		{name: "floor name on an unnamed server of a held role: asked on governed", server: "customer-models", tool: "add_backend", governed: askGovernance, applyG: true},
		{name: "look-alike of a floor of a role nobody holds", server: "customer-vms", tool: "approve_action", ref: true},
		{name: "generic name on a customer server", server: "customer-vms", tool: "create_vm"},
	} {
		got, applies, refused := governed.ServerEntry(tc.server, tc.tool)
		assert.Equal(t, tc.governed, got, "governed: "+tc.name)
		assert.Equal(t, tc.applyG, applies, "governed: "+tc.name)
		assert.Equal(t, tc.ref, refused, "governed: "+tc.name)
		got, applies, refused = trusted.ServerEntry(tc.server, tc.tool)
		assert.Equal(t, tc.trusted, got, "trusted: "+tc.name)
		assert.Equal(t, tc.applyT, applies, "trusted: "+tc.name)
		assert.Equal(t, tc.ref, refused, "trusted: "+tc.name)
	}
}

// Without a readable configuration the governed default applies to muster's
// own tools, and every floor's role is unheld, so floor names are refused.
func TestGovernance_Unreadable(t *testing.T) {
	g := Governance{}
	assert.True(t, g.Governed())
	_, _, refused := g.ServerEntry("anything", "set_agent_approval")
	assert.True(t, refused)
	_, applies, refused := g.ServerEntry("anything", "update_agent")
	assert.False(t, applies)
	assert.False(t, refused)
	got, ok := g.CoreEntry("core_service_stop")
	assert.True(t, ok)
	assert.Equal(t, askGovernance, got)
}

// muster's own tools: floors in both modes; on Governed every core tool but
// muster's reads asks, so every way to pause, resume or remove a connector
// does (M1-MU-S-03), and a core tool added later too.
func TestGovernance_CoreEntry(t *testing.T) {
	governed, trusted := platform(GovernanceGoverned), platform(GovernanceTrusted)
	for _, tool := range CoreFloorTools() {
		for _, g := range []Governance{governed, trusted} {
			got, ok := g.CoreEntry(tool)
			assert.True(t, ok, tool)
			assert.Equal(t, askFloor, got, tool)
		}
	}
	for _, tool := range []string{"core_service_stop", "core_service_start", "core_service_restart", "core_mcpserver_delete", "core_mcpserver_update", "core_something_new"} {
		got, ok := governed.CoreEntry(tool)
		assert.True(t, ok, tool)
		assert.Equal(t, askGovernance, got, tool)
		_, ok = trusted.CoreEntry(tool)
		assert.False(t, ok, "%s runs on Trusted", tool)
	}
	for _, tool := range CoreSafeTools() {
		_, ok := governed.CoreEntry(tool)
		assert.False(t, ok, tool)
	}
	_, ok := governed.CoreEntry("core_config_save")
	assert.False(t, ok, "the built-in ask covers muster's configuration")
}

func TestGovernance_ValidateAndUnmatchedSafe(t *testing.T) {
	require.NoError(t, platform(GovernanceGoverned).Validate())
	require.Error(t, Governance{Servers: []GovernedServer{{Server: "a", Role: GovernanceRoleModelManager, Safe: []string{"add_backend"}}}}.Validate())
	require.Error(t, Governance{Servers: []GovernedServer{{Server: "a", Role: "nope"}}}.Validate())
	assert.Equal(t, []string{"get_agent"}, platform(GovernanceGoverned).UnmatchedSafe("agent-manager", []string{"list_agents", "update_agent"}))
	assert.Nil(t, platform(GovernanceGoverned).UnmatchedSafe("github", []string{"x"}))
}

func TestGovernance_RegisteredCopyIsImmutable(t *testing.T) {
	servers := []GovernedServer{{Server: "agent-manager", Role: GovernanceRoleAgentManager, Safe: []string{"list_agents"}}}
	RegisterGovernance(Governance{Mode: GovernanceTrusted, Readable: true, Servers: servers})
	t.Cleanup(func() { RegisterGovernance(Governance{}) })
	servers[0].Server = "evil"
	servers[0].Safe[0] = "update_agent"
	got := GetGovernance()
	got.Servers[0].Safe[0] = "changed"
	again := GetGovernance()
	assert.Equal(t, "agent-manager", again.Servers[0].Server)
	assert.Equal(t, []string{"list_agents"}, again.Servers[0].Safe)
}

// On Governed a floor's name asks agents on every governed server, whatever
// role it is configured with, so a role override cannot remove a floor; a
// safe list naming any role's floor is refused at load (decision #90).
func TestGovernance_FloorNamesHoldWhateverTheRole(t *testing.T) {
	misrole := Governance{Mode: GovernanceGoverned, Readable: true, Servers: []GovernedServer{
		{Server: "platform-manager", Role: GovernanceRoleAgentManager},
	}}
	for _, tool := range []string{"approve_action", "merge_action", "set_agent_approval"} {
		got, applies, refused := misrole.ServerEntry("platform-manager", tool)
		if !applies || refused || got.Setting != ApprovalSettingAsk || got.Source != ApprovalSourceBuiltin {
			t.Fatalf("%s on a governed server of another role: got %+v applies=%v refused=%v", tool, got, applies, refused)
		}
	}
	trusted := misrole
	trusted.Mode = GovernanceTrusted
	if _, applies, _ := trusted.ServerEntry("platform-manager", "approve_action"); applies {
		t.Fatal("on Trusted the role's own floors decide, as before")
	}

	safe := Governance{Mode: GovernanceGoverned, Readable: true, Servers: []GovernedServer{
		{Server: "agent-manager", Role: GovernanceRoleAgentManager, Safe: []string{"list_agents", "approve_action"}},
	}}
	if err := safe.Validate(); err == nil || !strings.Contains(err.Error(), "approve_action") {
		t.Fatalf("a safe list naming another role's floor must be refused, got %v", err)
	}
}
