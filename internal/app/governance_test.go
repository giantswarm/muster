package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
)

func TestGovernanceFromConfig(t *testing.T) {
	g, err := governanceFromConfig(nil)
	require.NoError(t, err)
	assert.True(t, g.Governed(), "no configuration applies the governed default")
	g, err = governanceFromConfig(&config.GovernanceConfig{Mode: "Trusted"})
	require.NoError(t, err)
	assert.True(t, g.Governed(), "an unknown mode is unreadable")

	g, err = governanceFromConfig(&config.GovernanceConfig{Mode: "governed", GovernedServers: []config.GovernedServerConfig{
		{Server: "agent-manager", Role: "agentManager", Safe: []string{"list_agents"}},
		{Server: "kagent", Role: "sessionManager"},
	}})
	require.NoError(t, err)
	assert.Equal(t, []api.GovernedServer{
		{Server: "agent-manager", Role: "agentManager", Safe: []string{"list_agents"}},
		{Server: "kagent", Role: "sessionManager"},
	}, g.Servers)

	// Configured floors are added to the built-in ones and declare new
	// roles; a removed servers shape stops the start.
	g, err = governanceFromConfig(&config.GovernanceConfig{Mode: "governed",
		RoleFloors:      map[string][]string{"exampleManager": {"do_thing"}, "agentManager": {"extra_grant"}},
		GovernedServers: []config.GovernedServerConfig{{Server: "example", Role: "exampleManager", Safe: []string{"get_thing"}}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"do_thing"}, g.RoleFloorsOf("exampleManager"))
	assert.Contains(t, g.RoleFloorsOf("agentManager"), "set_agent_approval", "a configured role never loses a built-in floor")
	assert.Contains(t, g.RoleFloorsOf("agentManager"), "extra_grant")

	for name, bad := range map[string]*config.GovernanceConfig{
		"removed servers shape":  {Mode: "governed", Servers: map[string]any{"agentManager": []any{"a"}}},
		"undeclared role":        {Mode: "governed", GovernedServers: []config.GovernedServerConfig{{Server: "a", Role: "exampleManager"}}},
		"configured floor safe":  {Mode: "governed", RoleFloors: map[string][]string{"exampleManager": {"do_thing"}}, GovernedServers: []config.GovernedServerConfig{{Server: "a", Safe: []string{"do_thing"}}}},
		"floor without a name":   {Mode: "governed", RoleFloors: map[string][]string{"exampleManager": {""}}},
		"declared without tools": {Mode: "governed", RoleFloors: map[string][]string{"exampleManager": {}}, GovernedServers: []config.GovernedServerConfig{{Server: "a", Role: "exampleManager"}}},
		"built-in role emptied":  {Mode: "governed", RoleFloors: map[string][]string{"agentManager": nil}},
		"unknown role":           {Mode: "governed", GovernedServers: []config.GovernedServerConfig{{Server: "a", Role: "agentmanager"}}},
		"server twice":           {Mode: "governed", GovernedServers: []config.GovernedServerConfig{{Server: "a"}, {Server: "a"}}},
		"no server key":          {Mode: "governed", GovernedServers: []config.GovernedServerConfig{{Role: "agentManager"}}},
		"a floor marked safe":    {Mode: "governed", GovernedServers: []config.GovernedServerConfig{{Server: "a", Role: "agentManager", Safe: []string{"set_agent_approval"}}}},
	} {
		_, err := governanceFromConfig(bad)
		require.Error(t, err, name)
	}
}

func TestApprovalTTL(t *testing.T) {
	ttl, err := approvalTTL(config.ApprovalConfig{})
	require.NoError(t, err)
	assert.Equal(t, 5*time.Minute, ttl)
	ttl, err = approvalTTL(config.ApprovalConfig{TTL: "2m"})
	require.NoError(t, err)
	assert.Equal(t, 2*time.Minute, ttl)
	for _, bad := range []string{"soon", "0s", "-1m", "2h"} {
		_, err := approvalTTL(config.ApprovalConfig{TTL: bad})
		require.Error(t, err, bad)
	}
}
