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
	assert.True(t, governanceFromConfig(nil).Governed(), "no configuration applies the governed entries")
	assert.True(t, governanceFromConfig(&config.GovernanceConfig{Mode: "Trusted"}).Governed(), "an unknown mode is unreadable")

	g := governanceFromConfig(&config.GovernanceConfig{Mode: "trusted", Servers: config.GovernanceServers{AgentManager: []string{"agent-manager"}}})
	assert.False(t, g.Governed())
	assert.Equal(t, []string{"agent-manager"}, g.Servers[api.GovernanceRoleAgentManager])
	assert.True(t, governanceFromConfig(&config.GovernanceConfig{Mode: "governed"}).Governed())
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
