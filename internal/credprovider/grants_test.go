package credprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
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
  "grants":[{"entry":"tool:x_github_merge_pull_request","server":"github","uid":"u-github","tools":["merge_pull_request"]}]}}`

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
	assert.Equal(t, []api.AgentApprovalGrant{{Entry: "tool:x_github_merge_pull_request", Server: "github", UID: "u-github", Tools: []string{"merge_pull_request"}}}, b.Approval.Grants)
	assert.Equal(t, "system:serviceaccount:agent-platform:agent-manager", b.Sender)

	other := strings.Replace(settingsBindBody, testActor, otherActor, 1)
	rec := bindBody(t, p, "", other)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, ApprovalSettingsIgnored, rec.Header().Get(ApprovalSettingsHeader), "the sender is told what muster did")
	b, err = store.Get(ctx, otherActor)
	require.NoError(t, err)
	assert.Empty(t, b.Agent, "no sender, no agent")
	assert.Nil(t, b.Approval, "no sender, no settings")
	assert.Equal(t, []string{"server:github", "tool:x_github_merge_pull_request"}, b.Toolset, "the rest is bound")
}

// The person whose Session it is cannot strip the agent's settings by
// binding the actor again without the sender (M2-S3-S-01): 409, the binding
// stays as agent-manager wrote it.
func TestBindWithoutTheSenderCannotReplaceTheSendersBinding(t *testing.T) {
	ctx := context.Background()
	p, store := reviewingProvider(t)
	rec := bindBody(t, p, "agent-manager", settingsBindBody)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, ApprovalSettingsBound, rec.Header().Get(ApprovalSettingsHeader))

	strip := `{"actor":"` + testActor + `","session":"kagent/s1","providers":["github"],"toolset":["preset:full"]}`
	rec = bindBody(t, p, "", strip)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, ApprovalSettingsRefused, rec.Header().Get(ApprovalSettingsHeader))
	b, err := store.Get(ctx, testActor)
	require.NoError(t, err)
	require.NotNil(t, b.Approval, "the settings stay")
	assert.Equal(t, "kagent/alert-review", b.Agent)
	assert.Equal(t, []string{"server:github", "tool:x_github_merge_pull_request"}, b.Toolset)

	rec = bindBody(t, p, "agent-manager", strings.Replace(settingsBindBody, `"default":"Block"`, `"default":"Ask"`, 1))
	require.Equal(t, http.StatusNoContent, rec.Code, "the same sender replaces it")
	rec = bindBody(t, p, "agent-manager", `{"actor":"`+testActor+`","session":"kagent/s1","providers":["github"]}`)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, ApprovalSettingsNone, rec.Header().Get(ApprovalSettingsHeader))
}

// namingReviewer passes every token, naming the sender after it.
type namingReviewer struct{}

func (namingReviewer) Review(_ context.Context, token string) (string, error) {
	return "system:serviceaccount:ns:" + token, nil
}

// Another sender identity, even one allowed to send settings, cannot replace
// a sender's binding.
func TestBindByAnotherSenderIsRefused(t *testing.T) {
	p, _ := newTestProvider(t, fakeGrants{})
	p.senders = namingReviewer{}
	require.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", settingsBindBody).Code)
	assert.Equal(t, http.StatusForbidden, bindBody(t, p, "intruder", settingsBindBody).Code)
	assert.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", settingsBindBody).Code)
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

	keep := `{"agent":"kagent/alert-review","keep":[{"entry":"tool:x_github_merge_pull_request","server":"github","uid":"u-github","tools":["merge_pull_request"]}]}`
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
			require.NoError(t, tc.store.Put(ctx, otherActor, Binding{Subject: "bob", Agent: "kagent/b", ApprovalSender: true}, time.Hour))
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

// A drop needs no person's bearer, only the sender: agent-manager re-sends a
// drop that did not reach muster when it starts (M2-S3-C-04).
func TestDropGrantsWithTheSenderAlone(t *testing.T) {
	p, store := reviewingProvider(t)
	require.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", settingsBindBody).Code)
	body := `{"agent":"kagent/alert-review","keep":[]}`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, GrantsPath, strings.NewReader(body))
	p.HandleDropGrants(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code, "neither a person nor the sender")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, GrantsPath, strings.NewReader(body))
	req.Header.Set(ApprovalSenderHeader, "agent-manager")
	p.HandleDropGrants(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"bindings":1,"dropped":1}`, rec.Body.String())
	b, err := store.Get(context.Background(), testActor)
	require.NoError(t, err)
	assert.Empty(t, b.Approval.Grants)
}

// A drop keeps a mark only when keep names it with the same tools.
func TestDropGrantsComparesTheTools(t *testing.T) {
	p, store := reviewingProvider(t)
	require.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", settingsBindBody).Code)
	keep := `{"agent":"kagent/alert-review","keep":[{"entry":"tool:x_github_merge_pull_request","server":"github","uid":"u-github","tools":["other"]}]}`
	rec := dropGrantsRequest(t, p, "agent-manager", keep)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"bindings":1,"dropped":1}`, rec.Body.String())
	b, err := store.Get(context.Background(), testActor)
	require.NoError(t, err)
	assert.Empty(t, b.Approval.Grants)
}

// The Valkey index lives as long as the agent's bindings: an unread index
// of expired bindings disappears, while a Session that Touch keeps alive
// stays in it past the first TTL (M2-S3-C-03).
func TestValkeyAgentIndexExpiresWithItsBindings(t *testing.T) {
	ctx := context.Background()
	srv := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	store := NewValkeyStore(client, "muster:", nil)
	sender := Binding{Subject: "alice", Agent: "kagent/a", ApprovalSender: true}

	require.NoError(t, store.Put(ctx, testActor, sender, time.Hour))
	require.True(t, srv.Exists(store.agentKey("kagent/a")))
	srv.FastForward(61 * time.Minute)
	assert.False(t, srv.Exists(store.agentKey("kagent/a")), "the index expired with its only binding")

	require.NoError(t, store.Put(ctx, otherActor, Binding{Subject: "bob", Agent: "kagent/a", ApprovalSender: true}, time.Hour))
	for range 3 {
		srv.FastForward(50 * time.Minute)
		require.NoError(t, store.Touch(ctx, otherActor, "kagent/a", time.Hour))
	}
	actors, err := store.ActorsOfAgent(ctx, "kagent/a")
	require.NoError(t, err)
	assert.Equal(t, []string{otherActor}, actors, "a touched Session stays in the index")
}

// session_bound records the agent, its settings and the grant marks a
// Session was bound with (M2-S3-C-13, S-10).
func TestSessionBoundLogsTheAgentsSettings(t *testing.T) {
	var buf bytes.Buffer
	logging.InitForCLI(logging.LevelInfo, &buf)
	t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, io.Discard) })
	p, _ := reviewingProvider(t)
	require.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", settingsBindBody).Code)
	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "session_bound") {
			line = l
		}
	}
	require.NotEmpty(t, line)
	for _, want := range []string{"agent=kagent/alert-review", "approvalEntries=2", "default=Block", "tool:x_github_merge_pull_request@github"} {
		assert.Contains(t, line, want)
	}
}

func unbindWithSender(t *testing.T, p *Provider, sender string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, BindingsPath+"?actor="+testActor, nil)
	if sender != "" {
		req.Header.Set(ApprovalSenderHeader, sender)
	}
	asPerson("alice", p.HandleUnbind).ServeHTTP(rec, req)
	return rec
}

// The person cannot strip the agent's settings by unbinding and binding
// again: a sender-written binding is unbound only by its sender, and after
// it ends (unbound or expired) the actor's seal still refuses a bind without
// the sender (M2-S3-R-01).
func TestUnbindThenRebindCannotStripTheSettings(t *testing.T) {
	ctx := context.Background()
	p, store := newTestProvider(t, fakeGrants{})
	p.senders = namingReviewer{}
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	strip := `{"actor":"` + testActor + `","session":"kagent/s1","providers":["github"],"toolset":["preset:full"]}`

	require.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", settingsBindBody).Code)
	assert.Equal(t, http.StatusConflict, unbindWithSender(t, p, "").Code, "the person alone cannot unbind it")
	assert.Equal(t, http.StatusForbidden, unbindWithSender(t, p, "intruder").Code, "nor another sender")
	b, err := store.Get(ctx, testActor)
	require.NoError(t, err)
	require.NotNil(t, b)

	require.Equal(t, http.StatusNoContent, unbindWithSender(t, p, "agent-manager").Code, "its sender can")
	assert.Equal(t, http.StatusConflict, bindBody(t, p, "", strip).Code, "the seal refuses a bind without the sender")
	assert.Equal(t, http.StatusForbidden, bindBody(t, p, "intruder", settingsBindBody).Code, "and one from another sender")

	require.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", settingsBindBody).Code)
	now = now.Add(p.ttl + time.Minute)
	assert.Equal(t, http.StatusConflict, bindBody(t, p, "", strip).Code, "after the binding expired too")
	now = now.Add(sealFactor * p.ttl)
	assert.Equal(t, http.StatusNoContent, bindBody(t, p, "", strip).Code, "the seal ends after sealFactor TTLs")
}

// Both stores enforce the sender on replace and delete, and keep the seal
// (the Valkey store is production's, M2-S3-R-07).
func TestStoresKeepTheSendersBinding(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storeCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			sent := Binding{Subject: "alice", ApprovalSender: true, Sender: "am", Agent: "kagent/a"}
			require.NoError(t, tc.store.Put(ctx, testActor, sent, time.Hour))
			assert.ErrorIs(t, tc.store.Put(ctx, testActor, Binding{Subject: "alice"}, time.Hour), ErrSenderBound)
			other := sent
			other.Sender = "other"
			assert.ErrorIs(t, tc.store.Put(ctx, testActor, other, time.Hour), ErrSenderBound)
			assert.ErrorIs(t, tc.store.Delete(ctx, testActor, "alice", ""), ErrSenderBound)
			assert.ErrorIs(t, tc.store.Delete(ctx, testActor, "alice", "other"), ErrSenderBound)
			require.NoError(t, tc.store.Delete(ctx, testActor, "alice", "am"))
			assert.ErrorIs(t, tc.store.Put(ctx, testActor, Binding{Subject: "alice"}, time.Hour), ErrSenderBound, "sealed after delete")
			tc.advance(90 * time.Minute)
			assert.ErrorIs(t, tc.store.Put(ctx, testActor, Binding{Subject: "alice"}, time.Hour), ErrSenderBound, "sealed past the binding's TTL")
			tc.advance(time.Hour)
			require.NoError(t, tc.store.Put(ctx, testActor, Binding{Subject: "alice"}, time.Hour), "the seal ends")
		})
	}
}

// An unread agent index is pruned of expired members on the next write
// (M2-S3-R-07): an agent that always has a live Session does not grow its
// index with every Session it ever had.
func TestValkeyAgentIndexPrunesExpiredMembers(t *testing.T) {
	ctx := context.Background()
	srv := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	store := NewValkeyStore(client, "muster:", nil)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Agent: "kagent/a", ApprovalSender: true}, time.Hour))
	now = now.Add(2 * time.Hour)
	require.NoError(t, store.Put(ctx, otherActor, Binding{Subject: "bob", Agent: "kagent/a", ApprovalSender: true}, time.Hour))
	members, err := srv.ZMembers(store.agentKey("kagent/a"))
	require.NoError(t, err)
	assert.Equal(t, []string{otherActor}, members)
}

// The body limits fit the settings: a bind of 40 KiB and a drop of 200 KiB
// are read, larger ones refused (M2-S3-C-10, R-07).
func TestBodyLimits(t *testing.T) {
	p, _ := reviewingProvider(t)
	pad := strings.Repeat("x", 40<<10)
	big := strings.Replace(settingsBindBody, `"session":"kagent/s1"`, `"session":"kagent/`+pad+`"`, 1)
	assert.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", big).Code)
	tooBig := strings.Replace(settingsBindBody, `"session":"kagent/s1"`, `"session":"kagent/`+strings.Repeat("x", 65<<10)+`"`, 1)
	assert.Equal(t, http.StatusBadRequest, bindBody(t, p, "agent-manager", tooBig).Code)

	grant := `{"entry":"tool:x","server":"github","uid":"u","tools":["` + strings.Repeat("t", 780) + `"]}`
	keep := strings.TrimSuffix(strings.Repeat(grant+",", 250), ",")
	rec := dropGrantsRequest(t, p, "agent-manager", `{"agent":"kagent/alert-review","keep":[`+keep+`]}`)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// An unauthenticated drop is refused before its body is read (M2-S3-R-04).
func TestDropGrantsChecksTheSenderBeforeTheBody(t *testing.T) {
	p, _ := newTestProvider(t, fakeGrants{})
	reviews := 0
	p.senders = countingReviewer{n: &reviews}
	body := &readCounter{r: strings.NewReader(`{"agent":"kagent/a","keep":[]}`)}
	rec := httptest.NewRecorder()
	p.HandleDropGrants(rec, httptest.NewRequest(http.MethodPatch, GrantsPath, body))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Zero(t, body.n, "no sender: the body is not read")
	assert.Zero(t, reviews, "and nothing is reviewed")

	body = &readCounter{r: strings.NewReader(`{"agent":"kagent/a","keep":[]}`)}
	req := httptest.NewRequest(http.MethodPatch, GrantsPath, body)
	req.Header.Set(ApprovalSenderHeader, "junk")
	rec = httptest.NewRecorder()
	p.HandleDropGrants(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Zero(t, body.n, "a refused sender: the body is not read")
	assert.Equal(t, 1, reviews)
}

type countingReviewer struct{ n *int }

func (c countingReviewer) Review(context.Context, string) (string, error) {
	*c.n++
	return "", ErrSenderNotAllowed
}

type readCounter struct {
	r io.Reader
	n int
}

func (c *readCounter) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += n
	return n, err
}

// A refused bind says so in X-Muster-Approval-Settings, and a bind of an
// actor whose sender-written binding ended says why it is refused (M2I-7).
func TestRefusedBindsSayWhy(t *testing.T) {
	p, _ := newTestProvider(t, fakeGrants{})
	p.senders = namingReviewer{}
	strip := `{"actor":"` + testActor + `","session":"kagent/s1","providers":["github"]}`
	require.Equal(t, http.StatusNoContent, bindBody(t, p, "agent-manager", settingsBindBody).Code)

	rec := bindBody(t, p, "", strip)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, ApprovalSettingsRefused, rec.Header().Get(ApprovalSettingsHeader))
	assert.NotContains(t, rec.Body.String(), "has ended", "a live binding is not called ended")

	require.Equal(t, http.StatusNoContent, unbindWithSender(t, p, "agent-manager").Code)
	rec = bindBody(t, p, "", strip)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, ApprovalSettingsRefused, rec.Header().Get(ApprovalSettingsHeader))
	assert.Contains(t, rec.Body.String(), "has ended; start a new Session through agent-manager")

	rec = bindBody(t, p, "agent-manager", strings.Replace(settingsBindBody, `"setting":"Ask"`, `"setting":"Deny"`, 1))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, ApprovalSettingsRefused, rec.Header().Get(ApprovalSettingsHeader))
}
