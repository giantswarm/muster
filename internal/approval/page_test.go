package approval

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// fakeIdentity plays muster's identity provider: the sign-in returns at once
// with a code that redeems to whoever the test put in the browser, only with
// the PKCE verifier of the challenge it was started with.
type fakeIdentity struct {
	person     api.SignInIdentity
	challenges map[string]string // code -> challenge
	returns    map[string]string // code -> return URL
}

func (f *fakeIdentity) SignInURL(_ context.Context, returnURL *url.URL, state, challenge string, _ api.SignInIdentity) (string, error) {
	code := fmt.Sprintf("code-%d", len(f.challenges))
	f.challenges[code] = challenge
	f.returns[code] = returnURL.String()
	return returnURL.String() + "?code=" + code + "&state=" + url.QueryEscape(state), nil
}

func (f *fakeIdentity) CheckRefusal(api.SignInIdentity) error { return nil }

func (f *fakeIdentity) Identity(_ context.Context, returnURL *url.URL, code, verifier string) (api.SignInIdentity, error) {
	sum := sha256.Sum256([]byte(verifier))
	if f.challenges[code] != base64.RawURLEncoding.EncodeToString(sum[:]) || f.returns[code] != returnURL.String() {
		return api.SignInIdentity{}, fmt.Errorf("invalid identity code")
	}
	delete(f.challenges, code)
	return f.person, nil
}

type pageRig struct {
	store    *MemoryStore
	identity *fakeIdentity
	server   *httptest.Server
	page     *Page
}

func newPageRig(t *testing.T) *pageRig {
	t.Helper()
	rig := &pageRig{store: NewMemoryStore(), identity: &fakeIdentity{person: alice, challenges: map[string]string{}, returns: map[string]string{}}}
	mux := http.NewServeMux()
	rig.server = httptest.NewServer(mux)
	t.Cleanup(rig.server.Close)
	rig.page = &Page{
		PublicURL:       rig.server.URL,
		Installation:    "example",
		Store:           func() api.ApprovalStore { return rig.store },
		Identity:        func() api.BrowserIdentity { return rig.identity },
		RedirectAllowed: func(raw string) bool { return strings.HasPrefix(raw, "https://portal.example.com/") },
	}
	rig.page.Register(mux)
	return rig
}

func (rig *pageRig) create(t *testing.T, args map[string]any) *api.ApprovalRequest {
	t.Helper()
	request := newRequest(t, "req-"+fmt.Sprint(len(args)), alice, "s1", args)
	request.Display = api.ApprovalDisplay{Message: "An agent wants to run Merge pull request on github.", Title: "Merge pull request", Connector: "github", ChangesData: true,
		Arguments: []api.ApprovalArgument{{Key: "pr", Label: "Pr", Value: fmt.Sprint(args["pr"])}}, Setting: "ask", Source: "connector"}
	_, err := rig.store.Create(context.Background(), request)
	require.NoError(t, err)
	return request
}

func browser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// open follows the page through its sign-in and returns the rendered page.
func (rig *pageRig) open(t *testing.T, client *http.Client, link string) (*http.Response, string) {
	t.Helper()
	for range 4 {
		resp, err := client.Get(link)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
			return resp, string(body)
		}
		link = resp.Header.Get("Location")
	}
	t.Fatal("too many redirects")
	return nil, ""
}

var tokenPattern = regexp.MustCompile(`name="token" value="([^"]+)"`)

func TestPage_ApproveAndReturn(t *testing.T) {
	rig := newPageRig(t)
	request := rig.create(t, map[string]any{"pr": "<script>alert(1)</script>"})
	client := browser(t)

	resp, body := rig.open(t, client, rig.page.URL(request.ID)+"?redirect="+url.QueryEscape("https://portal.example.com/s/1?elicitationId="+request.ElicitationID))
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'")
	// Chromium applies form-action to the decision's redirect, under this
	// page's policy: the return's origin, and only it, is admitted.
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "form-action 'self' https://portal.example.com;")
	assert.Contains(t, body, "Approve this action?")
	assert.Contains(t, body, "Changes data")
	assert.Contains(t, body, "&lt;script&gt;alert(1)&lt;/script&gt;", "argument values are escaped")
	assert.NotContains(t, body, "<script>alert(1)</script>")
	assert.Contains(t, body, " disabled>Approve", "buttons start inert")
	assert.Contains(t, body, "getElementById('decide').addEventListener('submit'", "the buttons disable on submit")

	// Viewing changed nothing.
	got, err := rig.store.Get(context.Background(), request.ID)
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalStatusPending, got.Status)

	token := tokenPattern.FindStringSubmatch(body)[1]
	resp, err = client.PostForm(rig.page.URL(request.ID), url.Values{"token": {token}, "decision": {"approve"}})
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	location, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "portal.example.com", location.Host)
	assert.Equal(t, request.ElicitationID, location.Query().Get("elicitationId"))
	assert.NotContains(t, location.String(), "approve", "the return never carries the decision")
	assert.NotContains(t, location.String(), request.ID, "nor the approval id")

	got, err = rig.store.Get(context.Background(), request.ID)
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalStatusApproved, got.Status)
	assert.Equal(t, alice, got.DecidedBy)

	// The same form again (a double click) changes nothing and says what
	// stands.
	resp, err = client.PostForm(rig.page.URL(request.ID), url.Values{"token": {token}, "decision": {"decline"}})
	require.NoError(t, err)
	again, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, string(again), "This request was already approved")
	assert.NotContains(t, string(again), "Nothing was decided")
	got, err = rig.store.Get(context.Background(), request.ID)
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalStatusApproved, got.Status, "the second submit changed nothing")
}

// The first allowed return of a browser's flow is kept: a later link with
// another return does not replace it.
func TestPage_ReturnIsSetOnce(t *testing.T) {
	rig := newPageRig(t)
	request := rig.create(t, map[string]any{"pr": 1})
	client := browser(t)
	_, _ = rig.open(t, client, rig.page.URL(request.ID)+"?redirect="+url.QueryEscape("https://portal.example.com/s/1"))
	resp, body := rig.open(t, client, rig.page.URL(request.ID)+"?redirect="+url.QueryEscape("https://portal.example.com/elsewhere?x=1"))
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	token := tokenPattern.FindStringSubmatch(body)[1]
	resp, err := client.PostForm(rig.page.URL(request.ID), url.Values{"token": {token}, "decision": {"approve"}})
	require.NoError(t, err)
	_ = resp.Body.Close()
	location, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "/s/1", location.Path)
}

func TestPage_DeclineWithoutReturn(t *testing.T) {
	rig := newPageRig(t)
	request := rig.create(t, map[string]any{"pr": 1.0})
	client := browser(t)
	// A return not on the allowlist is dropped: the page answers itself.
	_, body := rig.open(t, client, rig.page.URL(request.ID)+"?redirect="+url.QueryEscape("https://evil.example.com/x"))
	token := tokenPattern.FindStringSubmatch(body)[1]
	resp, err := client.PostForm(rig.page.URL(request.ID), url.Values{"token": {token}, "decision": {"decline"}})
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Location"))
	got, err := rig.store.Get(context.Background(), request.ID)
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalStatusDeclined, got.Status)
}

// Another person signed in sees whose request it is, no arguments, no buttons,
// and cannot decide.
func TestPage_OtherPerson(t *testing.T) {
	rig := newPageRig(t)
	request := rig.create(t, map[string]any{"pr": "secret-value"})
	rig.identity.person = bob
	client := browser(t)
	resp, body := rig.open(t, client, rig.page.URL(request.ID))
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Contains(t, body, "someone else")
	assert.Contains(t, body, "alice")
	assert.NotContains(t, body, "secret-value")
	assert.NotContains(t, body, "name=\"token\"")
}

// A decision needs the browser's cookie and the form's token: a bearer or an
// actor token never decides, nor does a forged form.
func TestPage_DecisionNeedsBrowserAndToken(t *testing.T) {
	rig := newPageRig(t)
	request := rig.create(t, map[string]any{"pr": 1.0})

	// No browser cookie, a bearer token.
	req, err := http.NewRequest(http.MethodPost, rig.page.URL(request.ID), strings.NewReader("decision=approve&token=x"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer actor-jwt")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// The signed-in browser with a wrong token, and from another origin.
	client := browser(t)
	rig.open(t, client, rig.page.URL(request.ID))
	resp, err = client.PostForm(rig.page.URL(request.ID), url.Values{"token": {"forged"}, "decision": {"approve"}})
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	req, err = http.NewRequest(http.MethodPost, rig.page.URL(request.ID), strings.NewReader("decision=approve"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err = client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	got, err := rig.store.Get(context.Background(), request.ID)
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalStatusPending, got.Status)
}

func TestPage_ExpiredAndUnavailable(t *testing.T) {
	rig := newPageRig(t)
	// Before sign-in an unknown id answers like someone else's live request:
	// a redirect to sign in.
	foreign := rig.create(t, map[string]any{"pr": 1})
	for _, id := range []string{"unknown", foreign.ID} {
		resp, err := browser(t).Get(rig.page.URL(id))
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusFound, resp.StatusCode, id)
	}

	resp, body := rig.open(t, browser(t), rig.page.URL("unknown"))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, body, "expired")
	assert.Empty(t, resp.Header.Get("Location"), "error pages never redirect")

	rig.page.Store = func() api.ApprovalStore { return nil }
	resp, _ = rig.open(t, browser(t), rig.page.URL("unknown"))
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// A sign-in code that comes back to another browser is refused.
func TestPage_SignInBoundToBrowser(t *testing.T) {
	rig := newPageRig(t)
	request := rig.create(t, map[string]any{"pr": 1.0})
	first := browser(t)
	resp, err := first.Get(rig.page.URL(request.ID))
	require.NoError(t, err)
	_ = resp.Body.Close()
	signIn := resp.Header.Get("Location")
	require.Contains(t, signIn, "code=")

	resp, err = browser(t).Get(signIn)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.NotEqual(t, http.StatusSeeOther, resp.StatusCode)
}

// Regression: bidi overrides and zero-width
// characters in an argument reach the page only as visible markers, in the
// labelled value and in "Show details"; the connector is shown with its
// server key, not only its display name.
func TestPage_MarksInvisibleCharacters(t *testing.T) {
	p := &Page{PublicURL: "https://muster.example.com"}
	req := &api.ApprovalRequest{ID: "x", Identity: alice, Status: api.ApprovalStatusPending, ExpiresAt: time.Now().Add(time.Minute),
		Target:    api.ApprovalTarget{Kind: api.ApprovalTargetTool, Server: "team-a/github", Name: "delete_repository"},
		Arguments: "{\"repo\":\"acme/\u202ediorp\u202c\u200b\"}",
		Display: api.ApprovalDisplay{Title: "Delete repository", Connector: "GitHub",
			Arguments: []api.ApprovalArgument{{Key: "repo", Label: "Repo", Value: "\"acme/\u202ediorp\u202c\u200b\"", Type: "string"}}}}
	w := httptest.NewRecorder()
	p.renderRequest(w, req, &api.ApprovalPageFlow{Identity: alice, Token: "t"})
	body := w.Body.String()
	for _, r := range []rune{'\u202e', '\u202c', '\u200b'} {
		assert.NotContains(t, body, string(r))
	}
	assert.Equal(t, 2, strings.Count(body, "⟨U&#43;202E⟩"), "value and details both marked")
	assert.Contains(t, body, "⟨U&#43;200B⟩") // html/template writes + as &#43;
	assert.Contains(t, body, "(string)")
	assert.Contains(t, body, "<code>team-a/github</code>", "the connector's server key is shown")
}

// Blank look-alikes are marked too.
func TestVisible_BlankLookAlikes(t *testing.T) {
	for _, r := range []rune{'\u00a0', '\u2003', '\u3000', '\u3164', '\u115f', '\uffa0', '\u2800', '\ufe0f', '\u034f'} {
		assert.NotEqual(t, "a"+string(r)+"b", Visible("a"+string(r)+"b"), "U+%04X", r)
	}
	assert.Equal(t, "a b", Visible("a b"))
}

func TestVisible(t *testing.T) {
	assert.Equal(t, "plain ü", Visible("plain ü"))
	assert.Equal(t, "a⟨U+000A⟩b⟨U+2028⟩c⟨U+FEFF⟩", Visible("a\nb\u2028c\ufeff"))
}

// The decision a browser sends after the sign-in round trip is taken: under
// the page's Referrer-Policy: no-referrer a browser sends Origin: null on
// the form's same-origin POST (Fetch; seen in Chromium), which must not
// count as another origin (lab defect: "The decision did not come from this
// page"). A refused decision says plainly that nothing was decided.
func TestPage_DecisionAfterSignInWithOriginNull(t *testing.T) {
	rig := newPageRig(t)
	request := rig.create(t, map[string]any{"pr": 7})
	client := browser(t)
	resp, body := rig.open(t, client, rig.page.URL(request.ID)+"?redirect="+url.QueryEscape("https://portal.example.com/s/1"))
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	token := tokenPattern.FindStringSubmatch(body)[1]

	post := func(token string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, rig.page.URL(request.ID), strings.NewReader(url.Values{"token": {token}, "decision": {"approve"}}.Encode()))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "null")
		resp, err := client.Do(req)
		require.NoError(t, err)
		return resp
	}
	refused := post("forged")
	text, _ := io.ReadAll(refused.Body)
	_ = refused.Body.Close()
	assert.Equal(t, http.StatusForbidden, refused.StatusCode)
	assert.Contains(t, string(text), "Nothing was decided")
	assert.Contains(t, string(text), "this page is out of date", "the refusal names its cause (lab R1-2)")
	assert.Contains(t, string(text), "open the approval link again")

	decided := post(token)
	_ = decided.Body.Close()
	require.Equal(t, http.StatusSeeOther, decided.StatusCode)
	got, err := rig.store.Get(context.Background(), request.ID)
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalStatusApproved, got.Status)
}

// Every outcome says whether the request was approved, declined or nothing
// was decided, and what to do next.
func TestPage_OutcomesSayWhatHappened(t *testing.T) {
	decide := func(t *testing.T, rig *pageRig, request *api.ApprovalRequest, client *http.Client, decision string) string {
		_, body := rig.open(t, client, rig.page.URL(request.ID))
		token := tokenPattern.FindStringSubmatch(body)[1]
		resp, err := client.PostForm(rig.page.URL(request.ID), url.Values{"token": {token}, "decision": {decision}})
		require.NoError(t, err)
		text, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return string(text)
	}
	rig := newPageRig(t)
	approved := rig.create(t, map[string]any{"pr": 1})
	assert.Contains(t, decide(t, rig, approved, browser(t), "approve"), "You approved this request")
	_, body := rig.open(t, browser(t), rig.page.URL(approved.ID))
	assert.Contains(t, body, "This request was already approved")

	declined := rig.create(t, map[string]any{"pr": 2, "x": 1})
	assert.Contains(t, decide(t, rig, declined, browser(t), "decline"), "You declined this request")
	_, body = rig.open(t, browser(t), rig.page.URL(declined.ID))
	assert.Contains(t, body, "already declined")
	assert.Contains(t, body, "It will not run")

	_, body = rig.open(t, browser(t), rig.page.URL("gone"))
	assert.Contains(t, body, "Nothing to decide: this request expired or was already used")
	assert.Contains(t, body, "run it again")
}

// Another person sees whose request it is by email or name, not the
// identity provider's opaque subject (lab R1-1); a workflow's name is shown
// once (lab R1-3).
func TestPage_RequesterAndWorkflowName(t *testing.T) {
	p := &Page{PublicURL: "https://muster.example.com"}
	request := &api.ApprovalRequest{ID: "x", Identity: alice, RequesterName: "alice@example.com", Status: api.ApprovalStatusPending, ExpiresAt: time.Now().Add(time.Minute)}
	w := httptest.NewRecorder()
	p.renderRequest(w, request, &api.ApprovalPageFlow{Identity: bob})
	assert.Contains(t, w.Body.String(), "It was made for alice@example.com;")
	assert.NotContains(t, w.Body.String(), alice.Issuer)

	request.RequesterName = ""
	w = httptest.NewRecorder()
	p.renderRequest(w, request, &api.ApprovalPageFlow{Identity: bob})
	assert.Contains(t, w.Body.String(), "It was made for alice at https://dex.example.com;", "the subject only without a name")

	workflow := &api.ApprovalRequest{ID: "w", Identity: alice, Status: api.ApprovalStatusPending, ExpiresAt: time.Now().Add(time.Minute),
		Target:  api.ApprovalTarget{Kind: api.ApprovalTargetWorkflow, Name: "release"},
		Display: api.ApprovalDisplay{Title: "workflow release"}}
	w = httptest.NewRecorder()
	p.renderRequest(w, workflow, &api.ApprovalPageFlow{Identity: alice, Token: "t"})
	assert.NotContains(t, w.Body.String(), "<code>release</code>")
}

// The agent's reason is shown in its own box, escaped and with invisible
// characters marked, and the page names the agent and the client the
// approval is bound to.
func TestPage_AgentSaysAndBoundClient(t *testing.T) {
	p := &Page{PublicURL: "https://muster.example.com"}
	request := &api.ApprovalRequest{ID: "x", Identity: alice, Status: api.ApprovalStatusPending, ExpiresAt: time.Now().Add(time.Minute),
		Agent: "kagent/alert-review", SessionID: "session-1/client:portal",
		Display: api.ApprovalDisplay{Title: "Merge pull request", AgentSays: "<b>trust me</b>\u200b"}}
	w := httptest.NewRecorder()
	p.renderRequest(w, request, &api.ApprovalPageFlow{Identity: alice, Token: "t"})
	body := w.Body.String()
	assert.Contains(t, body, "<h2>Agent says</h2>")
	assert.Contains(t, body, "&lt;b&gt;trust me&lt;/b&gt;⟨U&#43;200B⟩")
	assert.Contains(t, body, "Agent kagent/alert-review")
	assert.Contains(t, body, "Client client:portal")

	request.Display.AgentSays, request.SessionID = "", "mcp-actor-abc"
	w = httptest.NewRecorder()
	p.renderRequest(w, request, &api.ApprovalPageFlow{Identity: alice, Token: "t"})
	assert.NotContains(t, w.Body.String(), "Agent says")
	assert.NotContains(t, w.Body.String(), "Client ")
}

// More than two combining marks on one character are marked, so a stack of
// them cannot draw over the lines around it; text with accents keeps them.
func TestVisible_StackedCombiningMarks(t *testing.T) {
	assert.Equal(t, "e\u0301\u0302", Visible("e\u0301\u0302"), "two marks stay")
	assert.Equal(t, "e\u0301\u0302⟨U+0303⟩⟨U+0304⟩", Visible("e\u0301\u0302\u0303\u0304"))
	assert.Equal(t, "a\u0301b\u0301", Visible("a\u0301b\u0301"), "the count starts again on every base")
	assert.Equal(t, "x\u20dd\u20dd⟨U+20DD⟩", Visible("x\u20dd\u20dd\u20dd"), "enclosing marks count too")
}

// The requester's name can come from the identity provider's own claim, so
// a hidden or bidirectional character in it is shown as a marker, like any
// other text the page prints.
func TestPage_RequesterNameIsMadeVisible(t *testing.T) {
	request := &api.ApprovalRequest{Identity: alice, RequesterName: "alice\u202Eevil"}
	assert.Equal(t, "alice⟨U+202E⟩evil", requester(request))
}
