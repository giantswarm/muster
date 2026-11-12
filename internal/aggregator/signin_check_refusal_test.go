package aggregator

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// stubBrowserIdentity refuses every sign-in check with refusal, or none when
// it is nil.
type stubBrowserIdentity struct {
	refusal error
}

func (s *stubBrowserIdentity) SignInURL(context.Context, *url.URL, string, string, api.SignInIdentity) (string, error) {
	return "", errors.New("not used")
}

func (s *stubBrowserIdentity) CheckRefusal(api.SignInIdentity) error { return s.refusal }

func (s *stubBrowserIdentity) Identity(context.Context, *url.URL, string, string) (api.SignInIdentity, error) {
	return api.SignInIdentity{}, errors.New("not used")
}

func registerStubBrowserIdentity(t *testing.T, identity *stubBrowserIdentity) {
	t.Helper()
	api.RegisterBrowserIdentity(identity)
	t.Cleanup(func() { api.RegisterBrowserIdentity(nil) })
}

func newSignInCheckTestServer(t *testing.T) (*AggregatorServer, *challengeCountingOAuthHandler) {
	t.Helper()
	a := newSuspensionTestServer(t)
	registerSuspendableServer(t, a.registry, "miro", false, api.StateAuthRequired)
	a.authMetrics = NewAuthMetrics()
	a.authRateLimiter = NewAuthRateLimiter(AuthRateLimiterConfig{MaxAttempts: 1})
	t.Cleanup(a.authRateLimiter.Stop)

	oauthHandler := &challengeCountingOAuthHandler{issuerMockOAuthHandler: issuerMockOAuthHandler{enabled: true}}
	api.RegisterOAuthHandler(oauthHandler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
	return a, oauthHandler
}

// A sign-in whose link no browser can pass is answered with a tool error
// naming the reason and no link, so no client elicits it. It charges no
// attempt and records nothing against the session: the next sign-in, once the
// check can pass, gets its link.
func TestHandleAuthLogin_RefusesASignInTheCheckCannotPass(t *testing.T) {
	a, oauthHandler := newSignInCheckTestServer(t)
	identity := &stubBrowserIdentity{refusal: &api.SignInCheckError{Reason: "your login method cannot be checked here"}}
	registerStubBrowserIdentity(t, identity)

	result, err := NewAuthToolProvider(a).handleAuthLogin(testSessionCtx(), map[string]any{"server": "miro"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, []any{"Cannot sign in to 'miro': your login method cannot be checked here."}, result.Content)
	require.Empty(t, challengeURL(result))
	require.Zero(t, oauthHandler.challenges)

	identity.refusal = nil
	result, err = NewAuthToolProvider(a).handleAuthLogin(testSessionCtx(), map[string]any{"server": "miro"})
	require.NoError(t, err)
	require.False(t, result.IsError, "the refusal charged no attempt: %v", result.Content)
	require.NotEmpty(t, challengeURL(result))
	require.Equal(t, 1, oauthHandler.challenges)
}

// A gated tool call answers the same refusal: auth_required with the reason,
// marked isError, and no authUrl for the URL elicitation to open.
func TestSignInAnswer_RefusesASignInTheCheckCannotPass(t *testing.T) {
	a, oauthHandler := newSignInCheckTestServer(t)
	registerStubBrowserIdentity(t, &stubBrowserIdentity{refusal: &api.SignInCheckError{Reason: "your login method cannot be checked here"}})
	ctx, recorder := withChallengeRecorder(testSessionCtx())

	result, connected, err := a.signInAnswer(ctx, "miro", "test-session", "this session is not authenticated to it")
	require.NoError(t, err)
	require.False(t, connected)
	require.True(t, result.IsError)
	require.Empty(t, signInLink(result))
	require.Nil(t, recorder.challenge.Load())
	require.Zero(t, oauthHandler.challenges)
}

// A caller that leaves before the login is answered is not a login failure.
func TestHandleAuthLogin_CancelledCallIsNoLoginFailure(t *testing.T) {
	a, _ := newSignInCheckTestServer(t)
	manager, _ := registerSuspendableServer(t, a.registry, "notion", false, api.StateAuthRequired)
	manager.err = context.Canceled
	ctx, cancel := context.WithCancel(testSessionCtx())
	cancel()

	result, err := NewAuthToolProvider(a).handleAuthLogin(ctx, map[string]any{"server": "notion"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Zero(t, a.authMetrics.GetSummary().TotalLoginFailures)

	manager.err = errors.New("apiserver unavailable")
	_, err = NewAuthToolProvider(a).handleAuthLogin(testSessionCtx(), map[string]any{"server": "notion"})
	require.NoError(t, err)
	require.EqualValues(t, 1, a.authMetrics.GetSummary().TotalLoginFailures)
}

// A provider App's sign-in goes through the same check, so it is refused the
// same way, while a grant the person holds still connects.
func TestGrantProviderApp_RefusesASignInTheCheckCannotPass(t *testing.T) {
	f := newAppFixture(t)
	registerStubBrowserIdentity(t, &stubBrowserIdentity{refusal: &api.SignInCheckError{Reason: "your login method cannot be checked here"}})
	p := NewAuthToolProvider(f.agg)
	ctx := api.WithSubject(api.WithSessionID(context.Background(), appSession), appSubject)

	result, err := p.handleAuthLogin(ctx, map[string]any{"app": "gitlab"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, []any{"Cannot sign in to 'gitlab': your login method cannot be checked here."}, result.Content)
	require.Empty(t, f.handler.challenges)

	f.handler.grant(gitlabAppIssuer, "glpat_alice")
	result, err = p.handleAuthLogin(ctx, map[string]any{"app": "gitlab"})
	require.NoError(t, err)
	require.True(t, loginConnected(result), "%v", result.Content)
}

// A plain auth://status read says the same: a grant server whose sign-in the
// check cannot pass advertises no auth_tool and carries the refusal in error,
// and core_auth_status with auth_urls issues it no link.
func TestSessionAuthStatus_ReportsASignInTheCheckCannotPass(t *testing.T) {
	a, oauthHandler := newSignInCheckTestServer(t)
	identity := &stubBrowserIdentity{refusal: &api.SignInCheckError{Reason: "your login method cannot be checked here"}}
	registerStubBrowserIdentity(t, identity)

	status := a.sessionAuthStatus(testSessionCtx())
	require.Len(t, status.Servers, 1)
	require.Equal(t, pkgoauth.SessionServerStatusAuthRequired, status.Servers[0].Status)
	require.Empty(t, status.Servers[0].AuthTool)
	require.Equal(t, "Cannot sign in to 'miro': your login method cannot be checked here.", status.Servers[0].Error)

	result, err := NewAuthToolProvider(a).handleAuthStatus(testSessionCtx(), map[string]any{authURLsArg: true})
	require.NoError(t, err)
	listed, ok := result.StructuredContent.(pkgoauth.AuthStatusResponse)
	require.True(t, ok)
	require.Empty(t, listed.Servers[0].AuthURL)
	require.Zero(t, oauthHandler.challenges)

	identity.refusal = nil
	status = a.sessionAuthStatus(testSessionCtx())
	require.Equal(t, "core_auth_login", status.Servers[0].AuthTool)
	require.Empty(t, status.Servers[0].Error)
}

// A provider App's status read reports the refusal the same way.
func TestProviderAppStatuses_ReportASignInTheCheckCannotPass(t *testing.T) {
	f := newAppFixture(t)
	registerStubBrowserIdentity(t, &stubBrowserIdentity{refusal: &api.SignInCheckError{Reason: "your login method cannot be checked here"}})
	ctx := api.WithSubject(api.WithSessionID(context.Background(), appSession), appSubject)

	apps := NewAuthToolProvider(f.agg).providerAppStatuses(ctx, appSession, appSubject, true)
	require.Len(t, apps, 2)
	for _, app := range apps {
		require.Equal(t, pkgoauth.SessionServerStatusAuthRequired, app.Status, app.Name)
		require.Empty(t, app.AuthTool, app.Name)
		require.Empty(t, app.AuthURL, app.Name)
		require.Equal(t, "Cannot sign in to '"+app.Name+"': your login method cannot be checked here.", app.Error)
	}
	require.Empty(t, f.handler.challenges)
}

// list_tools and filter_tools do not advertise core_auth_login for a sign-in
// the check cannot pass: the entry carries the refusal instead.
func TestListServersRequiringAuth_ReportsASignInTheCheckCannotPass(t *testing.T) {
	a, _ := newSignInCheckTestServer(t)
	identity := &stubBrowserIdentity{refusal: &api.SignInCheckError{Reason: "your login method cannot be checked here"}}
	registerStubBrowserIdentity(t, identity)

	servers := a.ListServersRequiringAuth(testSessionCtx())
	require.Len(t, servers, 1)
	require.Empty(t, servers[0].AuthTool)
	require.Equal(t, "Cannot sign in to 'miro': your login method cannot be checked here.", servers[0].Error)

	identity.refusal = nil
	servers = a.ListServersRequiringAuth(testSessionCtx())
	require.Equal(t, "core_auth_login", servers[0].AuthTool)
	require.Empty(t, servers[0].Error)
}
