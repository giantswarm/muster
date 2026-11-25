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

	// The earlier shape still works: its servers are governed, safe lists
	// empty.
	g, err = governanceFromConfig(&config.GovernanceConfig{Mode: "trusted", Servers: config.GovernanceServers{AgentManager: []string{"agent-manager"}}})
	require.NoError(t, err)
	assert.False(t, g.Governed())
	assert.Equal(t, []api.GovernedServer{{Server: "agent-manager", Role: "agentManager"}}, g.Servers)

	for name, bad := range map[string]*config.GovernanceConfig{
		"both shapes":         {Mode: "governed", Servers: config.GovernanceServers{AgentManager: []string{"a"}}, GovernedServers: []config.GovernedServerConfig{{Server: "b"}}},
		"unknown role":        {Mode: "governed", GovernedServers: []config.GovernedServerConfig{{Server: "a", Role: "agentmanager"}}},
		"server twice":        {Mode: "governed", GovernedServers: []config.GovernedServerConfig{{Server: "a"}, {Server: "a"}}},
		"no server key":       {Mode: "governed", GovernedServers: []config.GovernedServerConfig{{Role: "agentManager"}}},
		"a floor marked safe": {Mode: "governed", GovernedServers: []config.GovernedServerConfig{{Server: "a", Role: "agentManager", Safe: []string{"set_agent_approval"}}}},
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
