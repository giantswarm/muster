package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// decisionRig is a page rig plus the decision API as a trusted client's
// bearer reaches it (validated by the OAuth server): the person, the bearer's
// OAuth client and whether the credential is an agent's are set per test.
type decisionRig struct {
	*pageRig
	api     *DecisionAPI
	client  string
	agent   string
	now     time.Time
	decided []string
}

func newDecisionRig(t *testing.T) *decisionRig {
	t.Helper()
	rig := &decisionRig{pageRig: newPageRig(t), client: portalClient, now: time.Now()}
	rig.api = &DecisionAPI{
		Clients:         []string{portalClient},
		Installation:    "example",
		Store:           func() api.ApprovalStore { return rig.store },
		ClientOf:        func(context.Context) string { return rig.client },
		AgentCredential: func(context.Context) string { return rig.agent },
		Now:             func() time.Time { return rig.now },
		OnDecided:       func(r *api.ApprovalRequest) { rig.decided = append(rig.decided, r.ID) },
	}
	rig.page.Bearer = http.HandlerFunc(rig.api.ServeView)
	return rig
}

func (rig *decisionRig) serve(t *testing.T, person api.SignInIdentity, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer token")
	req = req.WithContext(api.WithSignInIdentity(req.Context(), person))
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	rig.page.Register(mux)
	mux.HandleFunc("POST "+PathPrefix+"{id}"+DecisionSuffix, rig.api.ServeDecision)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	}
	return rec
}

func (rig *decisionRig) view(t *testing.T, person api.SignInIdentity, id string) (int, RequestView, apiError) {
	t.Helper()
	rec := rig.serve(t, person, http.MethodGet, PathPrefix+id, nil)
	var view RequestView
	var refusal apiError
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	} else {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &refusal))
	}
	return rec.Code, view, refusal
}

func (rig *decisionRig) decide(t *testing.T, person api.SignInIdentity, id, decision, digest string) (int, map[string]string) {
	t.Helper()
	rec := rig.serve(t, person, http.MethodPost, PathPrefix+id+DecisionSuffix, decisionBody{Decision: decision, Digest: digest})
	var answer map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer))
	return rec.Code, answer
}

func TestDecisionAPI_ViewShowsWhatThePageShows(t *testing.T) {
	rig := newDecisionRig(t)
	request := rig.create(t, map[string]any{"pr": "1\u202e2"})
	request.Display.AgentSays = "merge it\u200b now"
	request.Display.Arguments[0].Value = `"1` + "\u202e" + `2"`
	request.Agent = "team-a/reviewer"
	request.RequesterName = "alice@example.com"
	request.Target.Exposed = "x_github_merge_pull_request"
	request.ID = "req-view"
	_, err := rig.store.Create(context.Background(), request)
	require.NoError(t, err)

	status, view, _ := rig.view(t, alice, request.ID)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "Approve this action?", view.Heading)
	assert.Equal(t, api.ApprovalStatusPending, view.Status)
	assert.Equal(t, ViewTool{Title: "Merge pull request", Name: "merge_pull_request", Exposed: "x_github_merge_pull_request", Kind: api.ApprovalTargetTool}, view.Tool)
	assert.Equal(t, &ViewConnector{DisplayName: "github", Server: "github"}, view.Connector)
	assert.True(t, view.ChangesData)
	require.Len(t, view.Arguments, 1)
	assert.Equal(t, `"1⟨U+202E⟩2"`, view.Arguments[0].Value, "bidi controls are marked as on the page")
	assert.Equal(t, "merge it⟨U+200B⟩ now", view.AgentSays)
	assert.Equal(t, &ViewAgent{Namespace: "team-a", Name: "reviewer"}, view.Agent, "no display name bound: the technical name only")
	assert.Equal(t, ViewWhy{Setting: "ask", Source: "connector"}, view.Why)
	assert.Equal(t, ViewPerson{Name: "alice@example.com", Subject: "alice", Issuer: alice.Issuer}, view.ActsFor)
	assert.Equal(t, "example", view.Installation)
	assert.Contains(t, view.CanonicalArguments, "⟨U+202E⟩")
	assert.Equal(t, request.ArgumentsHash, view.ArgumentsHash)
	assert.WithinDuration(t, request.ExpiresAt, view.ExpiresAt, time.Second)
	assert.Equal(t, DisplayDigest(request, "example"), view.Digest)
	assert.Len(t, view.Digest, 64)
}

func TestDecisionAPI_ApproveOnceThenAnswered(t *testing.T) {
	reader := useMetrics(t)
	var logs bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logs)
	rig := newDecisionRig(t)
	request := rig.create(t, map[string]any{"pr": 1})
	_, view, _ := rig.view(t, alice, request.ID)

	status, answer := rig.decide(t, alice, request.ID, "approve", view.Digest)
	require.Equal(t, http.StatusOK, status, answer)
	assert.Equal(t, api.ApprovalStatusApproved, answer["status"])
	assert.Equal(t, []string{request.ID}, rig.decided, "elicitation/complete is sent as for the page")

	stored, err := rig.store.Get(context.Background(), request.ID)
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalStatusApproved, stored.Status)

	status, answer = rig.decide(t, alice, request.ID, "decline", view.Digest)
	assert.Equal(t, http.StatusConflict, status, "decided once")
	assert.Equal(t, apiErrDecided, answer["error"])
	assert.Equal(t, api.ApprovalStatusApproved, answer["status"])
	assert.Len(t, rig.decided, 1)

	assert.EqualValues(t, 1, sum(t, reader, "muster.approval.decisions", map[string]string{"outcome": OutcomeApproved, "path": PathAPI}))
	assert.Contains(t, logs.String(), "approval_decision")
	assert.Contains(t, logs.String(), "path=api")
	assert.Contains(t, logs.String(), "client="+portalClient)
}

func TestDecisionAPI_Decline(t *testing.T) {
	rig := newDecisionRig(t)
	request := rig.create(t, map[string]any{"pr": 1})
	_, view, _ := rig.view(t, alice, request.ID)
	status, answer := rig.decide(t, alice, request.ID, "decline", view.Digest)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, api.ApprovalStatusDeclined, answer["status"])
	stored, err := rig.store.Get(context.Background(), request.ID)
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalStatusDeclined, stored.Status)
}

func TestDecisionAPI_Refusals(t *testing.T) {
	reader := useMetrics(t)
	rig := newDecisionRig(t)
	request := rig.create(t, map[string]any{"pr": 1})
	_, view, _ := rig.view(t, alice, request.ID)

	cases := []struct {
		name    string
		setup   func()
		person  api.SignInIdentity
		id      string
		digest  string
		status  int
		errCode string
	}{
		{name: "actor JWT", setup: func() { rig.agent = "the token is an actor JWT" }, status: http.StatusForbidden, errCode: apiErrActingParty},
		{name: "delegated token", setup: func() { rig.agent = "the token is a delegated token" }, status: http.StatusForbidden, errCode: apiErrActingParty},
		{name: "token naming an acting party", setup: func() { rig.agent = "the token names an acting party (act)" }, status: http.StatusForbidden, errCode: apiErrActingParty},
		{name: "another client (the CLI's)", setup: func() { rig.client = "agent-platform" }, status: http.StatusForbidden, errCode: apiErrClient},
		{name: "a bearer naming no client", setup: func() { rig.client = "" }, status: http.StatusForbidden, errCode: apiErrClient},
		{name: "no trusted client configured", setup: func() { rig.api.Clients = nil }, status: http.StatusForbidden, errCode: apiErrClient},
		{name: "another person", person: bob, status: http.StatusForbidden, errCode: apiErrForeign},
		{name: "no person", person: api.SignInIdentity{}, status: http.StatusForbidden, errCode: apiErrForeign},
		{name: "unknown request", id: "unknown", status: http.StatusNotFound, errCode: apiErrNotFound},
		{name: "changed digest", digest: strings.Repeat("0", 64), status: http.StatusConflict, errCode: apiErrChanged},
		{name: "no digest", digest: "-", status: http.StatusConflict, errCode: apiErrChanged},
		{name: "expired", setup: func() { rig.now = request.ExpiresAt.Add(time.Second) }, status: http.StatusGone, errCode: apiErrExpired},
		{name: "store unavailable", setup: func() { rig.api.Store = func() api.ApprovalStore { return nil } }, status: http.StatusServiceUnavailable, errCode: apiErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig.client, rig.agent, rig.now, rig.api.Clients = portalClient, "", time.Now(), []string{portalClient}
			rig.api.Store = func() api.ApprovalStore { return rig.store }
			if tc.setup != nil {
				tc.setup()
			}
			person := alice
			if tc.name == "another person" || tc.name == "no person" {
				person = tc.person
			}
			id := request.ID
			if tc.id != "" {
				id = tc.id
			}
			digest := view.Digest
			switch tc.digest {
			case "":
			case "-":
				digest = ""
			default:
				digest = tc.digest
			}
			status, answer := rig.decide(t, person, id, "approve", digest)
			assert.Equal(t, tc.status, status, answer)
			assert.Equal(t, tc.errCode, answer["error"])
			assert.NotEmpty(t, answer["message"])
		})
	}
	stored, err := rig.store.Get(context.Background(), request.ID)
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalStatusPending, stored.Status, "no refusal decided anything")
	assert.Empty(t, rig.decided)
	assert.EqualValues(t, 3, sum(t, reader, "muster.approval.refusals", map[string]string{"reason": ReasonActingParty, "path": PathAPI}))
	assert.EqualValues(t, 3, sum(t, reader, "muster.approval.refusals", map[string]string{"reason": ReasonUntrustedClient, "path": PathAPI}))
	assert.EqualValues(t, 2, sum(t, reader, "muster.approval.refusals", map[string]string{"reason": ReasonChanged, "path": PathAPI}))
}

func TestDecisionAPI_ViewRefusals(t *testing.T) {
	rig := newDecisionRig(t)
	request := rig.create(t, map[string]any{"pr": 1})

	status, _, refusal := rig.view(t, bob, request.ID)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, apiErrForeign, refusal.Error)

	rig.agent = "the token is an actor JWT"
	status, _, refusal = rig.view(t, alice, request.ID)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, apiErrActingParty, refusal.Error)
	rig.agent = ""

	rig.client = "agent-platform"
	status, _, refusal = rig.view(t, alice, request.ID)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, apiErrClient, refusal.Error)
}

func TestDecisionAPI_BadBodies(t *testing.T) {
	rig := newDecisionRig(t)
	request := rig.create(t, map[string]any{"pr": 1})
	status, answer := rig.decide(t, alice, request.ID, "maybe", "x")
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, apiErrBadRequest, answer["error"])

	rec := rig.serve(t, alice, http.MethodPost, PathPrefix+request.ID+DecisionSuffix, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDecisionAPI_PageWithoutBearerHandlerIgnoresTheBearer(t *testing.T) {
	rig := newDecisionRig(t)
	request := rig.create(t, map[string]any{"pr": 1})
	rig.page.Bearer = nil
	rec := rig.serve(t, alice, http.MethodGet, PathPrefix+request.ID, nil)
	assert.Equal(t, http.StatusFound, rec.Code, "the page signs the browser in; it never reads the bearer")
}

func TestDisplayDigest_ChangesWithWhatIsShown(t *testing.T) {
	request := newRequest(t, "r", alice, "s1", map[string]any{"pr": 1})
	request.Display = api.ApprovalDisplay{Title: "Merge", Setting: "ask", Source: "connector"}
	base := DisplayDigest(request, "example")
	assert.Equal(t, base, DisplayDigest(request, "example"), "stable")
	request.Status = api.ApprovalStatusApproved
	assert.Equal(t, base, DisplayDigest(request, "example"), "the status is not what was shown")
	assert.NotEqual(t, base, DisplayDigest(request, "other"))
	request.Display.AgentSays = "please"
	assert.NotEqual(t, base, DisplayDigest(request, "example"))
}

func TestDecisionAPI_ViewCarriesTheAgentDisplayName(t *testing.T) {
	rig := newDecisionRig(t)
	request := rig.create(t, map[string]any{"pr": "1"})
	request.Agent = "team-a/reviewer"
	request.AgentDisplayName = "Code\u202e Reviewer"
	request.ID = "req-name"
	_, err := rig.store.Create(context.Background(), request)
	require.NoError(t, err)

	status, view, _ := rig.view(t, alice, request.ID)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, &ViewAgent{Namespace: "team-a", Name: "reviewer", DisplayName: "Code⟨U+202E⟩ Reviewer"}, view.Agent)
}
