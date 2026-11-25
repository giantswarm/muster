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
		Installation:    "gazelle",
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
	assert.Contains(t, body, "Approve this action?")
	assert.Contains(t, body, "Changes data")
	assert.Contains(t, body, "&lt;script&gt;alert(1)&lt;/script&gt;", "argument values are escaped")
	assert.NotContains(t, body, "<script>alert(1)</script>")
	assert.Contains(t, body, " disabled>Approve", "buttons start inert")

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

	// The same form again is refused: its flow is used up.
	resp, err = client.PostForm(rig.page.URL(request.ID), url.Values{"token": {token}, "decision": {"decline"}})
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
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
