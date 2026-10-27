package aggregator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/credprovider"
	"github.com/giantswarm/muster/v5/internal/oauth"
	"github.com/giantswarm/muster/v5/internal/server"
)

// pinnedLoginFixture is an aggregator on a real OAuth adapter, whose token
// store a restart keeps when it is Valkey.
type pinnedLoginFixture struct {
	t         *testing.T
	a         *AggregatorServer
	handler   *oauth.Adapter
	refreshed []string
	// onRefresh answers a login family's refresh at the identity provider.
	onRefresh func(familyID string) error
	restart   func()
}

func newPinnedLoginFixture(t *testing.T, backend string) *pinnedLoginFixture {
	t.Helper()
	f := &pinnedLoginFixture{t: t, onRefresh: func(string) error { return errors.New("refresh token rejected") }}
	var srv *miniredis.Miniredis
	if backend == "valkey" {
		srv = miniredis.RunT(t)
	}
	f.restart = func() {
		var opts []oauth.ManagerOption
		if srv != nil {
			client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
			require.NoError(t, err)
			t.Cleanup(client.Close)
			opts = append(opts, oauth.WithValkeyTokenStore(oauth.NewValkeyTokenStore(client, time.Hour, "muster:", nil)))
		}
		manager := oauth.NewManager(config.OAuthMCPClientConfig{
			Enabled: true, PublicURL: "https://muster.example.com", ClientID: "client-id", CallbackPath: "/oauth/proxy/callback",
		}, opts...)
		t.Cleanup(manager.Stop)
		f.handler = oauth.NewAdapter(manager)
		api.RegisterOAuthHandler(f.handler)
		f.a, _ = newIdentityTestAggregator(t)
		api.RegisterOAuthHandler(f.handler)
		f.a.oauthHTTPServer = &refreshingOAuthServer{onRefresh: func(familyID string) {
			f.refreshed = append(f.refreshed, familyID)
			_ = f.onRefresh(familyID)
		}}
	}
	f.restart()
	return f
}

// login files a login ID token of the session, as muster's sign-in, a
// refresh, or a forwarded bearer does.
func (f *pinnedLoginFixture) login(sessionID, userID, token string, exp time.Time) {
	api.StoreLoginIDToken(f.handler, sessionID, userID, identityTestIssuer, token, exp)
}

// actorToken serves one actor request pinned to loginSession and returns the
// ID token it carries.
func (f *pinnedLoginFixture) actorToken(loginSession string) string {
	ctx := f.a.withPersonIDToken(context.Background(), pinnedCaller(loginSession))
	token, _ := server.GetIDTokenFromContext(ctx)
	return token
}

// actorCopy is the token the actor session's background connections find.
func (f *pinnedLoginFixture) actorCopy() string {
	return getIDTokenForForwarding(context.Background(), pinnedCaller("").SessionID, identityTestIssuer, nil)
}

func pinnedCaller(loginSession string) *credprovider.ActorCaller {
	return &credprovider.ActorCaller{
		Subject: "alice", Session: "agents/s1", SessionID: "actor-0123", Actor: "spiffe://x/actor", LoginSession: loginSession,
		SignIn: api.SignInIdentity{Issuer: identityTestIssuer, Subject: "alice"},
	}
}

// idToken is a JWT-shaped ID token of alice issued to the audiences.
func idToken(t *testing.T, audiences ...string) string {
	t.Helper()
	return idTokenOf(t, identityTestIssuer, "alice", audiences...)
}

func idTokenOf(t *testing.T, issuer, subject string, audiences ...string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"iss": issuer, "sub": subject, "aud": audiences, "exp": time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func forEachStore(t *testing.T, test func(t *testing.T, backend string)) {
	for _, backend := range []string{"memory", "valkey"} {
		t.Run(backend, func(t *testing.T) { test(t, backend) })
	}
}

// giantswarm/muster#1715: a forwarded token that lives longer never takes the
// place of the login the binding pins, whatever the person's other sessions.
func TestWithPersonIDToken_ServesThePinnedLoginOnly(t *testing.T) {
	forEachStore(t, func(t *testing.T, backend string) {
		f := newPinnedLoginFixture(t, backend)
		pinned := idToken(t, "muster", "dex-k8s-authenticator")
		f.login("family-1", "alice", pinned, time.Now().Add(time.Hour))
		f.login("ext-agent-platform", "alice", idToken(t, "agent-platform"), time.Now().Add(5*time.Hour))
		f.login("family-2", "alice", idToken(t, "muster", "dex-k8s-authenticator"), time.Now().Add(3*time.Hour))

		require.Equal(t, pinned, f.actorToken("family-1"))
		require.Equal(t, pinned, f.actorCopy(), "the actor session's background connections find the same token")

		if backend == "valkey" {
			f.restart()
			require.Equal(t, pinned, f.actorToken("family-1"), "a restart between turns still resolves it")
		}
	})
}

// An actor request files the pinned token for the actor session as the
// person (the session's SSO bootstrap); a bind made with that token, a
// Session started from inside the sandbox, still pins the family.
func TestWithPersonIDToken_ActorCopyNeverTakesThePin(t *testing.T) {
	forEachStore(t, func(t *testing.T, backend string) {
		f := newPinnedLoginFixture(t, backend)
		pinned := idToken(t, "muster", "dex-k8s-authenticator")
		f.login("family-1", "alice", pinned, time.Now().Add(time.Hour))
		require.Equal(t, pinned, f.actorToken("family-1"))

		f.a.storeIDTokenForSSO(pinnedCaller("").SessionID, "alice", pinned)
		require.Equal(t, "family-1", api.LoginSessionForIDToken(f.handler, pinned))
	})
}

// A token of the same person that another client or another issuer's Dex
// forwarded never reaches the actor, even when the pinned login is gone.
func TestWithPersonIDToken_NeverServesAnotherSessionsToken(t *testing.T) {
	forEachStore(t, func(t *testing.T, backend string) {
		f := newPinnedLoginFixture(t, backend)
		f.login("ext-other-client", "alice", idToken(t, "dex-k8s-authenticator"), time.Now().Add(time.Hour))
		f.login("ext-other-dex", "alice@example.com", idToken(t, "dex-k8s-authenticator"), time.Now().Add(time.Hour))

		require.Empty(t, f.actorToken("family-1"), "the pinned login holds no token")
		require.Equal(t, []string{"family-1"}, f.refreshed, "only the pinned family is refreshed")
		require.Empty(t, f.actorCopy())
	})
}

// An expired pinned login is refreshed with its own family's refresh token,
// at most once per backoff, never another login of the person.
func TestWithPersonIDToken_RefreshesOnlyThePinnedFamily(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newPinnedLoginFixture(t, "memory")
		f.login("family-2", "alice", idToken(t, "dex-k8s-authenticator"), time.Now().Add(time.Hour))

		require.Empty(t, f.actorToken("family-1"))
		require.Empty(t, f.actorToken("family-1"))
		require.Equal(t, []string{"family-1"}, f.refreshed, "within the backoff the family is not refreshed again")

		time.Sleep(pinnedLoginRefreshBackoff)
		refreshedToken := idToken(t, "muster", "dex-k8s-authenticator")
		f.onRefresh = func(familyID string) error {
			f.login(familyID, "alice", refreshedToken, time.Now().Add(time.Hour))
			return nil
		}
		require.Equal(t, refreshedToken, f.actorToken("family-1"), "the refreshed login serves the actor")
		require.Equal(t, []string{"family-1", "family-1"}, f.refreshed)
	})
}

// A sign-out or a revocation of the pinned login fails the actor closed: no
// token, and its copy goes too.
func TestWithPersonIDToken_SignOutFailsClosed(t *testing.T) {
	forEachStore(t, func(t *testing.T, backend string) {
		f := newPinnedLoginFixture(t, backend)
		f.login("family-1", "alice", idToken(t, "dex-k8s-authenticator"), time.Now().Add(time.Hour))
		f.login("family-2", "alice", idToken(t, "dex-k8s-authenticator"), time.Now().Add(time.Hour))
		require.NotEmpty(t, f.actorToken("family-1"))

		f.handler.DeleteTokensBySession("family-1")
		require.Empty(t, f.actorToken("family-1"), "another login of the person never stands in")
		require.Empty(t, f.actorCopy(), "the actor session's copy is cleared")
	})
}

// A binding pinned to a session a client forwarded its own token in serves
// that session's latest token, is never refreshed by muster, and leaves the
// session's own token for its own servers.
func TestWithPersonIDToken_ForwardedSession(t *testing.T) {
	forEachStore(t, func(t *testing.T, backend string) {
		f := newPinnedLoginFixture(t, backend)
		portal := idToken(t, "backstage", "dex-k8s-authenticator")
		f.login("ext-portal", "alice@example.com", portal, time.Now().Add(time.Hour))
		f.login("family-1", "alice", idToken(t, "dex-k8s-authenticator"), time.Now().Add(5*time.Hour))

		require.Equal(t, portal, f.actorToken("ext-portal"))
		require.Equal(t, portal, getIDTokenForForwarding(context.Background(), "ext-portal", identityTestIssuer, nil),
			"the forwarded session keeps its own token")

		renewed := idToken(t, "backstage")
		f.login("ext-portal", "alice@example.com", renewed, time.Now().Add(time.Hour))
		require.Equal(t, renewed, f.actorToken("ext-portal"), "the latest token the client sent")

		f.handler.DeleteTokensBySession("ext-portal")
		require.Empty(t, f.actorToken("ext-portal"))
		require.Empty(t, f.refreshed, "muster holds no refresh token for a forwarded session")
	})
}

// A forwarded session's ID names no principal muster can trust (mcp-oauth
// derives it from a truncated hash): its token serves only when it was
// issued to the identity the bind request signed in as.
func TestWithPersonIDToken_ForwardedSessionServesOnlyTheBindersIdentity(t *testing.T) {
	forEachStore(t, func(t *testing.T, backend string) {
		f := newPinnedLoginFixture(t, backend)
		f.login("ext-portal", "bob", idTokenOf(t, identityTestIssuer, "bob", "backstage"), time.Now().Add(time.Hour))
		require.Empty(t, f.actorToken("ext-portal"), "another person's token")

		f.login("ext-portal", "alice", idTokenOf(t, "https://dex.other.example.com", "alice", "backstage"), time.Now().Add(time.Hour))
		require.Empty(t, f.actorToken("ext-portal"), "another issuer's token")

		caller := pinnedCaller("ext-portal")
		caller.SignIn = api.SignInIdentity{}
		f.login("ext-portal", "alice", idToken(t, "backstage"), time.Now().Add(time.Hour))
		token, _ := server.GetIDTokenFromContext(f.a.withPersonIDToken(context.Background(), caller))
		require.Empty(t, token, "a binding that recorded no sign-in identity")
	})
}

// A binding that recorded no login session serves no token, whatever logins
// the person holds: the Session is bound again to get one.
func TestWithPersonIDToken_UnpinnedBindingFailsClosed(t *testing.T) {
	forEachStore(t, func(t *testing.T, backend string) {
		f := newPinnedLoginFixture(t, backend)
		f.login("family-1", "alice", idToken(t, "dex-k8s-authenticator"), time.Now().Add(time.Hour))
		require.NotEmpty(t, f.actorToken("family-1"))

		require.Empty(t, f.actorToken(""))
		require.Empty(t, f.actorCopy(), "the copy an earlier binding left is cleared")
		require.Empty(t, f.refreshed)
	})
}

func TestWithPersonIDToken_OAuthOff(t *testing.T) {
	a, _ := newIdentityTestAggregator(t)
	api.RegisterOAuthHandler(nil)
	ctx := context.Background()
	require.Equal(t, ctx, a.withPersonIDToken(ctx, pinnedCaller("family-1")))
}
