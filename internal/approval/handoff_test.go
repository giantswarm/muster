package approval

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const portalClient = "portal"

// The workload tokens the fake review knows: the portal backend's
// ServiceAccount (allowed to decide), another ServiceAccount (not allowed),
// and one whose review cannot be made.
const (
	portalWorkload   = "portal-sa-token"
	otherWorkload    = "other-sa-token"
	brokenWorkload   = "broken-review"
	portalWorkloadSA = "system:serviceaccount:agent-platform:backstage"
)

// fakeWorkloadReview reviews the tokens above.
func fakeWorkloadReview(_ context.Context, token string) (string, error) {
	switch token {
	case portalWorkload:
		return portalWorkloadSA, nil
	case brokenWorkload:
		return "", errors.New("apiserver unavailable")
	}
	return "", ErrWorkloadNotAllowed
}

// handoffRig is a page rig whose page redeems handoffs, plus the issuing
// endpoint as the portal's bearer reaches it (identity and client already
// validated by the OAuth server).
type handoffRig struct {
	*pageRig
	handoffs *Handoffs
	client   string
	workload string
	agent    string
	email    string
}

func newHandoffRig(t *testing.T) *handoffRig {
	t.Helper()
	rig := &handoffRig{pageRig: newPageRig(t), client: portalClient, workload: portalWorkload, email: "alice@example.com"}
	rig.handoffs = &Handoffs{
		PublicURL:       rig.server.URL,
		StartPath:       "/oauth/proxy/start",
		Clients:         []string{portalClient},
		Store:           func() api.ApprovalStore { return rig.store },
		ClientOf:        func(context.Context) string { return rig.client },
		AgentCredential: func(context.Context) string { return rig.agent },
		Workload:        fakeWorkloadReview,
		VerifiedEmailOf: func(context.Context) string { return rig.email },
	}
	rig.page.Handoffs = rig.handoffs
	return rig
}

// issue asks for a ticket for link as person, returning the status and the
// ticket (empty unless issued).
func (rig *handoffRig) issue(t *testing.T, person api.SignInIdentity, link string) (int, string) {
	t.Helper()
	body, err := json.Marshal(handoffRequest{URL: link})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, HandoffPath, strings.NewReader(string(body)))
	if rig.workload != "" {
		req.Header.Set(ClientWorkloadHeader, rig.workload)
	}
	req = req.WithContext(api.WithSignInIdentity(req.Context(), person))
	rec := httptest.NewRecorder()
	rig.handoffs.ServeHTTP(rec, req)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	if rec.Code != http.StatusCreated {
		assert.NotContains(t, rec.Body.String(), "ticket")
		return rec.Code, ""
	}
	var answer handoffAnswer
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer))
	assert.Equal(t, 60, answer.ExpiresIn)
	require.NotEmpty(t, answer.Ticket)
	return rec.Code, answer.Ticket
}

func TestHandoff_IssuedOnlyToTheRequestersOwnAdmittedClient(t *testing.T) {
	reader := useMetrics(t)
	rig := newHandoffRig(t)
	request := rig.create(t, map[string]any{"pr": 1})
	link := rig.page.URL(request.ID)

	status, ticket := rig.issue(t, alice, link)
	assert.Equal(t, http.StatusCreated, status)
	_, err := rig.store.TakeHandoff(context.Background(), ticket)
	assert.ErrorIs(t, err, api.ErrApprovalNotFound, "the ticket itself is never a store key, only its digest")

	status, _ = rig.issue(t, bob, link)
	assert.Equal(t, http.StatusForbidden, status, "another person's approval")
	status, _ = rig.issue(t, alice, rig.page.URL("unknown"))
	assert.Equal(t, http.StatusForbidden, status, "an unknown approval answers like another person's")

	rig.client = "agent-platform"
	status, _ = rig.issue(t, alice, link)
	assert.Equal(t, http.StatusForbidden, status, "a client not on approval.handoffClients")
	rig.client = ""
	status, _ = rig.issue(t, alice, link)
	assert.Equal(t, http.StatusForbidden, status, "a bearer naming no client")
	rig.client = portalClient
	rig.handoffs.Clients = nil
	status, _ = rig.issue(t, alice, link)
	assert.Equal(t, http.StatusForbidden, status, "no client admitted: muster issues none")
	rig.handoffs.Clients = []string{portalClient}

	status, _ = rig.issue(t, api.SignInIdentity{}, link)
	assert.Equal(t, http.StatusForbidden, status, "no person")

	// IA-01: the person's bearer alone (no workload token of the portal
	// backend), another workload, an agent's credential: no ticket.
	rig.workload = ""
	status, _ = rig.issue(t, alice, link)
	assert.Equal(t, http.StatusForbidden, status, "the person's token alone")
	rig.workload = otherWorkload
	status, _ = rig.issue(t, alice, link)
	assert.Equal(t, http.StatusForbidden, status, "another workload")
	rig.workload = brokenWorkload
	status, _ = rig.issue(t, alice, link)
	assert.Equal(t, http.StatusServiceUnavailable, status, "the review cannot be made")
	rig.workload = portalWorkload
	status, _ = rig.issue(t, api.SignInIdentity{}, link)
	assert.Equal(t, http.StatusForbidden, status, "the workload alone, no person")
	rig.agent = "the token is an actor JWT"
	status, _ = rig.issue(t, alice, link)
	assert.Equal(t, http.StatusForbidden, status, "an agent's credential")
	rig.agent = ""
	rig.handoffs.Workload = nil
	status, _ = rig.issue(t, alice, link)
	assert.Equal(t, http.StatusForbidden, status, "muster cannot review workloads")
	rig.handoffs.Workload = fakeWorkloadReview

	for _, other := range []string{
		"https://evil.example.com/approvals/" + request.ID,
		rig.server.URL + "/approvals/",
		rig.server.URL + "/approvals/a/b",
		rig.server.URL + "/mcp",
		rig.server.URL + "/oauth/proxy/start",
		"not a url",
	} {
		status, _ = rig.issue(t, alice, other)
		assert.Equal(t, http.StatusBadRequest, status, other)
	}

	assert.EqualValues(t, 1, sum(t, reader, "muster.approval.handoffs", map[string]string{"outcome": HandoffIssued, "page": api.HandoffKindApproval}))
	assert.EqualValues(t, 2, sum(t, reader, "muster.approval.handoffs", map[string]string{"outcome": HandoffRefused, "reason": HandoffReasonForeign}))
	assert.EqualValues(t, 5, sum(t, reader, "muster.approval.handoffs", map[string]string{"outcome": HandoffRefused, "reason": HandoffReasonClient}))
	assert.EqualValues(t, 3, sum(t, reader, "muster.approval.handoffs", map[string]string{"outcome": HandoffRefused, "reason": HandoffReasonWorkload}))
	assert.EqualValues(t, 1, sum(t, reader, "muster.approval.handoffs", map[string]string{"outcome": HandoffRefused, "reason": HandoffReasonActingParty}))
	assert.EqualValues(t, 6, sum(t, reader, "muster.approval.handoffs", map[string]string{"outcome": HandoffRefused, "reason": HandoffReasonTarget}))
}

func TestHandoff_IssuedForAConnectorSignInStart(t *testing.T) {
	rig := newHandoffRig(t)
	status, ticket := rig.issue(t, alice, rig.server.URL+"/oauth/proxy/start?state=abc&redirect=x")
	require.Equal(t, http.StatusCreated, status)
	assert.False(t, rig.handoffs.Redeem(context.Background(), ticket, api.HandoffKindSignIn, "abc", bob), "the start's person is checked on redemption")

	_, ticket = rig.issue(t, alice, rig.server.URL+"/oauth/proxy/start?state=abc")
	handoff, ok := rig.handoffs.RedeemHandoff(context.Background(), ticket, api.HandoffKindSignIn, "abc", alice)
	assert.True(t, ok)
	assert.Equal(t, "alice@example.com", handoff.Name, "the start's confirmation names the person the ticket was issued to")
}

// The sign-in confirmation names the ticket's person by a verified
// identifier only: an unverified email or a self-chosen name names nobody,
// and what the identity provider sent is marked like any untrusted text.
func TestHandoff_NamesThePersonByAVerifiedIdentifier(t *testing.T) {
	label := func(email string) string {
		rig := newHandoffRig(t)
		rig.email = email
		_, ticket := rig.issue(t, alice, rig.server.URL+"/oauth/proxy/start?state=abc")
		handoff, ok := rig.handoffs.RedeemHandoff(context.Background(), ticket, api.HandoffKindSignIn, "abc", alice)
		require.True(t, ok)
		return handoff.Name
	}
	assert.Equal(t, "alice@example.com", label("alice@example.com"))
	assert.Equal(t, "alice at https://dex.example.com", label(""), "no verified email: the subject at its issuer")
	assert.Equal(t, "alice⟨U+202E⟩moc.elpmaxe@x", label("alice\u202emoc.elpmaxe@x"), "bidi characters are marked")
	assert.Equal(t, "a⟨U+200B⟩b at iss", PersonLabel("", api.SignInIdentity{Subject: "a\u200bb", Issuer: "iss"}))
}

func TestHandoff_RedeemedOnceForItsPageAndPersonWithinItsTTL(t *testing.T) {
	reader := useMetrics(t)
	rig := newHandoffRig(t)
	request := rig.create(t, map[string]any{"pr": 1})
	ctx := context.Background()
	issue := func() string {
		_, ticket := rig.issue(t, alice, rig.page.URL(request.ID))
		return ticket
	}

	ticket := issue()
	assert.True(t, rig.handoffs.Redeem(ctx, ticket, api.HandoffKindApproval, request.ID, alice))
	assert.False(t, rig.handoffs.Redeem(ctx, ticket, api.HandoffKindApproval, request.ID, alice), "single use")

	ticket = issue()
	assert.False(t, rig.handoffs.Redeem(ctx, ticket, api.HandoffKindApproval, "another", alice), "another page")
	assert.False(t, rig.handoffs.Redeem(ctx, ticket, api.HandoffKindApproval, request.ID, alice), "spent by the wrong page")

	ticket = issue()
	assert.False(t, rig.handoffs.Redeem(ctx, ticket, api.HandoffKindSignIn, request.ID, alice), "another kind of page")

	ticket = issue()
	assert.False(t, rig.handoffs.Redeem(ctx, ticket, api.HandoffKindApproval, request.ID, bob), "another person")
	ticket = issue()
	assert.False(t, rig.handoffs.Redeem(ctx, ticket, api.HandoffKindApproval, request.ID, api.SignInIdentity{}), "nobody")

	ticket = issue()
	now := time.Now()
	rig.store.SetClock(func() time.Time { return now.Add(HandoffTTL) })
	assert.False(t, rig.handoffs.Redeem(ctx, ticket, api.HandoffKindApproval, request.ID, alice), "expired")
	rig.store.SetClock(time.Now)

	assert.False(t, rig.handoffs.Redeem(ctx, "", api.HandoffKindApproval, request.ID, alice))
	assert.False(t, rig.handoffs.Redeem(ctx, strings.Repeat("A", 65), api.HandoffKindApproval, request.ID, alice))
	assert.False(t, rig.handoffs.Redeem(ctx, "guessed", api.HandoffKindApproval, request.ID, alice))

	assert.EqualValues(t, 1, sum(t, reader, "muster.approval.handoffs", map[string]string{"outcome": HandoffRedeemed, "page": api.HandoffKindApproval}))
	assert.EqualValues(t, 6, sum(t, reader, "muster.approval.handoffs", map[string]string{"outcome": HandoffRefused, "reason": HandoffReasonInvalid}))
	assert.EqualValues(t, 2, sum(t, reader, "muster.approval.handoffs", map[string]string{"outcome": HandoffRefused, "reason": HandoffReasonTarget}))
	assert.EqualValues(t, 2, sum(t, reader, "muster.approval.handoffs", map[string]string{"outcome": HandoffRefused, "reason": HandoffReasonForeign}))
}

func TestHandoff_ValkeyStoreTakesATicketOnce(t *testing.T) {
	store, srv, _ := newValkeyStoreForTest(t)
	ctx := context.Background()
	handoff := api.SignInHandoff{Kind: api.HandoffKindApproval, Target: "req", Identity: alice}
	require.NoError(t, store.PutHandoff(ctx, "k", handoff, HandoffTTL))
	got, err := store.TakeHandoff(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, handoff, *got)
	_, err = store.TakeHandoff(ctx, "k")
	assert.ErrorIs(t, err, api.ErrApprovalNotFound)

	require.NoError(t, store.PutHandoff(ctx, "k", handoff, HandoffTTL))
	srv.FastForward(HandoffTTL)
	_, err = store.TakeHandoff(ctx, "k")
	assert.ErrorIs(t, err, api.ErrApprovalNotFound)
}

func TestPage_HandoffSignsTheBrowserInAndLeavesTheAddress(t *testing.T) {
	rig := newHandoffRig(t)
	request := rig.create(t, map[string]any{"pr": 1})
	// muster's own sign-in would put someone else in this browser: the page
	// must not use it.
	rig.identity.person = bob
	_, ticket := rig.issue(t, alice, rig.page.URL(request.ID))
	client := browser(t)

	link := rig.page.URL(request.ID) + "?redirect=" + url.QueryEscape("https://portal.example.com/s/1") + "&" + HandoffParam + "=" + ticket
	resp, err := client.Get(link)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, rig.page.URL(request.ID), resp.Header.Get("Location"), "the ticket leaves the address")
	assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))

	resp, body := rig.open(t, client, resp.Header.Get("Location"))
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Contains(t, body, "Approve this action?")
	assert.Empty(t, rig.identity.challenges, "no sign-in at the identity provider")

	// The decision keeps its checks and the link's return.
	token := tokenPattern.FindStringSubmatch(body)[1]
	resp, err = client.PostForm(rig.page.URL(request.ID), url.Values{"token": {token}, "decision": {"approve"}})
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "https://portal.example.com/s/1?elicitationId="+request.ElicitationID, resp.Header.Get("Location"))

	// The ticket is spent: another browser with the same link signs in itself.
	other := browser(t)
	resp, body = rig.open(t, other, link)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, body)
	assert.NotContains(t, body, "name=\"token\"")
}

func TestPage_InvalidHandoffFallsBackToSignIn(t *testing.T) {
	rig := newHandoffRig(t)
	request := rig.create(t, map[string]any{"pr": 1})
	client := browser(t)

	resp, err := client.Get(rig.page.URL(request.ID) + "?" + HandoffParam + "=guessed")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, rig.page.URL(request.ID), resp.Header.Get("Location"))

	resp, body := rig.open(t, client, resp.Header.Get("Location"))
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.NotEmpty(t, rig.identity.returns, "signed in at the identity provider")
	assert.NotContains(t, body, "guessed", "nothing about the ticket is shown")

	// An unknown request: the ticket is dropped, the page signs in as for any id.
	resp, err = client.Get(rig.page.URL("unknown") + "?" + HandoffParam + "=guessed")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, rig.page.URL("unknown"), resp.Header.Get("Location"))
}

func TestPage_HandoffWithoutRedeemerIsDropped(t *testing.T) {
	rig := newPageRig(t)
	request := rig.create(t, map[string]any{"pr": 1})
	resp, err := browser(t).Get(rig.page.URL(request.ID) + "?" + HandoffParam + "=x")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, rig.page.URL(request.ID), resp.Header.Get("Location"))
}
