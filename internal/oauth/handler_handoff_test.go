package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// fakeRedeemer redeems the ticket "good" once, for whoever the call names,
// and records what it was asked. The ticket was issued to mallory@example.com.
type fakeRedeemer struct {
	used               bool
	kind, target, want string
}

func (f *fakeRedeemer) redeem(_ context.Context, ticket, kind, target string, want api.SignInIdentity) (api.SignInHandoff, bool) {
	f.kind, f.target, f.want = kind, target, want.Subject
	if ticket != "good" || f.used || want != testSignIn {
		return api.SignInHandoff{}, false
	}
	f.used = true
	return api.SignInHandoff{Kind: kind, Target: target, Identity: want, Name: "mallory@example.com"}, true
}

func registerRedeemer(t *testing.T, r api.HandoffRedeemer) {
	t.Helper()
	api.RegisterHandoffRedeemer(r)
	t.Cleanup(func() { api.RegisterHandoffRedeemer(nil) })
}

// post submits the confirmation form from the browser.
func (b *browser) post(handler *Handler, form url.Values, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/oauth/proxy/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for _, c := range b.cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	handler.HandleStart(rr, req)
	b.cookies = append(b.cookies, rr.Result().Cookies()...)
	return rr
}

var confirmToken = regexp.MustCompile(`name="token" value="([^"]+)"`)

// handOver redeems the ticket in the browser and returns the clean start's
// answer: the confirmation.
func handOver(t *testing.T, handler *Handler, b *browser, encodedState string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	handed := b.start(handler, "state="+url.QueryEscape(encodedState)+"&handoff=good")
	require.Equal(t, http.StatusSeeOther, handed.Code, handed.Body.String())
	clean, err := url.Parse(handed.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/oauth/proxy/start", clean.Path)
	require.Equal(t, url.Values{"state": {encodedState}}, clean.Query(), "the ticket leaves the address")
	page := b.start(handler, clean.RawQuery)
	match := confirmToken.FindStringSubmatch(page.Body.String())
	token := ""
	if match != nil {
		token = match[1]
	}
	return page, token
}

func TestHandler_Handoff_ConfirmsPersonAndConnectorBeforeTheSignIn(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	identity := &fakeBrowserIdentity{subject: "someone-else"}
	registerBrowserIdentity(t, identity)
	redeemer := &fakeRedeemer{}
	registerRedeemer(t, redeemer.redeem)
	opener := &browser{}

	page, token := handOver(t, handler, opener, encodedState)
	require.Equal(t, api.HandoffKindSignIn, redeemer.kind)
	require.Equal(t, encodedState, redeemer.target)
	require.Equal(t, testSignIn.Subject, redeemer.want, "redeemed for the person the flow was started for")

	// IA-02: the handed-over browser is not sent upstream; it is asked first,
	// on muster's own page naming the person and the connector.
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	body := page.Body.String()
	assert.Contains(t, body, "Connect <strong>test-server</strong> as <strong>mallory@example.com</strong>?")
	assert.Contains(t, body, `value="continue" disabled`, "buttons start inert and arm after a second")
	require.NotEmpty(t, token)
	csp := page.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "form-action 'self' https://idp.example.com;", "the continue's redirect may reach the connector's sign-in")
	assert.Contains(t, csp, "frame-ancestors 'none'")
	assert.Equal(t, "DENY", page.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-store", page.Header().Get("Cache-Control"))
	assert.Empty(t, identity.challenge, "no identity check")
	assert.Empty(t, handler.client.stateStore.Update(encodedState, func(*OAuthState) {}).BrowserBinding, "nothing bound before the confirmation")

	confirmed := opener.post(handler, url.Values{"state": {encodedState}, "token": {token}, "decision": {"continue"}}, "https://muster.example.com")
	require.Equal(t, http.StatusSeeOther, confirmed.Code, confirmed.Body.String())
	require.Equal(t, "https://idp.example.com/authorize?state=abc", confirmed.Header().Get("Location"))

	again := opener.start(handler, "state="+url.QueryEscape(encodedState))
	require.Equal(t, http.StatusFound, again.Code)
	require.Equal(t, "https://idp.example.com/authorize?state=abc", again.Header().Get("Location"),
		"the confirmed browser goes straight to the server's sign-in")

	// Another browser holding the spent link is sent to the identity check.
	other := &browser{}
	spent := other.start(handler, "state="+url.QueryEscape(encodedState)+"&handoff=good")
	require.Equal(t, http.StatusSeeOther, spent.Code)
	sendToSignIn(t, handler, other, encodedState)
}

func TestHandler_Handoff_CancelConnectsNothing(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	registerRedeemer(t, (&fakeRedeemer{}).redeem)
	opener := &browser{}

	_, token := handOver(t, handler, opener, encodedState)
	cancelled := opener.post(handler, url.Values{"state": {encodedState}, "token": {token}, "decision": {"cancel"}}, "")
	require.Equal(t, http.StatusOK, cancelled.Code)
	assert.Contains(t, cancelled.Body.String(), "Nothing was connected to test-server")
	state := handler.client.stateStore.Update(encodedState, func(*OAuthState) {})
	assert.Empty(t, state.BrowserBinding)
	assert.Empty(t, state.HandoffBinding, "the handoff is dropped")

	// The link now asks for muster's own sign-in, as without a ticket.
	sendToSignIn(t, handler, opener, encodedState)
}

func TestHandler_Handoff_ConfirmationRefusals(t *testing.T) {
	cases := []struct {
		name   string
		form   func(state, token string) url.Values
		origin string
		other  bool
	}{
		{name: "another browser", other: true, form: func(s, tok string) url.Values {
			return url.Values{"state": {s}, "token": {tok}, "decision": {"continue"}}
		}},
		{name: "no token", form: func(s, _ string) url.Values {
			return url.Values{"state": {s}, "decision": {"continue"}}
		}},
		{name: "wrong token", form: func(s, _ string) url.Values {
			return url.Values{"state": {s}, "token": {"forged"}, "decision": {"continue"}}
		}},
		{name: "another origin", origin: "https://evil.example.com", form: func(s, tok string) url.Values {
			return url.Values{"state": {s}, "token": {tok}, "decision": {"continue"}}
		}},
		{name: "unknown flow", form: func(_, tok string) url.Values {
			return url.Values{"state": {"bogus"}, "token": {tok}, "decision": {"continue"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
			registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
			registerRedeemer(t, (&fakeRedeemer{}).redeem)
			opener := &browser{}
			_, token := handOver(t, handler, opener, encodedState)
			poster := opener
			if tc.other {
				poster = &browser{}
			}
			answer := poster.post(handler, tc.form(encodedState, token), tc.origin)
			assert.Equal(t, http.StatusBadRequest, answer.Code)
			assert.Empty(t, answer.Header().Get("Location"), "nothing goes upstream")
			assert.Empty(t, handler.client.stateStore.Update(encodedState, func(*OAuthState) {}).BrowserBinding)
		})
	}
}

// Without muster's own OAuth server the start has no POST.
func TestHandler_Handoff_NoConfirmationWithoutBrowserIdentity(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	answer := (&browser{}).post(handler, url.Values{"state": {encodedState}}, "")
	assert.Equal(t, http.StatusMethodNotAllowed, answer.Code)
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

// A handed-over flow keeps its pending confirmation on Valkey too, so any
// replica shows and takes it.
func TestValkeyStateEntry_KeepsTheHandoffConfirmation(t *testing.T) {
	state := &OAuthState{Nonce: "n", HandoffBinding: "hb", HandoffToken: "ht", HandoffName: "mallory@example.com", BrowserBinding: "bb"}
	back := stateToEntry(state).toState()
	assert.Equal(t, "hb", back.HandoffBinding)
	assert.Equal(t, "ht", back.HandoffToken)
	assert.Equal(t, "mallory@example.com", back.HandoffName)
	assert.Equal(t, "bb", back.BrowserBinding)
}
