package oauth

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// fakeRedeemer redeems the ticket "good" once, for whoever the call names,
// and records what it was asked.
type fakeRedeemer struct {
	used               bool
	kind, target, want string
}

func (f *fakeRedeemer) redeem(_ context.Context, ticket, kind, target string, want api.SignInIdentity) bool {
	f.kind, f.target, f.want = kind, target, want.Subject
	if ticket != "good" || f.used || want != testSignIn {
		return false
	}
	f.used = true
	return true
}

func registerRedeemer(t *testing.T, r api.HandoffRedeemer) {
	t.Helper()
	api.RegisterHandoffRedeemer(r)
	t.Cleanup(func() { api.RegisterHandoffRedeemer(nil) })
}

func TestHandler_Handoff_BindsTheBrowserWithoutTheIdentityCheck(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	identity := &fakeBrowserIdentity{subject: "someone-else"}
	registerBrowserIdentity(t, identity)
	redeemer := &fakeRedeemer{}
	registerRedeemer(t, redeemer.redeem)
	owner := &browser{}

	handed := owner.start(handler, "state="+url.QueryEscape(encodedState)+"&handoff=good")
	require.Equal(t, http.StatusSeeOther, handed.Code, handed.Body.String())
	clean, err := url.Parse(handed.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/oauth/proxy/start", clean.Path)
	require.Equal(t, url.Values{"state": {encodedState}}, clean.Query(), "the ticket leaves the address")
	require.Equal(t, api.HandoffKindSignIn, redeemer.kind)
	require.Equal(t, encodedState, redeemer.target)
	require.Equal(t, testSignIn.Subject, redeemer.want, "redeemed for the person the flow was started for")

	again := owner.start(handler, clean.RawQuery)
	require.Equal(t, http.StatusFound, again.Code)
	require.Equal(t, "https://idp.example.com/authorize?state=abc", again.Header().Get("Location"),
		"the bound browser goes straight to the server's sign-in")
	require.Empty(t, identity.challenge, "no identity check")

	// Another browser holding the spent link is sent to the identity check.
	other := &browser{}
	spent := other.start(handler, "state="+url.QueryEscape(encodedState)+"&handoff=good")
	require.Equal(t, http.StatusSeeOther, spent.Code)
	sendToSignIn(t, handler, other, encodedState)
}

func TestHandler_Handoff_InvalidTicketFallsBackToTheIdentityCheck(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	registerRedeemer(t, (&fakeRedeemer{}).redeem)
	b := &browser{}

	handed := b.start(handler, "state="+url.QueryEscape(encodedState)+"&handoff=bad")
	require.Equal(t, http.StatusSeeOther, handed.Code)
	require.NotContains(t, handed.Header().Get("Location"), "handoff")
	sendToSignIn(t, handler, b, encodedState)
}
