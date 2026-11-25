package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGovernance_CoreFloor(t *testing.T) {
	for _, mode := range []Governance{
		{Mode: GovernanceTrusted, Readable: true},
		{Mode: GovernanceGoverned, Readable: true},
		{},
	} {
		for _, tool := range []string{"core_mcpserver_create", "core_mcpserver_connect", "core_mcpserver_set_approval",
			"core_workflow_create", "core_workflow_update", "core_workflow_delete"} {
			got, ok := mode.CoreFloor(tool, nil)
			assert.True(t, ok, "%s asks in mode %q", tool, mode.Mode)
			assert.Equal(t, ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceBuiltin}, got)
		}
		_, ok := mode.CoreFloor("core_mcpserver_list", nil)
		assert.False(t, ok)
	}

	trusted := Governance{Mode: GovernanceTrusted, Readable: true}
	_, ok := trusted.CoreFloor("core_mcpserver_update", map[string]any{"suspended": true})
	assert.False(t, ok, "pausing a connector runs on Trusted")
	governed := Governance{Mode: GovernanceGoverned, Readable: true}
	got, ok := governed.CoreFloor("core_mcpserver_update", map[string]any{"suspended": true})
	assert.True(t, ok)
	assert.Equal(t, ApprovalSourceGovernance, got.Source)
	_, ok = governed.CoreFloor("core_mcpserver_update", map[string]any{"suspended": false})
	assert.False(t, ok, "resuming runs")
	_, ok = Governance{}.CoreFloor("core_mcpserver_update", map[string]any{"suspended": true})
	assert.True(t, ok, "without a readable configuration the governed entries apply")
}

// Every way to pause or remove a connector asks an agent on Governed, not
// only core_mcpserver_update {suspended: true} (M1-MU-S-03, M1-MU-C-15).
func TestGovernance_EveryPauseAsksOnGoverned(t *testing.T) {
	governed := Governance{Mode: GovernanceGoverned, Readable: true}
	trusted := Governance{Mode: GovernanceTrusted, Readable: true}
	for name, call := range map[string]struct {
		tool string
		args map[string]any
	}{
		"service stop":        {"core_service_stop", map[string]any{"name": "github"}},
		"delete":              {"core_mcpserver_delete", map[string]any{"name": "github"}},
		"autoStart false":     {"core_mcpserver_update", map[string]any{"name": "github", "autoStart": false}},
		"templated suspended": {"core_mcpserver_update", map[string]any{"name": "github", "suspended": "{{ .input.pause }}"}},
		"templated autoStart": {"core_mcpserver_update", map[string]any{"name": "github", "autoStart": "{{ .input.x }}"}},
		"suspended true":      {"core_mcpserver_update", map[string]any{"name": "github", "suspended": true}},
	} {
		got, ok := governed.CoreFloor(call.tool, call.args)
		assert.True(t, ok, name)
		assert.Equal(t, ApprovalSourceGovernance, got.Source, name)
		_, ok = trusted.CoreFloor(call.tool, call.args)
		assert.False(t, ok, "%s runs on Trusted", name)
	}
	_, ok := governed.CoreFloor("core_mcpserver_update", map[string]any{"name": "github", "autoStart": true, "suspended": false})
	assert.False(t, ok, "resuming runs")
	_, ok = governed.CoreFloor("core_service_start", map[string]any{"name": "github"})
	assert.False(t, ok)
}

func TestGovernance_ServerFloor(t *testing.T) {
	servers := map[string][]string{
		GovernanceRoleAgentManager:    {"agent-manager"},
		GovernanceRolePlatformManager: {"platform-manager"},
	}
	trusted := Governance{Mode: GovernanceTrusted, Readable: true, Servers: servers}
	governed := Governance{Mode: GovernanceGoverned, Readable: true, Servers: servers}

	for _, g := range []Governance{trusted, governed} {
		for server, tools := range map[string][]string{
			"agent-manager":    {"set_agent_approval", "set_organization_connector"},
			"platform-manager": {"approve_action", "merge_action"},
		} {
			for _, tool := range tools {
				got, applies, refused := g.ServerFloor(server, tool, nil)
				assert.True(t, applies, "%s/%s asks in both modes", server, tool)
				assert.False(t, refused)
				assert.Equal(t, ApprovalSourceBuiltin, got.Source)
			}
		}
		_, _, refused := g.ServerFloor("look-alike", "set_agent_approval", nil)
		assert.True(t, refused, "a floor's name on a server the configuration does not name is refused")
		_, applies, refused := g.ServerFloor("agent-manager", "get_agent", nil)
		assert.False(t, applies)
		assert.False(t, refused)
	}

	for _, tool := range []string{"create_agent", "update_agent_access", "start_session"} {
		_, applies, _ := trusted.ServerFloor("agent-manager", tool, nil)
		assert.False(t, applies, "%s runs on Trusted", tool)
		got, applies, _ := governed.ServerFloor("agent-manager", tool, nil)
		assert.True(t, applies, "%s asks on Governed", tool)
		assert.Equal(t, ApprovalSourceGovernance, got.Source)
		_, _, refused := trusted.ServerFloor("other", tool, nil)
		assert.True(t, refused)
	}

	// Without a configuration no server is named: every such name is refused
	// to agents.
	_, _, refused := Governance{}.ServerFloor("agent-manager", "create_agent", nil)
	assert.True(t, refused)
}

func TestGovernance_RegisteredCopyIsImmutable(t *testing.T) {
	servers := map[string][]string{GovernanceRoleAgentManager: {"agent-manager"}}
	RegisterGovernance(Governance{Mode: GovernanceTrusted, Readable: true, Servers: servers})
	t.Cleanup(func() { RegisterGovernance(Governance{}) })
	servers[GovernanceRoleAgentManager][0] = "changed"
	got := GetGovernance()
	got.Servers[GovernanceRoleAgentManager][0] = "changed too"
	assert.Equal(t, []string{"agent-manager"}, GetGovernance().Servers[GovernanceRoleAgentManager])
}
