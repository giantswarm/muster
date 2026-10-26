package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
)

// testSignIn is who the test flows' caller authenticated as.
var testSignIn = api.SignInIdentity{Issuer: testIdentityIssuer, Subject: "user-1"}

const testIdentityIssuer = "https://dex.example.com"

// fakeBrowserIdentity signs every browser in as subject at
// testIdentityIssuer. Like the real identity flow, it redeems a code only
// with the verifier of the challenge the sign-in was started with.
type fakeBrowserIdentity struct {
	subject   string
	challenge string
}

func (f *fakeBrowserIdentity) SignInURL(_ context.Context, _ *url.URL, state, codeChallenge string) (string, error) {
	f.challenge = codeChallenge
	return "https://dex.example.com/auth?state=" + url.QueryEscape(state), nil
}

func (f *fakeBrowserIdentity) Identity(_ context.Context, _ *url.URL, code, codeVerifier string) (api.SignInIdentity, error) {
	sum := sha256.Sum256([]byte(codeVerifier))
	if code == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
		return api.SignInIdentity{}, errors.New("invalid identity code")
	}
	return api.SignInIdentity{Issuer: testIdentityIssuer, Subject: f.subject}, nil
}

func registerBrowserIdentity(t *testing.T, identity api.BrowserIdentity) {
	t.Helper()
	api.RegisterBrowserIdentity(identity)
	t.Cleanup(func() { api.RegisterBrowserIdentity(nil) })
}

// browser keeps the cookies muster sets, like a real browser does.
type browser struct {
	cookies []*http.Cookie
}

func (b *browser) get(handle http.HandlerFunc, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	for _, c := range b.cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	handle(rr, req)
	b.cookies = append(b.cookies, rr.Result().Cookies()...)
	return rr
}

func (b *browser) start(handler *Handler, query string) *httptest.ResponseRecorder {
	return b.get(handler.HandleStart, "/oauth/proxy/start?"+query)
}

// passIdentityCheck runs the browser through the start endpoint and back from
// the identity sign-in, returning the answer to its return.
func passIdentityCheck(t *testing.T, handler *Handler, b *browser, encodedState string) *httptest.ResponseRecorder {
	t.Helper()
	return b.start(handler, "code=identity-code&state="+url.QueryEscape(sendToSignIn(t, handler, b, encodedState)))
}

// sendToSignIn opens the start link in the browser, which is sent to muster's
// identity provider, and returns the state the provider brings back.
func sendToSignIn(t *testing.T, handler *Handler, b *browser, encodedState string) string {
	t.Helper()
	start := b.start(handler, "state="+url.QueryEscape(encodedState))
	require.Equal(t, http.StatusFound, start.Code)
	signIn, err := url.Parse(start.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "dex.example.com", signIn.Host, "the browser signs in to muster's identity provider first")
	return signIn.Query().Get("state")
}

func TestHandler_IdentityCheck_OwnerContinuesUpstream(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	owner := &browser{}

	back := passIdentityCheck(t, handler, owner, encodedState)

	require.Equal(t, http.StatusFound, back.Code, back.Body.String())
	require.Equal(t, "https://idp.example.com/authorize?state=abc", back.Header().Get("Location"))
	require.NotEmpty(t, handler.client.stateStore.Update(encodedState, func(*OAuthState) {}).BrowserBinding)

	again := owner.start(handler, "state="+url.QueryEscape(encodedState))
	require.Equal(t, "https://idp.example.com/authorize?state=abc", again.Header().Get("Location"),
		"a checked flow goes straight upstream in the browser that passed the check")
}

// SECURITY: a sign-in link forwarded to, or phished from, another person must
// not connect that person's account to the owner's session.
func TestHandler_IdentityCheck_RefusesSomeoneElse(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	identity := &fakeBrowserIdentity{subject: "user-2"}
	registerBrowserIdentity(t, identity)

	back := passIdentityCheck(t, handler, &browser{}, encodedState)

	require.Equal(t, http.StatusBadRequest, back.Code)
	require.Contains(t, back.Body.String(), summaryOtherPerson)
	require.Empty(t, back.Header().Get("Location"))
	require.Empty(t, handler.client.stateStore.Update(encodedState, func(*OAuthState) {}).BrowserBinding)

	identity.subject = "user-1"
	owner := passIdentityCheck(t, handler, &browser{}, encodedState)
	require.Equal(t, http.StatusFound, owner.Code, "the link stays usable by its owner")
}

// A session whose subject a trusted issuer maps from email is checked by the
// raw sub it authenticated with, not by the mapped subject.
func TestHandler_IdentityCheck_ComparesTheRawSubject(t *testing.T) {
	handler, _ := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "CgVhbGljZRIGZ2l0aHVi"})
	encodedState, err := handler.client.stateStore.GenerateState(StateParams{
		SessionID: "session-1", UserID: "alice@example.com", ServerName: "test-server",
		SignIn: api.SignInIdentity{Issuer: testIdentityIssuer, Subject: "CgVhbGljZRIGZ2l0aHVi"},
	}, func(string) (string, error) { return "https://idp.example.com/authorize?state=abc", nil })
	require.NoError(t, err)

	back := passIdentityCheck(t, handler, &browser{}, encodedState)

	require.Equal(t, http.StatusFound, back.Code, back.Body.String())
}

// SECURITY: a person is the issuer and its sub together (OIDC Core §5.7). A
// link issued to an identity of another issuer, or to no identity, cannot be
// confirmed at muster's identity provider.
func TestHandler_IdentityCheck_RefusesAnIdentityItCannotConfirm(t *testing.T) {
	for name, signIn := range map[string]api.SignInIdentity{
		"another issuer": {Issuer: "https://dex.other.example.com", Subject: "user-1"},
		"no identity":    {},
	} {
		t.Run(name, func(t *testing.T) {
			handler, _ := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
			registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
			encodedState, err := handler.client.stateStore.GenerateState(StateParams{
				SessionID: "session-1", UserID: "user-1", ServerName: "test-server", SignIn: signIn,
			}, func(string) (string, error) { return "https://idp.example.com/authorize?state=abc", nil })
			require.NoError(t, err)

			back := passIdentityCheck(t, handler, &browser{}, encodedState)

			require.Equal(t, http.StatusBadRequest, back.Code)
			require.Contains(t, back.Body.String(), summaryUnconfirmedPerson)
			require.Empty(t, back.Header().Get("Location"))
		})
	}
}

// SECURITY: the owner passing the check and then forwarding the link must not
// let the browser it is forwarded to go upstream unchecked.
func TestHandler_IdentityCheck_ForwardedCheckedLinkIsCheckedAgain(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	require.Equal(t, http.StatusFound, passIdentityCheck(t, handler, &browser{}, encodedState).Code)

	sendToSignIn(t, handler, &browser{}, encodedState)
}

// SECURITY: the owner signing in at the check and forwarding the return URL
// must not bind the flow to the browser it is forwarded to.
func TestHandler_IdentityCheck_RefusesAReturnInAnotherBrowser(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	identityState := sendToSignIn(t, handler, &browser{}, encodedState)

	for name, other := range map[string]*browser{
		"no cookie":    {},
		"other cookie": {cookies: []*http.Cookie{{Name: browserCookieName, Value: "other-browser"}}},
	} {
		t.Run(name, func(t *testing.T) {
			back := other.start(handler, "code=identity-code&state="+url.QueryEscape(identityState))

			require.Equal(t, http.StatusBadRequest, back.Code)
			require.Empty(t, back.Header().Get("Location"))
			require.Empty(t, handler.client.stateStore.Update(encodedState, func(*OAuthState) {}).BrowserBinding)
		})
	}
}

func TestHandler_IdentityCheck_RefusesACodeOfAnotherSignIn(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	b := &browser{}
	identityState := sendToSignIn(t, handler, b, encodedState)

	back := b.start(handler, "code=&state="+url.QueryEscape(identityState))

	require.Equal(t, http.StatusBadRequest, back.Code)
	require.Empty(t, handler.client.stateStore.Update(encodedState, func(*OAuthState) {}).BrowserBinding)
}

func TestHandler_IdentityCheck_ExpiredFlow(t *testing.T) {
	handler, _ := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	unknown, err := nonceState("never-issued")
	require.NoError(t, err)

	back := (&browser{}).start(handler, "code=identity-code&state="+url.QueryEscape(unknown))

	require.Equal(t, http.StatusBadRequest, back.Code)
	require.Contains(t, back.Body.String(), summaryExpiredLink)
}

func newIdentityCallbackHandler(t *testing.T) (*Handler, *Client, string, string) {
	t.Helper()
	as := newDCRTestServer(t)
	as.advertiseCIMD = true
	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	t.Cleanup(client.Stop)
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	return NewHandler(client), client, as.server.URL, startCallbackFlow(t, client, as.server.URL)
}

// SECURITY: a callback for a flow whose browser never passed the check (the
// upstream authorization URL opened directly) exchanges no code.
func TestHandler_IdentityCheck_CallbackRefusesAnUncheckedFlow(t *testing.T) {
	handler, client, issuer, callbackPath := newIdentityCallbackHandler(t)

	rr := (&browser{}).get(handler.HandleCallback, callbackPath)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), summaryUnverifiedResponse)
	require.Nil(t, client.GetToken("session-1", issuer, "openid"), "no token was obtained")
}

// SECURITY: the owner passing the check and forwarding the upstream
// authorization URL must not let another browser complete the flow.
func TestHandler_IdentityCheck_CallbackRefusesAnotherBrowser(t *testing.T) {
	handler, client, issuer, callbackPath := newIdentityCallbackHandler(t)
	state := httptest.NewRequest("GET", callbackPath, nil).URL.Query().Get("state")
	require.Equal(t, http.StatusFound, passIdentityCheck(t, handler, &browser{}, state).Code)

	rr := (&browser{}).get(handler.HandleCallback, callbackPath)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), summaryUnverifiedResponse)
	require.Nil(t, client.GetToken("session-1", issuer, "openid"), "no token was obtained")
}

func TestHandler_IdentityCheck_CallbackCompletesACheckedFlow(t *testing.T) {
	handler, _, _, callbackPath := newIdentityCallbackHandler(t)
	state := httptest.NewRequest("GET", callbackPath, nil).URL.Query().Get("state")
	owner := &browser{}
	require.Equal(t, http.StatusFound, passIdentityCheck(t, handler, owner, state).Code)

	rr := owner.get(handler.HandleCallback, callbackPath)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

// The sign-in identity and the browser binding survive Valkey: a replica
// that handles the callback
// reads what another wrote.
func TestValkeyStateStore_KeepsTheBrowserBinding(t *testing.T) {
	srv := miniredis.RunT(t)
	vc, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(vc.Close)
	store := NewValkeyStateStore(vc, "muster:", nil)

	encodedState, err := store.GenerateState(StateParams{SessionID: "session-1", UserID: "user-1", SignIn: testSignIn, ServerName: "server-1"}, nil)
	require.NoError(t, err)
	require.NotNil(t, store.Update(encodedState, func(s *OAuthState) { s.BrowserBinding = "binding" }))

	consumed := store.ValidateState(encodedState)
	require.NotNil(t, consumed)
	require.Equal(t, "binding", consumed.BrowserBinding)
	require.Equal(t, testSignIn, consumed.SignIn)
}
