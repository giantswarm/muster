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

// fakeBrowserIdentity signs every browser in as subject. Like the real
// identity flow, it redeems a code only with the verifier of the challenge
// the sign-in was started with.
type fakeBrowserIdentity struct {
	subject   string
	challenge string
}

func (f *fakeBrowserIdentity) SignInURL(_ context.Context, state, codeChallenge string) (string, error) {
	f.challenge = codeChallenge
	return "https://dex.example.com/auth?state=" + url.QueryEscape(state), nil
}

func (f *fakeBrowserIdentity) Subject(_ context.Context, code, codeVerifier string) (string, error) {
	sum := sha256.Sum256([]byte(codeVerifier))
	if code == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
		return "", errors.New("invalid identity code")
	}
	return f.subject, nil
}

func registerBrowserIdentity(t *testing.T, identity api.BrowserIdentity) {
	t.Helper()
	api.RegisterBrowserIdentity(identity)
	t.Cleanup(func() { api.RegisterBrowserIdentity(nil) })
}

// passIdentityCheck runs the browser through the start endpoint and back from
// the identity sign-in, returning the answer to its return.
func passIdentityCheck(t *testing.T, handler *Handler, encodedState string) *httptest.ResponseRecorder {
	t.Helper()
	start := startRequest(handler, "state="+url.QueryEscape(encodedState))
	require.Equal(t, http.StatusFound, start.Code)
	signIn, err := url.Parse(start.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "dex.example.com", signIn.Host, "the browser signs in to muster's identity provider first")

	return startRequest(handler, "code=identity-code&state="+url.QueryEscape(signIn.Query().Get("state")))
}

func TestHandler_IdentityCheck_OwnerContinuesUpstream(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})

	back := passIdentityCheck(t, handler, encodedState)

	require.Equal(t, http.StatusFound, back.Code, back.Body.String())
	require.Equal(t, "https://idp.example.com/authorize?state=abc", back.Header().Get("Location"))
	state := handler.client.stateStore.Update(encodedState, func(*OAuthState) {})
	require.True(t, state.BrowserVerified)
	require.Empty(t, state.BrowserVerifier)

	again := startRequest(handler, "state="+url.QueryEscape(encodedState))
	require.Equal(t, "https://idp.example.com/authorize?state=abc", again.Header().Get("Location"),
		"a checked flow goes straight upstream")
}

// SECURITY: a sign-in link forwarded to, or phished from, another person must
// not connect that person's account to the owner's session.
func TestHandler_IdentityCheck_RefusesSomeoneElse(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	identity := &fakeBrowserIdentity{subject: "user-2"}
	registerBrowserIdentity(t, identity)

	back := passIdentityCheck(t, handler, encodedState)

	require.Equal(t, http.StatusBadRequest, back.Code)
	require.Contains(t, back.Body.String(), summaryOtherPerson)
	require.Empty(t, back.Header().Get("Location"))
	require.False(t, handler.client.stateStore.Update(encodedState, func(*OAuthState) {}).BrowserVerified)

	identity.subject = "user-1"
	owner := passIdentityCheck(t, handler, encodedState)
	require.Equal(t, http.StatusFound, owner.Code, "the link stays usable by its owner")
}

func TestHandler_IdentityCheck_RefusesACodeOfAnotherSignIn(t *testing.T) {
	handler, encodedState := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	start := startRequest(handler, "state="+url.QueryEscape(encodedState))
	require.Equal(t, http.StatusFound, start.Code)

	back := startRequest(handler, "code=&state="+url.QueryEscape(encodedState))

	require.Equal(t, http.StatusBadRequest, back.Code)
	require.False(t, handler.client.stateStore.Update(encodedState, func(*OAuthState) {}).BrowserVerified)
}

func TestHandler_IdentityCheck_ExpiredFlow(t *testing.T) {
	handler, _ := startTestHandler(t, "https://idp.example.com/authorize?state=abc")
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	unknown, err := nonceState("never-issued")
	require.NoError(t, err)

	back := startRequest(handler, "code=identity-code&state="+url.QueryEscape(unknown))

	require.Equal(t, http.StatusBadRequest, back.Code)
	require.Contains(t, back.Body.String(), summaryExpiredLink)
}

// SECURITY: a callback for a flow whose browser never passed the check (the
// upstream authorization URL opened directly) exchanges no code.
func TestHandler_IdentityCheck_CallbackRefusesAnUncheckedFlow(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseCIMD = true
	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	t.Cleanup(client.Stop)
	handler := NewHandler(client)
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	callbackPath := startCallbackFlow(t, client, as.server.URL)

	rr := httptest.NewRecorder()
	handler.HandleCallback(rr, httptest.NewRequest("GET", callbackPath, nil))

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), summaryUnverifiedResponse)
	require.Nil(t, client.GetToken("session-1", as.server.URL, "openid"), "no token was obtained")
}

func TestHandler_IdentityCheck_CallbackCompletesACheckedFlow(t *testing.T) {
	as := newDCRTestServer(t)
	as.advertiseCIMD = true
	client := NewClient("https://muster.example.com/.well-known/oauth-client.json",
		"https://muster.example.com", "/oauth/proxy/callback", "openid profile email")
	t.Cleanup(client.Stop)
	handler := NewHandler(client)
	registerBrowserIdentity(t, &fakeBrowserIdentity{subject: "user-1"})
	callbackPath := startCallbackFlow(t, client, as.server.URL)
	state := httptest.NewRequest("GET", callbackPath, nil).URL.Query().Get("state")
	require.Equal(t, http.StatusFound, passIdentityCheck(t, handler, state).Code)

	rr := httptest.NewRecorder()
	handler.HandleCallback(rr, httptest.NewRequest("GET", callbackPath, nil))

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

// The sign-in check's verifier and outcome survive Valkey: a replica that
// handles the browser's return, or the callback, reads what another wrote.
func TestValkeyStateStore_KeepsTheSignInCheck(t *testing.T) {
	srv := miniredis.RunT(t)
	vc, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(vc.Close)
	store := NewValkeyStateStore(vc, "muster:", nil)

	encodedState, err := store.GenerateState(StateParams{SessionID: "session-1", UserID: "user-1", ServerName: "server-1"}, nil)
	require.NoError(t, err)
	require.NotNil(t, store.Update(encodedState, func(s *OAuthState) { s.BrowserVerifier = "verifier" }))
	require.Equal(t, "verifier", store.Update(encodedState, func(*OAuthState) {}).BrowserVerifier)

	require.NotNil(t, store.Update(encodedState, func(s *OAuthState) {
		s.BrowserVerified = true
		s.BrowserVerifier = ""
	}))
	consumed := store.ValidateState(encodedState)
	require.NotNil(t, consumed)
	require.True(t, consumed.BrowserVerified)
}
