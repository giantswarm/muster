package aggregator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
)

// subjectGrantMockOAuthHandler is issuerMockOAuthHandler plus the
// api.SubjectGrantHandler extension: it knows which issuers are pinned
// subject-scoped and records every clear it is asked for.
type subjectGrantMockOAuthHandler struct {
	issuerMockOAuthHandler
	subjectScoped map[string]bool

	clearedForUser  []clearCall
	clearedForIssue []clearCall
}

type clearCall struct {
	sessionID, userID, issuer string
}

func (m *subjectGrantMockOAuthHandler) GetFullTokenByIssuerForUser(_, _, _ string) *api.OAuthToken {
	return nil
}

func (m *subjectGrantMockOAuthHandler) ClearTokenByIssuerForUser(sessionID, userID, issuer string) {
	m.clearedForUser = append(m.clearedForUser, clearCall{sessionID, userID, issuer})
}

func (m *subjectGrantMockOAuthHandler) ClearTokenByIssuer(sessionID, issuer string) {
	m.clearedForIssue = append(m.clearedForIssue, clearCall{sessionID: sessionID, issuer: issuer})
}

func (m *subjectGrantMockOAuthHandler) IssuerSubjectScoped(issuer string) bool {
	return m.subjectScoped[issuer]
}

var _ api.SubjectGrantHandler = (*subjectGrantMockOAuthHandler)(nil)

const (
	logoutTestIssuer  = "https://github.example.com/login/oauth"
	logoutTestSubject = "alice"
	logoutSessionA    = "session-a"
	logoutSessionB    = "session-b"
	logoutSessionBob  = "session-bob"
)

// logoutFixture is an aggregator with two OAuth servers on one issuer, a
// third on another issuer, an SSO server, and three live sessions: two of
// alice, one of bob. Every session is connected to every OAuth server.
type logoutFixture struct {
	agg      *AggregatorServer
	handler  *subjectGrantMockOAuthHandler
	provider *AuthToolProvider
	clients  map[string]*poolTestClient // "<session>/<server>"
}

func newLogoutFixture(t *testing.T, subjectScoped bool) *logoutFixture {
	t.Helper()
	return newLogoutFixtureWith(t, subjectScoped, false)
}

// newLogoutFixtureWith builds the fixture; with pinOnly the registry entries
// carry no AuthInfo (the state right after a restart, before any login) and
// name their issuer only through spec.auth.authorizationServer.
func newLogoutFixtureWith(t *testing.T, subjectScoped, pinOnly bool) *logoutFixture {
	t.Helper()

	handler := &subjectGrantMockOAuthHandler{
		issuerMockOAuthHandler: issuerMockOAuthHandler{enabled: true},
		subjectScoped:          map[string]bool{},
	}
	if subjectScoped {
		handler.subjectScoped[logoutTestIssuer] = true
	}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	reg := NewServerRegistry("x")
	for _, s := range []struct{ name, issuer string }{
		{"github", logoutTestIssuer},
		{"github-pro", logoutTestIssuer},
		{"other", "https://other.example.com"},
	} {
		registration := PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: s.name, ToolPrefix: s.name},
			URL:                "https://" + s.name + ".example.com/mcp",
			AuthInfo:           &AuthInfo{Issuer: s.issuer, Scope: "repo"},
		}
		if pinOnly {
			registration.AuthInfo = nil
			grantScope := ""
			if subjectScoped && s.issuer == logoutTestIssuer {
				grantScope = api.GrantScopeSubject
			}
			registration.AuthConfig = &api.MCPServerAuth{
				Type: "oauth",
				AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
					// The trailing slash is the operator's; the pin trims it.
					Issuer:     s.issuer + "/",
					GrantScope: grantScope,
				},
			}
		}
		require.NoError(t, reg.RegisterPendingAuth(registration))
	}
	// An SSO server on the same issuer is connected from muster's own token,
	// never from the person's grant; it must not be touched.
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "sso", ToolPrefix: "sso"},
		URL:                "https://sso.example.com/mcp",
		AuthInfo:           &AuthInfo{Issuer: logoutTestIssuer, Scope: "openid"},
		AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
	}))

	authStore := oauthstore.NewInMemorySessionAuthStore(time.Hour)
	t.Cleanup(authStore.Stop)
	capStore := oauthstore.NewInMemoryCapabilityStore(time.Hour)
	t.Cleanup(capStore.Stop)
	pool := NewSessionConnectionPool(time.Hour)
	t.Cleanup(pool.Stop)
	tracker := newSubjectSessionTracker()
	tracker.TrackOAuth(logoutTestSubject, logoutSessionA)
	tracker.TrackOAuth(logoutTestSubject, logoutSessionB)
	tracker.TrackOAuth("bob", logoutSessionBob)

	f := &logoutFixture{
		handler: handler,
		clients: map[string]*poolTestClient{},
	}
	ctx := context.Background()
	for _, sid := range []string{logoutSessionA, logoutSessionB, logoutSessionBob} {
		for _, server := range []string{"github", "github-pro", "other", "sso"} {
			require.NoError(t, authStore.MarkAuthenticated(ctx, sid, server))
			require.NoError(t, capStore.Set(ctx, sid, server, &oauthstore.Capabilities{}))
			c := &poolTestClient{}
			pool.Put(sid, server, c)
			f.clients[sid+"/"+server] = c
		}
	}

	f.agg = &AggregatorServer{
		registry:        reg,
		authStore:       authStore,
		capabilityStore: capStore,
		connPool:        pool,
		subjectSessions: tracker,
	}
	f.provider = NewAuthToolProvider(f.agg)
	return f
}

func (f *logoutFixture) logout(t *testing.T, sessionID, sub, server string) *api.CallToolResult {
	t.Helper()
	ctx := api.WithSubject(api.WithSessionID(context.Background(), sessionID), sub)
	result, err := f.provider.handleAuthLogout(ctx, map[string]any{"server": server})
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

// connected reports whether the session still has the auth mark, the
// capability entry and an open pooled client for the server.
func (f *logoutFixture) connected(t *testing.T, sessionID, server string) bool {
	t.Helper()
	ctx := context.Background()
	authed, err := f.agg.authStore.IsAuthenticated(ctx, sessionID, server)
	require.NoError(t, err)
	caps, err := f.agg.capabilityStore.Exists(ctx, sessionID, server)
	require.NoError(t, err)
	_, pooled := f.agg.connPool.Get(sessionID, server)
	closed := f.clients[sessionID+"/"+server].closeCount.Load() > 0
	if authed != caps || caps != pooled || pooled == closed {
		t.Fatalf("inconsistent state for %s/%s: authed=%v caps=%v pooled=%v closed=%v",
			sessionID, server, authed, caps, pooled, closed)
	}
	return authed
}

func resultText(result *api.CallToolResult) string {
	var parts []string
	for _, c := range result.Content {
		if s, ok := c.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

func TestHandleAuthLogout_SubjectScopedIssuerRevokesTheGrantForThePerson(t *testing.T) {
	f := newLogoutFixture(t, true)

	result := f.logout(t, logoutSessionA, logoutTestSubject, "github")
	assert.False(t, result.IsError, resultText(result))

	// The grant is cleared for the person, not just for the session.
	require.Equal(t, []clearCall{{logoutSessionA, logoutTestSubject, logoutTestIssuer}}, f.handler.clearedForUser)
	assert.Empty(t, f.handler.clearedForIssue)

	// Every server of the issuer is disconnected in the calling session...
	assert.False(t, f.connected(t, logoutSessionA, "github"))
	assert.False(t, f.connected(t, logoutSessionA, "github-pro"), "the sibling server on the same issuer keeps no connection from a revoked grant")
	// ...and in the person's other live session.
	assert.False(t, f.connected(t, logoutSessionB, "github"))
	assert.False(t, f.connected(t, logoutSessionB, "github-pro"))

	// Servers on other issuers, SSO servers, and other people are untouched.
	assert.True(t, f.connected(t, logoutSessionA, "other"))
	assert.True(t, f.connected(t, logoutSessionA, "sso"))
	assert.True(t, f.connected(t, logoutSessionB, "other"))
	assert.True(t, f.connected(t, logoutSessionBob, "github"))
	assert.True(t, f.connected(t, logoutSessionBob, "github-pro"))

	text := resultText(result)
	assert.Contains(t, text, "Successfully logged out from 'github'")
	assert.Contains(t, text, "revoked for all your sessions")
	assert.Contains(t, text, "disconnected as well: github-pro")
	assert.NotContains(t, text, "other")
}

func TestHandleAuthLogout_SessionScopedSharedIssuerKeepsTheSiblings(t *testing.T) {
	f := newLogoutFixture(t, false)

	result := f.logout(t, logoutSessionA, logoutTestSubject, "github")
	assert.False(t, result.IsError, resultText(result))

	// A token shared by several servers is not cleared for one server's logout.
	assert.Empty(t, f.handler.clearedForUser)
	assert.Empty(t, f.handler.clearedForIssue)

	assert.False(t, f.connected(t, logoutSessionA, "github"))
	assert.True(t, f.connected(t, logoutSessionA, "github-pro"), "a sibling on a session-scoped shared issuer stays connected")
	assert.True(t, f.connected(t, logoutSessionB, "github"), "another session is not affected")

	text := resultText(result)
	assert.NotContains(t, text, "revoked")
	assert.NotContains(t, text, "github-pro")
}

func TestHandleAuthLogout_ExclusiveSessionScopedIssuerClearsTheSessionToken(t *testing.T) {
	f := newLogoutFixture(t, false)

	result := f.logout(t, logoutSessionA, logoutTestSubject, "other")
	assert.False(t, result.IsError, resultText(result))

	// Nobody else uses the issuer: the session's token goes (with the
	// subject-aware clear, which is a plain session clear for this issuer).
	require.Equal(t, []clearCall{{logoutSessionA, logoutTestSubject, "https://other.example.com"}}, f.handler.clearedForUser)

	assert.False(t, f.connected(t, logoutSessionA, "other"))
	assert.True(t, f.connected(t, logoutSessionA, "github"))
	assert.True(t, f.connected(t, logoutSessionB, "other"))
	assert.NotContains(t, resultText(result), "revoked")
}

func TestHandleAuthLogout_SubjectScopedWithoutTrackerStillDisconnectsSiblings(t *testing.T) {
	f := newLogoutFixture(t, true)
	f.agg.subjectSessions = nil

	result := f.logout(t, logoutSessionA, logoutTestSubject, "github-pro")
	assert.False(t, result.IsError, resultText(result))

	require.Len(t, f.handler.clearedForUser, 1)
	assert.False(t, f.connected(t, logoutSessionA, "github"))
	assert.False(t, f.connected(t, logoutSessionA, "github-pro"))
	// Without the tracker the other session's state is not reached here; its
	// pooled client finds the token gone on its next request instead.
	assert.True(t, f.connected(t, logoutSessionB, "github"))
	assert.Contains(t, resultText(result), "disconnected as well: github")
}

func TestServersOfIssuer_IgnoresTrailingSlashAndSSOServers(t *testing.T) {
	f := newLogoutFixture(t, true)
	assert.Equal(t, []string{"github", "github-pro"}, f.agg.serversOfIssuer(logoutTestIssuer+"/"))
	assert.Equal(t, []string{"other"}, f.agg.serversOfIssuer("https://other.example.com"))
	assert.Empty(t, f.agg.serversOfIssuer("https://nobody.example.com"))
}

func TestSubjectSessionTracker_OAuthSessionIDsForSubject(t *testing.T) {
	tracker := newSubjectSessionTracker()
	assert.Empty(t, tracker.OAuthSessionIDsForSubject("alice"))

	tracker.TrackOAuth("alice", "s1")
	tracker.TrackOAuth("alice", "s2")
	tracker.TrackOAuth("bob", "s3")
	assert.ElementsMatch(t, []string{"s1", "s2"}, tracker.OAuthSessionIDsForSubject("alice"))
	assert.Equal(t, []string{"s3"}, tracker.OAuthSessionIDsForSubject("bob"))
	assert.Empty(t, tracker.OAuthSessionIDsForSubject(""))

	tracker.UntrackOAuth("s1")
	assert.Equal(t, []string{"s2"}, tracker.OAuthSessionIDsForSubject("alice"))
}

func TestIssuerSubjectScoped_Helper(t *testing.T) {
	assert.False(t, api.IssuerSubjectScoped(nil, logoutTestIssuer))
	assert.False(t, api.IssuerSubjectScoped(&issuerMockOAuthHandler{enabled: true}, logoutTestIssuer),
		"a handler without subject-scoped grants never reports one")

	h := &subjectGrantMockOAuthHandler{subjectScoped: map[string]bool{logoutTestIssuer: true}}
	assert.True(t, api.IssuerSubjectScoped(h, logoutTestIssuer))
	assert.False(t, api.IssuerSubjectScoped(h, "https://other.example.com"))
}

func TestHandleAuthLogout_IssuerKnownOnlyFromThePinAfterRestart(t *testing.T) {
	// Right after a restart the registry entries carry no AuthInfo -- the
	// probe's bare 401 said nothing and nobody logged in yet -- while the
	// tokens from before the restart are still in the store.
	f := newLogoutFixtureWith(t, true, true)

	result := f.logout(t, logoutSessionA, logoutTestSubject, "github")
	assert.False(t, result.IsError, resultText(result))

	require.Equal(t, []clearCall{{logoutSessionA, logoutTestSubject, logoutTestIssuer}}, f.handler.clearedForUser,
		"the pin names the issuer, trailing slash dropped")
	assert.False(t, f.connected(t, logoutSessionA, "github"))
	assert.False(t, f.connected(t, logoutSessionA, "github-pro"), "the sibling is found through its pin as well")
	assert.False(t, f.connected(t, logoutSessionB, "github-pro"))
	assert.True(t, f.connected(t, logoutSessionA, "other"))
	assert.Contains(t, resultText(result), "disconnected as well: github-pro")
}

func TestHandleAuthLogout_UnknownIssuerWithoutPinDropsOnlyTheSessionState(t *testing.T) {
	f := newLogoutFixture(t, false)
	// A server whose entry knows no issuer and has no pin; discovery fails
	// fast against a closed port.
	require.NoError(t, f.agg.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "unknown", ToolPrefix: "unknown"},
		URL:                "http://127.0.0.1:1/mcp",
	}))
	require.NoError(t, f.agg.authStore.MarkAuthenticated(context.Background(), logoutSessionA, "unknown"))

	result := f.logout(t, logoutSessionA, logoutTestSubject, "unknown")
	assert.False(t, result.IsError, resultText(result))
	assert.Empty(t, f.handler.clearedForUser)
	assert.Empty(t, f.handler.clearedForIssue)
	authed, err := f.agg.authStore.IsAuthenticated(context.Background(), logoutSessionA, "unknown")
	require.NoError(t, err)
	assert.False(t, authed, "the session's auth mark goes even when no token can be found")
	assert.True(t, f.connected(t, logoutSessionA, "github"))
}

func TestKnownServerIssuer(t *testing.T) {
	assert.Empty(t, knownServerIssuer(nil))
	assert.Empty(t, knownServerIssuer(&ServerInfo{}))
	assert.Equal(t, "https://a.example.com/", knownServerIssuer(&ServerInfo{AuthInfo: &AuthInfo{Issuer: "https://a.example.com/"}}),
		"what the probe recorded is passed on as recorded; stored tokens use that spelling")
	pinned := &ServerInfo{AuthConfig: &api.MCPServerAuth{AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: "https://b.example.com/"}}}
	assert.Equal(t, "https://b.example.com", knownServerIssuer(pinned), "the pin is trimmed like PinIssuer does")
	both := &ServerInfo{AuthInfo: &AuthInfo{Issuer: "https://a.example.com"}, AuthConfig: pinned.AuthConfig}
	assert.Equal(t, "https://a.example.com", knownServerIssuer(both), "AuthInfo wins over the pin")
}
