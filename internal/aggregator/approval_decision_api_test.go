package aggregator

import (
	"context"
	"errors"
	"testing"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"
	"github.com/giantswarm/mcp-oauth/providers/oidc"
	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/approval"
	"github.com/giantswarm/muster/v5/internal/credprovider"
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

type stubSenderReviewer struct {
	identity string
	err      error
}

func (s stubSenderReviewer) Review(context.Context, string) (string, error) { return s.identity, s.err }

// The decider review maps a token that does not pass onto the approval
// package's refusal, and passes a review error through as one.
func TestDeciderReview(t *testing.T) {
	ctx := context.Background()
	identity, err := deciderReview(stubSenderReviewer{identity: "system:serviceaccount:agent-platform:backstage"})(ctx, "t")
	assert.NoError(t, err)
	assert.Equal(t, "system:serviceaccount:agent-platform:backstage", identity)

	_, err = deciderReview(stubSenderReviewer{err: credprovider.ErrSenderNotAllowed})(ctx, "t")
	assert.ErrorIs(t, err, approval.ErrWorkloadNotAllowed)

	_, err = deciderReview(stubSenderReviewer{err: errors.New("apiserver unavailable")})(ctx, "t")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, approval.ErrWorkloadNotAllowed)
}

// The sign-in confirmation's person is named by an email only when the
// issuer verified it.
func TestRequesterVerifiedEmail(t *testing.T) {
	with := func(info *providers.UserInfo) context.Context {
		return oauthhandler.ContextWithUserInfo(context.Background(), info)
	}
	assert.Equal(t, "alice@example.com", requesterVerifiedEmail(with(&providers.UserInfo{ID: "a", Email: "alice@example.com", EmailVerified: true, Name: "Alice"})))
	assert.Empty(t, requesterVerifiedEmail(with(&providers.UserInfo{ID: "a", Email: "alice@example.com", Name: "Alice"})), "an unverified email")
	assert.Empty(t, requesterVerifiedEmail(with(&providers.UserInfo{ID: "a", Name: "Alice Example"})), "a self-chosen name")
	assert.Empty(t, requesterVerifiedEmail(context.Background()))
}
