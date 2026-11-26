package aggregator

import (
	"context"
	"testing"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"
	"github.com/giantswarm/mcp-oauth/providers/oidc"
	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/v5/internal/api"
)

// The decision API refuses every agent's credential: only a person's own
// bearer decides.
func TestAgentCredential(t *testing.T) {
	a := &AggregatorServer{}
	a.config.CredentialProvider.ActorBearer.Issuer = "https://agent-manager.example.com"
	base := context.Background()
	person := func(ctx context.Context, info *providers.UserInfo) context.Context {
		return oauthhandler.ContextWithUserInfo(api.WithSessionID(ctx, "s-p"), info)
	}
	for name, tc := range map[string]struct {
		ctx   context.Context
		agent bool
	}{
		"person's bearer": {person(base, &providers.UserInfo{ID: "alice", ClientID: "portal", Issuer: "https://dex.example.com"}), false},
		"token naming an acting party": {person(base, &providers.UserInfo{ID: "alice", ClientID: "portal",
			ActorChain: []oidc.ActorClaim{{Subject: "agent-7"}}}), true},
		"actor JWT issuer": {person(base, &providers.UserInfo{ID: "alice", Issuer: "https://agent-manager.example.com"}), true},
		"delegated token":  {api.WithDelegatedCaller(person(base, &providers.UserInfo{ID: "alice", ClientID: "portal"}), api.DelegatedCaller{Subject: "alice"}), true},
		"actor Session":    {oauthhandler.ContextWithUserInfo(api.WithSessionID(base, api.ActorSessionPrefix+"kagent/s1"), &providers.UserInfo{ID: "alice"}), true},
	} {
		t.Run(name, func(t *testing.T) {
			got := a.agentCredential(tc.ctx)
			if tc.agent {
				assert.NotEmpty(t, got)
			} else {
				assert.Empty(t, got)
			}
		})
	}
}
