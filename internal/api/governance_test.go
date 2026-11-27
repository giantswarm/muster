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
// ask in both modes.
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
		{name: "floor name on an unnamed server of a held role: asked on governed", server: "other-models", tool: "add_backend", governed: askGovernance, applyG: true},
		{name: "look-alike of a floor of a role nobody holds", server: "other-vms", tool: "mcpserver_create", ref: true},
		{name: "generic name on another server", server: "other-vms", tool: "create_vm"},
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
// does, and a core tool added later too.
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

// On Governed a floor's name, built in or configured, asks agents on every
// governed server whatever role it is configured with, so a role override
// cannot remove a floor; a safe list naming any role's floor is refused at
// load.
func TestGovernance_FloorNamesHoldWhateverTheRole(t *testing.T) {
	floors := MergeRoleFloors(map[string][]string{"exampleManager": {"do_thing"}})
	misrole := Governance{Mode: GovernanceGoverned, Readable: true, RoleFloors: floors, Servers: []GovernedServer{
		{Server: "example", Role: GovernanceRoleSessionManager},
	}}
	for _, tool := range []string{"do_thing", "set_agent_approval"} {
		got, applies, refused := misrole.ServerEntry("example", tool)
		if !applies || refused || got.Setting != ApprovalSettingAsk || got.Source != ApprovalSourceBuiltin {
			t.Fatalf("%s on a governed server of another role: got %+v applies=%v refused=%v", tool, got, applies, refused)
		}
	}
	trusted := misrole
	trusted.Mode = GovernanceTrusted
	for _, tool := range []string{"do_thing", "set_agent_approval"} {
		got, applies, refused := trusted.ServerEntry("example", tool)
		if !applies || refused || got.Setting != ApprovalSettingAsk || got.Source != ApprovalSourceBuiltin {
			t.Fatalf("%s on a governed server of another role on Trusted: floors hold in both modes, got %+v applies=%v refused=%v", tool, got, applies, refused)
		}
	}
	if _, applies, _ := trusted.ServerEntry("example", "list_things"); applies {
		t.Fatal("on Trusted a name that is no floor follows the server's own settings")
	}

	safe := Governance{Mode: GovernanceGoverned, Readable: true, RoleFloors: floors, Servers: []GovernedServer{
		{Server: "agent-manager", Role: GovernanceRoleAgentManager, Safe: []string{"list_agents", "do_thing"}},
	}}
	if err := safe.Validate(); err == nil || !strings.Contains(err.Error(), "do_thing") {
		t.Fatalf("a safe list naming another role's floor must be refused, got %v", err)
	}
}

// Configured floors are a union with the built-in ones: a configured role
// becomes valid, a built-in role keeps its floors whatever the configuration
// says, and a role neither built in nor configured is refused.
func TestGovernance_RoleFloorsAddOnly(t *testing.T) {
	floors := MergeRoleFloors(map[string][]string{
		GovernanceRoleAgentManager: {},
		GovernanceRoleModelManager: {"add_backend", "add_model_source"},
		"exampleManager":           {"do_thing"},
		"emptyRole":                nil,
	})
	assert.Equal(t, builtinRoleFloors[GovernanceRoleAgentManager], floors[GovernanceRoleAgentManager], "an empty list removes nothing")
	assert.Equal(t, []string{"add_backend", "add_model_source"}, floors[GovernanceRoleModelManager])
	assert.Equal(t, []string{"do_thing"}, floors["exampleManager"])
	assert.Equal(t, []string{}, floors["emptyRole"])

	g := Governance{Mode: GovernanceGoverned, Readable: true, RoleFloors: floors, Servers: []GovernedServer{{Server: "example", Role: "exampleManager"}}}
	require.NoError(t, g.Validate())
	got, applies, _ := g.ServerEntry("example", "do_thing")
	assert.True(t, applies)
	assert.Equal(t, askFloor, got)
	got, applies, refused := g.ServerEntry("look-alike", "do_thing")
	assert.True(t, applies, "a configured floor's name on an unnamed server asks on governed once its role is held")
	assert.False(t, refused)
	assert.Equal(t, askGovernance, got)

	unheld := Governance{Mode: GovernanceTrusted, Readable: true, RoleFloors: floors}
	_, _, refused = unheld.ServerEntry("look-alike", "do_thing")
	assert.True(t, refused, "while no server holds the role, its floor names are refused to agents")

	undeclared := Governance{Mode: GovernanceGoverned, Readable: true, Servers: []GovernedServer{{Server: "example", Role: "exampleManager"}}}
	assert.ErrorContains(t, undeclared.Validate(), "unknown role")
	assert.Len(t, GovernanceRoles(), 10, "only the built-in roles")
}

// A name that is a floor of two roles gets one verdict on an unnamed server,
// the strictest, whatever order the roles are read in.
func TestGovernance_FloorOfTwoRolesIsDeterministic(t *testing.T) {
	floors := MergeRoleFloors(map[string][]string{"aManager": {"shared_floor"}, "zManager": {"shared_floor"}})
	for _, tc := range []struct {
		name, mode     string
		holder         string
		applies, refus bool
	}{
		{"governed, one role held: asks", GovernanceGoverned, "zManager", true, false},
		{"governed, the other role held: asks", GovernanceGoverned, "aManager", true, false},
		{"governed, none held: refused", GovernanceGoverned, "", false, true},
		{"trusted, one role unheld: refused", GovernanceTrusted, "zManager", false, true},
	} {
		var servers []GovernedServer
		if tc.holder != "" {
			servers = []GovernedServer{{Server: "holder", Role: tc.holder}}
		}
		g := Governance{Mode: tc.mode, Readable: true, RoleFloors: floors, Servers: servers}
		for range 50 {
			got, applies, refused := g.ServerEntry("look-alike", "shared_floor")
			if applies != tc.applies || refused != tc.refus || (applies && got != askGovernance) {
				t.Fatalf("%s: got %+v applies=%v refused=%v", tc.name, got, applies, refused)
			}
		}
	}
	both := Governance{Mode: GovernanceTrusted, Readable: true, RoleFloors: floors, Servers: []GovernedServer{{Server: "a", Role: "aManager"}, {Server: "z", Role: "zManager"}}}
	if _, applies, refused := both.ServerEntry("look-alike", "shared_floor"); applies || refused {
		t.Fatal("trusted with every role held: the server's own settings decide")
	}
	empty := Governance{Mode: GovernanceGoverned, Readable: true, RoleFloors: map[string][]string{}}
	if got, applies, _ := empty.ServerEntry("x", "set_agent_approval"); applies || got != (ToolApproval{}) {
		t.Fatalf("an empty table keeps the built-in floors: got %+v", got)
	}
	if _, _, refused := empty.ServerEntry("x", "set_agent_approval"); !refused {
		t.Fatal("an empty table keeps the built-in floors (refused while unheld)")
	}
}
