package aggregator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/v5/internal/api"
)

func TestCallerKindHeader(t *testing.T) {
	api.RegisterGovernance(api.Governance{Mode: api.GovernanceTrusted, Readable: true,
		Servers: map[string][]string{api.GovernanceRoleAgentManager: {"agent-manager"}}})
	t.Cleanup(func() { api.RegisterGovernance(api.Governance{}) })

	assert.Nil(t, callerKindHeaderFunc("actor-1", &ServerInfo{Name: "github"}), "only servers the configuration names")

	fn := callerKindHeaderFunc("actor-1", &ServerInfo{Name: "agent-manager"})
	assert.Equal(t, map[string]string{HeaderCallerKind: "agent"}, fn(context.Background()), "an actor JWT caller is an agent")

	delegated := api.WithDelegatedCaller(context.Background(), api.DelegatedCaller{Subject: "alice"})
	assert.Equal(t, map[string]string{HeaderCallerKind: "agent"}, callerKindHeaderFunc("session-1", &ServerInfo{Name: "agent-manager"})(delegated))

	assert.Nil(t, callerKindHeaderFunc("session-1", &ServerInfo{Name: "agent-manager"})(context.Background()), "a person sends none")
}
