package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// A server that discovers its subject-scoped issuer takes only what is its
// own: an issuer that has an issuer key, the person's grant under a
// discovered description, and a grant bound to its own RFC 8707 resource.

// registerDiscoveredAt registers a pending-auth server at url whose 401 or
// metadata named issuer and declared url as its resource.
func registerDiscoveredAt(t *testing.T, a *AggregatorServer, name, url, issuer string, auth *api.MCPServerAuth) *ServerInfo {
	t.Helper()
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: name, ToolPrefix: name},
		URL:                url,
		AuthInfo:           &AuthInfo{Issuer: issuer, Scope: "read", Resource: url},
		AuthConfig:         auth,
	}))
	info, ok := a.registry.GetServerInfo(name)
	require.True(t, ok)
	return info
}

func newDiscoveredGrantHandler(t *testing.T) *discoveredGrantHandler {
	t.Helper()
	handler := &discoveredGrantHandler{subjectGrantMockHandler: newSubjectGrantMockHandler(), pins: map[string]api.IssuerPin{}}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
	return handler
}

// TestDiscoveredIssuerWithoutIssuerKey: a backend whose metadata names "/" or
// a value that reads as another namespace's issuer key describes nothing, so
// nothing is pinned under the empty key and releasing it never makes a server
// that pins nothing the owner (which crashed muster).
func TestDiscoveredIssuerWithoutIssuerKey(t *testing.T) {
	ctx := context.Background()
	for _, issuer := range []string{"/", "ns:org-a:" + discoveredIssuer} {
		t.Run(issuer, func(t *testing.T) {
			handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
			api.RegisterOAuthHandler(handler)
			defer api.RegisterOAuthHandler(nil)

			a := bareAggregator()
			connector := registerDiscovered(t, a.registry, "connector", issuer, discoveredSubjectAuth())
			// A server that pins nothing, as most do.
			registerDiscovered(t, a.registry, "plain", discoveredIssuer, &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth})

			assert.Nil(t, discoveredDescription(connector))
			as, key := pinnedDescription(connector)
			assert.Nil(t, as)
			assert.Empty(t, key)
			assert.False(t, effectiveSubjectScoped(connector))

			require.NoError(t, a.pinDiscoveredIssuer(ctx, connector))
			assert.Empty(t, handler.pins, "nothing is pinned under the empty key")

			// Deleting the server releases what it described.
			previous := serverDescription(connector)
			require.NoError(t, a.registry.Deregister("connector"))
			assert.NotPanics(t, func() { a.releaseAuthorizationServerDescription(ctx, "connector", previous) })
			assert.Empty(t, handler.pins)
			assert.Empty(t, handler.unpinned)
		})
	}

	t.Run("the empty key has no owner", func(t *testing.T) {
		handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
		api.RegisterOAuthHandler(handler)
		defer api.RegisterOAuthHandler(nil)

		a := bareAggregator()
		registerDiscovered(t, a.registry, "plain", discoveredIssuer, &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth})
		assert.Nil(t, issuerOwner(a.describedServers(), ""))
		assert.NotPanics(t, func() {
			a.releaseAuthorizationServerDescription(ctx, "gone", &api.MCPServerAuthAuthorizationServer{Issuer: "/", GrantScope: api.GrantScopeSubject})
		})
		assert.Empty(t, handler.pins)
	})
}

// TestAdoptSubjectGrant_NotUnderAnotherDescription: a discovered connector
// whose backend names an issuer that an operator's MCPServer or a provider
// App describes never receives the person's grant or session token for it,
// whatever scope that description gives the issuer -- not even one bound to
// the connector's own resource -- and a 401 from its backend clears nothing.
func TestAdoptSubjectGrant_NotUnderAnotherDescription(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		issuer string
		// describe places the other description under the issuer.
		describe func(t *testing.T, a *AggregatorServer)
		// session files the token under the session instead of the person.
		session bool
	}{
		"an operator's subject-scoped pin with endpoints": {
			issuer: discoveredIssuer,
			describe: func(t *testing.T, a *AggregatorServer) {
				auth := pinnedAuth(discoveredIssuer, discoveredIssuer+"/authorize", discoveredIssuer+"/token")
				auth.AuthorizationServer.GrantScope = api.GrantScopeSubject
				operator := registerDiscovered(t, a.registry, "github", "", auth)
				require.NoError(t, a.pinAuthorizationServer(ctx, operator))
			},
		},
		"an operator's pin that only says grantScope: subject": {
			issuer: discoveredIssuer,
			describe: func(t *testing.T, a *AggregatorServer) {
				operator := registerDiscovered(t, a.registry, "github", "", &api.MCPServerAuth{
					Type:                api.MCPServerAuthTypeOAuth,
					AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: discoveredIssuer, GrantScope: api.GrantScopeSubject},
				})
				require.NoError(t, a.pinAuthorizationServer(ctx, operator))
			},
		},
		"an operator's session-scoped pin": {
			issuer:  discoveredIssuer,
			session: true,
			describe: func(t *testing.T, a *AggregatorServer) {
				operator := registerDiscovered(t, a.registry, "hand-configured", "", pinnedAuth(discoveredIssuer, discoveredIssuer+"/authorize", discoveredIssuer+"/token"))
				require.NoError(t, a.pinAuthorizationServer(ctx, operator))
			},
		},
		"a provider App": {
			issuer: githubAppIssuer,
			describe: func(t *testing.T, a *AggregatorServer) {
				set, err := newProviderAppSet(testProviderApps(), "muster")
				require.NoError(t, err)
				setAppOwnedIssuers(set)
				t.Cleanup(func() { setAppOwnedIssuers(nil) })
				handler := api.GetOAuthHandler().(api.IssuerPinner)
				handler.PinIssuer(githubAppIssuer, api.IssuerPin{DescriptionKey: "provider-app", SubjectScoped: true})
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			handler := newDiscoveredGrantHandler(t)
			// The connector's backend accepts the token, so only muster
			// keeps it from being sent.
			backend, initializes := newGrantBackend(t, subjectGrantToken)
			url := backend.URL + "/mcp"

			a := newTestAggregatorWithPool(t)
			tc.describe(t, a)
			connector := registerDiscoveredAt(t, a, "connector", url, tc.issuer, discoveredSubjectAuth())
			connector.CreatedAt = time.Now().Add(-time.Hour)
			if tc.session {
				handler.StoreToken("alice-2", "alice", tc.issuer, &api.OAuthToken{AccessToken: subjectGrantToken, Issuer: tc.issuer, Resource: url})
			} else {
				handler.grantFor("alice", tc.issuer, subjectGrantToken, url)
			}

			require.True(t, holdsSubjectGrants(connector), "the spec makes it a candidate")
			assert.False(t, subjectGrantAdoptable(connector))
			adopted, err := a.adoptSubjectGrant(sessionContext("alice-2", "alice"), connector, "alice-2", "alice")
			require.NoError(t, err)
			assert.False(t, adopted)
			assert.Zero(t, initializes.Load(), "the connector's backend never sees the token")
			assert.Empty(t, handler.clearedGrants())
		})
	}
}

// TestAdoptSubjectGrant_BoundToTheServersResource: two connectors that
// discover the same authorization server share the issuer key, but a grant
// is bound to the resource its sign-in asked for. The other connector neither
// receives it nor, when its backend rejects it, clears it; the connector the
// grant was issued for adopts it.
func TestAdoptSubjectGrant_BoundToTheServersResource(t *testing.T) {
	handler := newDiscoveredGrantHandler(t)
	first, firstInitializes := newGrantBackend(t, subjectGrantToken)
	// The second backend rejects the first one's audience-bound token.
	second, secondInitializes := newGrantBackend(t, "another-audience")

	a := newTestAggregatorWithPool(t)
	s1 := registerDiscoveredAt(t, a, "connector-a", first.URL+"/mcp", discoveredIssuer, discoveredSubjectAuth())
	s2 := registerDiscoveredAt(t, a, "connector-b", second.URL+"/mcp", discoveredIssuer, discoveredSubjectAuth())
	handler.grantFor("alice", discoveredIssuer, subjectGrantToken, first.URL+"/mcp")

	adopted, err := a.adoptSubjectGrant(sessionContext("alice-2", "alice"), s2, "alice-2", "alice")
	require.NoError(t, err)
	assert.False(t, adopted, "the grant is bound to connector-a")
	assert.Zero(t, secondInitializes.Load())
	assert.Empty(t, handler.clearedGrants(), "connector-b's backend cannot revoke connector-a's grant")
	require.True(t, discoveredPinStands(s2), "the discovered description is pinned")

	adopted, err = a.adoptSubjectGrant(sessionContext("alice-2", "alice"), s1, "alice-2", "alice")
	require.NoError(t, err)
	assert.True(t, adopted)
	assert.Equal(t, int32(1), firstInitializes.Load())

	t.Run("a grant recorded without a resource is not taken", func(t *testing.T) {
		handler.grantFor("bob", discoveredIssuer, subjectGrantToken, "")
		adopted, err := a.adoptSubjectGrant(sessionContext("bob-2", "bob"), s1, "bob-2", "bob")
		require.NoError(t, err)
		assert.False(t, adopted)
	})

	t.Run("a grant bound to another origin is not taken", func(t *testing.T) {
		evil := registerDiscovered(t, a.registry, "connector-c", discoveredIssuer, discoveredSubjectAuth())
		assert.False(t, grantBoundToServer(evil, first.URL+"/mcp", &api.OAuthToken{AccessToken: subjectGrantToken, Resource: first.URL + "/mcp"}),
			"a backend that declares another server's resource does not get that server's grant")
	})
}

// challengeGrantHandler is the discovered-grant handler that answers a
// sign-in challenge.
type challengeGrantHandler struct {
	*discoveredGrantHandler
	challenges []api.AuthChallengeParams
}

func (h *challengeGrantHandler) CreateAuthChallenge(_ context.Context, params api.AuthChallengeParams) (*api.AuthChallenge, error) {
	h.challenges = append(h.challenges, params)
	return &api.AuthChallenge{Status: "auth_required", AuthURL: "https://muster.example.com/oauth/proxy/start?for=" + params.ServerName}, nil
}

// TestHandleAuthLogin_DiscoveredServerSignsInForItsOwnResource: core_auth_login
// of a connector whose issuer another connector's grant sits under does not
// send that grant to its backend or clear it on a 401: the person signs in to
// this connector.
func TestHandleAuthLogin_DiscoveredServerSignsInForItsOwnResource(t *testing.T) {
	handler := &challengeGrantHandler{discoveredGrantHandler: &discoveredGrantHandler{subjectGrantMockHandler: newSubjectGrantMockHandler(), pins: map[string]api.IssuerPin{}}}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	first, _ := newGrantBackend(t, subjectGrantToken)
	second, secondInitializes := newGrantBackend(t, "another-audience")
	a := newTestAggregatorWithPool(t)
	registerDiscoveredAt(t, a, "connector-a", first.URL+"/mcp", discoveredIssuer, discoveredSubjectAuth())
	registerDiscoveredAt(t, a, "connector-b", second.URL+"/mcp", discoveredIssuer, discoveredSubjectAuth())
	handler.grantFor("test-user", discoveredIssuer, subjectGrantToken, first.URL+"/mcp")

	result, err := NewAuthToolProvider(a).handleAuthLogin(testSessionCtx(), map[string]any{"server": "connector-b"})
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	require.Len(t, handler.challenges, 1, "the person is asked to sign in to connector-b")
	assert.Equal(t, second.URL+"/mcp", handler.challenges[0].Resource)
	assert.Zero(t, secondInitializes.Load(), "connector-a's grant never reaches connector-b's backend")
	assert.Empty(t, handler.clearedGrants())
}
