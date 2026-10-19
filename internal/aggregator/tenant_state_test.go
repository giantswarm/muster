package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/oauth"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
)

const tenantTestIssuer = "https://idp.example.com"

// tenantFixture is muster with its OAuth handler and stores, the MCPServer
// definitions it reads, and a way to start a new muster process on the same
// stores (restart), as a pod does on Valkey.
type tenantFixture struct {
	t            *testing.T
	handler      *oauth.Adapter
	owners       oauthstore.TenantOwnerStore
	authStore    oauthstore.SessionAuthStore
	capabilities oauthstore.CapabilityStore
	definitions  *stubMCPServerManager
	am           *AggregatorManager
}

func newTenantFixture(t *testing.T, backend string) *tenantFixture {
	t.Helper()
	f := &tenantFixture{t: t, definitions: &stubMCPServerManager{servers: map[string]api.MCPServerInfo{}}}
	var opts []oauth.ManagerOption
	switch backend {
	case "memory":
		f.owners = oauthstore.NewInMemoryTenantOwnerStore()
		f.authStore = oauthstore.NewInMemorySessionAuthStore(time.Hour)
		f.capabilities = oauthstore.NewInMemoryCapabilityStore(time.Hour)
	case "valkey":
		srv := miniredis.RunT(t)
		client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
		require.NoError(t, err)
		t.Cleanup(client.Close)
		f.owners = oauthstore.NewValkeyTenantOwnerStore(client, "muster:")
		f.authStore = oauthstore.NewValkeySessionAuthStore(client, time.Hour, "muster:")
		f.capabilities = oauthstore.NewValkeyCapabilityStore(client, time.Hour, "muster:")
		opts = append(opts,
			oauth.WithValkeyTokenStore(oauth.NewValkeyTokenStore(client, time.Hour, "muster:", nil)),
			oauth.WithValkeyStateStore(oauth.NewValkeyStateStore(client, "muster:", nil)),
			oauth.WithValkeyClientCredentialStore(oauth.NewValkeyClientCredentialStore(client, "muster:", nil)))
	}
	manager := oauth.NewManager(config.OAuthMCPClientConfig{
		Enabled: true, PublicURL: "https://muster.example.com", ClientID: "client-id", CallbackPath: "/oauth/proxy/callback",
	}, opts...)
	t.Cleanup(manager.Stop)
	f.handler = oauth.NewAdapter(manager)
	api.RegisterOAuthHandler(f.handler)
	api.RegisterMCPServerManager(f.definitions)
	t.Cleanup(func() {
		api.RegisterOAuthHandler(nil)
		api.RegisterMCPServerManager(nil)
	})
	f.restart()
	return f
}

// restart starts a new muster process: an empty registry on the same stores.
func (f *tenantFixture) restart() {
	pool := NewSessionConnectionPool(time.Hour)
	f.t.Cleanup(pool.Stop)
	f.am = &AggregatorManager{aggregatorServer: &AggregatorServer{
		registry:        NewServerRegistry("x"),
		authStore:       f.authStore,
		capabilityStore: f.capabilities,
		tenantOwners:    f.owners,
		connPool:        pool,
		ssoTracker:      newSSOTracker(),
	}}
}

// define makes an MCPServer object exist; key is its server key.
func (f *tenantFixture) define(key, uid string) {
	namespace, name := api.SplitMCPServerKey(key)
	f.definitions.servers[key] = api.MCPServerInfo{Name: name, Namespace: namespace, Key: key, UID: uid}
}

// remove deletes the MCPServer object, as muster sees it: the definition
// goes, then its service (the reconciler's RemoveService).
func (f *tenantFixture) remove(key string) {
	delete(f.definitions.servers, key)
	_ = f.am.aggregatorServer.DeregisterServer(key)
}

// register is the server's 401 reaching the aggregator, naming issuer.
func (f *tenantFixture) register(key, uid, issuer string) error {
	namespace, _ := api.SplitMCPServerKey(key)
	return f.am.RegisterServerPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: key, Namespace: namespace, UID: uid},
		URL:                "http://127.0.0.1:1/mcp",
		AuthInfo:           &AuthInfo{Issuer: issuer},
	})
}

// signIn leaves what a completed sign-in of user to the server leaves: the
// grant under the server's issuer key and the session's mark.
func (f *tenantFixture) signIn(user, key, issuer string) {
	issuerKey := api.ServerIssuerKey(key, issuer)
	f.handler.StoreToken(user+"-session", user, issuerKey, &api.OAuthToken{
		AccessToken: user + "-token", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour), Issuer: issuerKey,
	})
	require.NoError(f.t, f.authStore.MarkAuthenticated(context.Background(), user+"-session", key))
}

// signedIn reports whether the login of user to the server would find a
// grant (the lookup core_auth_login connects with, no challenge) or the
// session's mark.
func (f *tenantFixture) signedIn(user, key, issuer string) (grant, mark bool) {
	token := api.FullTokenByIssuerForUser(f.handler, user+"-session", user, api.ServerIssuerKey(key, issuer))
	marked, err := f.authStore.IsAuthenticated(context.Background(), user+"-session", key)
	require.NoError(f.t, err)
	return token != nil && token.AccessToken != "", marked
}

// A new namespace that takes a deleted one's name gets none of its sign-ins
// (giantswarm/agentlab#907): the grants, and the sessions' marks, of the old
// namespace's server are gone before the new server can be signed in to,
// whether muster saw the deletion or was down while it happened.
func TestTenantSignIns_DoNotOutliveTheirNamespace(t *testing.T) {
	const key = "org-b/fixture"
	for _, backend := range []string{"memory", "valkey"} {
		t.Run(backend+": muster sees the deletion", func(t *testing.T) {
			f := newTenantFixture(t, backend)
			f.define(key, "uid-1")
			require.NoError(t, f.register(key, "uid-1", tenantTestIssuer))
			f.signIn("bob", key, tenantTestIssuer)

			f.remove(key)
			grant, mark := f.signedIn("bob", key, tenantTestIssuer)
			assert.False(t, grant, "the grant goes with the namespace's last server")
			assert.False(t, mark)

			f.define(key, "uid-2")
			require.NoError(t, f.register(key, "uid-2", tenantTestIssuer))
			grant, _ = f.signedIn("bob", key, tenantTestIssuer)
			assert.False(t, grant, "the new namespace's server starts with nothing")
		})
	}

	t.Run("valkey: muster down while the namespace went and came back", func(t *testing.T) {
		f := newTenantFixture(t, "valkey")
		f.define(key, "uid-1")
		require.NoError(t, f.register(key, "uid-1", tenantTestIssuer))
		f.signIn("bob", key, tenantTestIssuer)

		// No deletion seen: the new object is there when muster starts.
		delete(f.definitions.servers, key)
		f.define(key, "uid-2")
		f.restart()
		require.NoError(t, f.register(key, "uid-2", tenantTestIssuer))

		grant, mark := f.signedIn("bob", key, tenantTestIssuer)
		assert.False(t, grant, "the grant of the old namespace's server is gone")
		assert.False(t, mark)
	})
}

// Sign-ins stay where no server went: a restart registers the same objects
// again, and in a namespace that keeps servers only the issuer the deleted
// server alone named is forgotten.
func TestTenantSignIns_StayWithTheirServers(t *testing.T) {
	const (
		github = "org-a/github"
		miro   = "org-a/miro"
		other  = "https://other.example.com"
	)
	for _, backend := range []string{"memory", "valkey"} {
		t.Run(backend+": restart", func(t *testing.T) {
			f := newTenantFixture(t, backend)
			f.define(github, "uid-g")
			require.NoError(t, f.register(github, "uid-g", tenantTestIssuer))
			f.signIn("alice", github, tenantTestIssuer)

			f.restart()
			require.NoError(t, f.register(github, "uid-g", tenantTestIssuer))
			grant, _ := f.signedIn("alice", github, tenantTestIssuer)
			assert.True(t, grant, "nobody signs in again after a restart")
		})

		t.Run(backend+": a server deleted next to others", func(t *testing.T) {
			f := newTenantFixture(t, backend)
			f.define(github, "uid-g")
			f.define(miro, "uid-m")
			require.NoError(t, f.register(github, "uid-g", tenantTestIssuer))
			require.NoError(t, f.register(miro, "uid-m", other))
			f.signIn("alice", github, tenantTestIssuer)
			f.signIn("alice", miro, other)

			f.remove(github)
			grant, _ := f.signedIn("alice", github, tenantTestIssuer)
			assert.False(t, grant, "the issuer only the deleted server named is forgotten")
			grant, mark := f.signedIn("alice", miro, other)
			assert.True(t, grant, "the server left keeps its sign-in")
			assert.True(t, mark)
		})
	}
}

// With mcpServersInAllNamespaces off every server is in muster's namespace
// (a bare key): deleting and creating one again keeps everyone's sign-in,
// as before.
func TestTenantSignIns_MustersNamespaceUnchanged(t *testing.T) {
	const key = "github"
	f := newTenantFixture(t, "valkey")
	f.define(key, "uid-1")
	require.NoError(t, f.register(key, "uid-1", tenantTestIssuer))
	f.signIn("alice", key, tenantTestIssuer)

	f.remove(key)
	f.define(key, "uid-2")
	require.NoError(t, f.register(key, "uid-2", tenantTestIssuer))

	grant, _ := f.signedIn("alice", key, tenantTestIssuer)
	assert.True(t, grant)
}
