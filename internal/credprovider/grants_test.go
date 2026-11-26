package credprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const (
	otherActor = "spiffe://substrate-actor.local/actor/ns/other-actor"
	thirdActor = "spiffe://substrate-actor.local/actor/ns/third-actor"
)

// settingsBindBody binds testActor with the agent kagent/alert-review, its
// toolset and approval settings with one grant.
const settingsBindBody = `{"actor":"` + testActor + `","session":"kagent/s1","providers":["github"],
 "toolset":["server:github","tool:x_github_merge_pull_request"],
 "agent":"kagent/alert-review",
 "approval":{"entries":[{"entry":"server:github","setting":"Ask"},{"entry":"tool:x_github_merge_pull_request","setting":"Allow"}],
  "default":"Block",
  "grants":[{"entry":"tool:x_github_merge_pull_request","server":"github","uid":"u-github"}]}}`

func bindBody(t *testing.T, p *Provider, sender, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, BindingsPath, strings.NewReader(body))
	if sender != "" {
		req.Header.Set(ApprovalSenderHeader, sender)
	}
	asPerson("alice", p.HandleBind).ServeHTTP(rec, req)
	return rec
}

func reviewingProvider(t *testing.T) (*Provider, *MemoryStore) {
	t.Helper()
	client, _ := fakeCluster(t, nil)
	p, store := newTestProvider(t, fakeGrants{})
	p.senders = KubernetesSenderReviewer{Client: client, Namespace: "muster"}
	return p, store
}

// The agent and its approval settings are taken from an approval sender
// only: without one the rest of the bind is taken without them.
func TestBindTakesAgentSettingsFromTheSenderOnly(t *testing.T) {
	ctx := context.Background()
	p, store := reviewingProvider(t)

	require.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", settingsBindBody).Code)
	b, err := store.Get(ctx, testActor)
	require.NoError(t, err)
	assert.Equal(t, "kagent/alert-review", b.Agent)
	require.NotNil(t, b.Approval)
	assert.Equal(t, "Block", b.Approval.Default)
	assert.Len(t, b.Approval.Entries, 2)
	assert.Equal(t, []api.AgentApprovalGrant{{Entry: "tool:x_github_merge_pull_request", Server: "github", UID: "u-github"}}, b.Approval.Grants)

	require.Equal(t, http.StatusNoContent, bindBody(t, p, "", settingsBindBody).Code)
	b, err = store.Get(ctx, testActor)
	require.NoError(t, err)
	assert.Empty(t, b.Agent, "no sender, no agent")
	assert.Nil(t, b.Approval, "no sender, no settings")
	assert.Equal(t, []string{"server:github", "tool:x_github_merge_pull_request"}, b.Toolset, "the rest is bound")
}

func TestBindRejectsBadAgentSettings(t *testing.T) {
	p, _ := reviewingProvider(t)
	bad := map[string]string{
		"entry outside the toolset": strings.Replace(settingsBindBody, `"toolset":["server:github",`, `"toolset":[`, 1),
		"unknown setting":           strings.Replace(settingsBindBody, `"setting":"Ask"`, `"setting":"Deny"`, 1),
		"grant without an Allow":    strings.Replace(settingsBindBody, `"grants":[{"entry":"tool:x_github_merge_pull_request"`, `"grants":[{"entry":"server:github"`, 1),
		"grant without a server":    strings.Replace(settingsBindBody, `"server":"github"`, `"server":""`, 1),
		"agent not ns/name":         strings.Replace(settingsBindBody, `"agent":"kagent/alert-review"`, `"agent":"alert-review"`, 1),
	}
	for name, body := range bad {
		assert.Equal(t, http.StatusBadRequest, bindBody(t, p, "agent-manager", body).Code, name)
	}
}

func dropGrantsRequest(t *testing.T, p *Provider, sender, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, GrantsPath, strings.NewReader(body))
	if sender != "" {
		req.Header.Set(ApprovalSenderHeader, sender)
	}
	asPerson("bob", p.HandleDropGrants).ServeHTTP(rec, req)
	return rec
}

// Another person's save drops grant marks from the agent's running
// Sessions, whoever bound them, and changes nothing else; it needs the
// approval sender.
func TestDropGrantsRemovesOnlyMarks(t *testing.T) {
	ctx := context.Background()
	p, store := reviewingProvider(t)
	require.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", settingsBindBody).Code)
	other := strings.Replace(settingsBindBody, testActor, otherActor, 1)
	other = strings.Replace(other, "kagent/alert-review", "kagent/release-bot", 1)
	require.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", other).Code)
	before, err := store.Get(ctx, testActor)
	require.NoError(t, err)

	body := `{"agent":"kagent/alert-review","keep":[]}`
	assert.Equal(t, http.StatusForbidden, dropGrantsRequest(t, p, "", body).Code, "the sender is required")
	assert.Equal(t, http.StatusForbidden, dropGrantsRequest(t, p, "other", body).Code, "a sender the review refuses")

	keep := `{"agent":"kagent/alert-review","keep":[{"entry":"tool:x_github_merge_pull_request","server":"github","uid":"u-github"}]}`
	rec := dropGrantsRequest(t, p, "agent-manager", keep)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"bindings":1,"dropped":0}`, rec.Body.String())

	rec = dropGrantsRequest(t, p, "agent-manager", body)
	require.Equal(t, http.StatusOK, rec.Code)
	var out DropGrantsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, DropGrantsResponse{Bindings: 1, Dropped: 1}, out)

	after, err := store.Get(ctx, testActor)
	require.NoError(t, err)
	assert.Empty(t, after.Approval.Grants)
	assert.Equal(t, "alice", after.Subject, "the person stays")
	assert.Equal(t, before.Approval.Entries, after.Approval.Entries, "the settings stay")
	assert.Equal(t, before.Toolset, after.Toolset)
	assert.True(t, after.ApprovalSender)

	untouched, err := store.Get(ctx, otherActor)
	require.NoError(t, err)
	assert.Len(t, untouched.Approval.Grants, 1, "another agent's Session is untouched")

	broken, _ := fakeCluster(t, errors.New("apiserver unavailable"))
	p.senders = KubernetesSenderReviewer{Client: broken, Namespace: "muster"}
	assert.Equal(t, http.StatusServiceUnavailable, dropGrantsRequest(t, p, "agent-manager", body).Code)
}

// Both stores find an agent's bindings and drop marks in place, keeping the
// binding's expiry; a binding of another agent, an expired or a rebound one
// is not touched.
func TestStoreDropsAnAgentsGrants(t *testing.T) {
	ctx := context.Background()
	grant := api.AgentApprovalGrant{Entry: "tool:x", Server: "github", UID: "u"}
	settings := func() *api.AgentApproval {
		return &api.AgentApproval{Entries: []api.AgentApprovalEntry{{Entry: "tool:x", Setting: api.ApprovalAllow}}, Grants: []api.AgentApprovalGrant{grant}}
	}
	for _, tc := range storeCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.store.Put(ctx, testActor, Binding{Subject: "alice", Agent: "kagent/a", Approval: settings(), ApprovalSender: true}, time.Hour))
			require.NoError(t, tc.store.Put(ctx, otherActor, Binding{Subject: "bob", Agent: "kagent/a", Approval: settings(), ApprovalSender: true}, 2*time.Hour))
			require.NoError(t, tc.store.Put(ctx, thirdActor, Binding{Subject: "carol", Agent: "kagent/b", Approval: settings(), ApprovalSender: true}, time.Hour))

			actors, err := tc.store.ActorsOfAgent(ctx, "kagent/a")
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{testActor, otherActor}, actors)

			dropped, err := tc.store.DropGrants(ctx, otherActor, "kagent/a", func(api.AgentApprovalGrant) bool { return false })
			require.NoError(t, err)
			assert.Equal(t, 1, dropped)
			dropped, err = tc.store.DropGrants(ctx, thirdActor, "kagent/a", func(api.AgentApprovalGrant) bool { return false })
			require.NoError(t, err)
			assert.Zero(t, dropped, "a binding of another agent is left alone")

			got, err := tc.store.Get(ctx, otherActor)
			require.NoError(t, err)
			assert.Empty(t, got.Approval.Grants)
			assert.Equal(t, "bob", got.Subject)

			tc.advance(90 * time.Minute)
			got, err = tc.store.Get(ctx, otherActor)
			require.NoError(t, err)
			require.NotNil(t, got, "the drop kept the binding's expiry")
			actors, err = tc.store.ActorsOfAgent(ctx, "kagent/a")
			require.NoError(t, err)
			assert.Equal(t, []string{otherActor}, actors, "an expired binding leaves the index")

			// Rebound to another agent: no longer the first agent's.
			require.NoError(t, tc.store.Put(ctx, otherActor, Binding{Subject: "bob", Agent: "kagent/b"}, time.Hour))
			actors, err = tc.store.ActorsOfAgent(ctx, "kagent/a")
			require.NoError(t, err)
			assert.Empty(t, actors)
		})
	}
}

// An actor's requests carry the agent and its settings only from a binding
// an approval sender wrote.
func TestBindingApprovalNeedsTheSender(t *testing.T) {
	settings := &api.AgentApproval{Default: api.ApprovalAsk}
	assert.Nil(t, bindingApproval(&Binding{Agent: "kagent/a", Approval: settings}))
	assert.Nil(t, bindingApproval(&Binding{ApprovalSender: true}))
	got := bindingApproval(&Binding{ApprovalSender: true, Agent: "kagent/a", Approval: settings})
	require.NotNil(t, got)
	assert.Equal(t, "kagent/a", got.Agent)
	assert.Equal(t, api.ApprovalAsk, got.Default)
	assert.False(t, got.Reporting)
}
