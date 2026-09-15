package testing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/testing/fixtures/scale"
)

// A scenario that names the scale fixture boots the fixture's servers,
// workflows and authorization servers plus its own; a scenario server with a
// fixture server's name overlays that server's config.
func TestApplyFixture_MergesTheScaleFixtureUnderTheScenario(t *testing.T) {
	pre := &MusterPreConfiguration{
		Fixture: scale.Name,
		MCPServers: []MCPServerConfig{
			{Name: "zircon-clusters", Config: map[string]interface{}{"connect_delay": "4s"}},
			{Name: "extra", Config: map[string]interface{}{"type": "streamable-http", "tools": []interface{}{}}},
		},
		Workflows: []WorkflowConfig{{Name: "scenario-only", Config: map[string]interface{}{"steps": []interface{}{}}}},
		Storage:   &StorageConfig{Type: StorageValkey},
	}
	require.NoError(t, applyFixture(pre))

	shape := scale.Get().Shape()
	assert.Len(t, pre.MCPServers, shape.Servers+1, "the fixture's servers plus the scenario's new one")
	assert.Len(t, pre.Workflows, shape.Workflows+1)
	assert.Len(t, pre.MockOAuthServers, 2)
	require.NotNil(t, pre.MusterBroker)
	assert.Equal(t, StorageValkey, pre.Storage.Type, "the scenario's own settings stay")

	var overlaid *MCPServerConfig
	for i := range pre.MCPServers {
		if pre.MCPServers[i].Name == "zircon-clusters" {
			overlaid = &pre.MCPServers[i]
		}
	}
	require.NotNil(t, overlaid)
	assert.Equal(t, "4s", overlaid.Config["connect_delay"], "the scenario's key is added")
	assert.Equal(t, "streamable-http", overlaid.Config["type"], "the fixture's keys stay")
	tools, _ := overlaid.Config["tools"].([]interface{})
	assert.NotEmpty(t, tools, "the fixture server's tools stay")
	assert.Equal(t, "extra", pre.MCPServers[len(pre.MCPServers)-1].Name, "scenario servers come after the fixture's")
}

func TestApplyFixture_UnknownNameIsRefused(t *testing.T) {
	err := applyFixture(&MusterPreConfiguration{Fixture: "gazelle"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"gazelle" is unknown`)
	assert.Contains(t, err.Error(), "scale")
}

func TestApplyFixture_NoFixtureIsANoop(t *testing.T) {
	pre := &MusterPreConfiguration{MCPServers: []MCPServerConfig{{Name: "only"}}}
	require.NoError(t, applyFixture(pre))
	assert.Len(t, pre.MCPServers, 1)
	require.NoError(t, applyFixture(nil))
}

// The scenario's own mock OAuth server with a fixture server's name replaces
// it; the broker is the scenario's when declared.
func TestApplyFixture_ScenarioAuthorizationServersWin(t *testing.T) {
	broker := &MusterBrokerConfig{TrustedIssuers: []BrokerTrustedIssuerConfig{{OAuthServerRef: "mine"}}}
	pre := &MusterPreConfiguration{
		Fixture:          scale.Name,
		MockOAuthServers: []MockOAuthServerConfig{{Name: scale.Get().FleetIssuer, Profile: "dex"}},
		MusterBroker:     broker,
	}
	require.NoError(t, applyFixture(pre))
	assert.Len(t, pre.MockOAuthServers, 2)
	var fleet MockOAuthServerConfig
	for _, s := range pre.MockOAuthServers {
		if s.Name == scale.Get().FleetIssuer {
			fleet = s
		}
	}
	assert.Equal(t, "dex", fleet.Profile)
	assert.Same(t, broker, pre.MusterBroker)
}
