package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const discoveredIssuer = "https://as.connector.example.com"

// discoveredSubjectAuth is the auth config of a connector the developer
// portal registers: OAuth, no pinned issuer, the person's grants.
func discoveredSubjectAuth() *api.MCPServerAuth {
	return &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, GrantScope: api.GrantScopeSubject}
}

// registerDiscovered registers a pending-auth server with the given auth
// config whose 401 named the given issuer ("" for a bare 401).
func registerDiscovered(t *testing.T, reg *ServerRegistry, name, issuer string, auth *api.MCPServerAuth) *ServerInfo {
	t.Helper()
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: name},
		URL:                "https://" + name + ".example.com/mcp",
		AuthInfo:           &AuthInfo{Issuer: issuer},
		AuthConfig:         auth,
	}))
	info, ok := reg.GetServerInfo(name)
	require.True(t, ok)
	return info
}

// TestDiscoveredDescription: a server with spec.auth.grantScope: subject and
// no pinned issuer describes the issuer it discovered as a subject-scoped pin
// once that issuer is known, and nothing before; spec.auth.grantScope applies
// to a pinned issuer as well.
func TestDiscoveredDescription(t *testing.T) {
	reg := NewServerRegistry("x")

	known := registerDiscovered(t, reg, "connector", discoveredIssuer+"/", discoveredSubjectAuth())
	as, key := pinnedDescription(known)
	require.NotNil(t, as)
	assert.Equal(t, discoveredIssuer, key)
	assert.Equal(t, discoveredIssuer, as.Issuer)
	assert.True(t, as.SubjectScoped())
	assert.False(t, as.HasPinnedEndpoints())
	assert.Nil(t, as.ClientCredentialsSecretRef)
	assert.Equal(t, as, serverDescription(known))

	unknown := registerDiscovered(t, reg, "bare-401", "", discoveredSubjectAuth())
	as, key = pinnedDescription(unknown)
	assert.Nil(t, as, "the issuer is not known yet")
	assert.Empty(t, key)

	session := registerDiscovered(t, reg, "session", discoveredIssuer, &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth})
	as, _ = pinnedDescription(session)
	assert.Nil(t, as, "a session-scoped server that discovers its issuer pins nothing, as before")
	assert.Nil(t, serverDescription(session))

	pinned := discoveredSubjectAuth()
	pinned.AuthorizationServer = &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth"}
	pinnedServer := registerDiscovered(t, reg, "pinned", discoveredIssuer, pinned)
	as, key = pinnedDescription(pinnedServer)
	require.NotNil(t, as)
	assert.Equal(t, "https://github.com/login/oauth", key, "the pin, not the discovered issuer, is the key")
	assert.True(t, as.SubjectScoped(), "spec.auth.grantScope applies to the pinned issuer")
	assert.Empty(t, pinned.AuthorizationServer.GrantScope, "the spec is left as written")
}

// TestPinDiscoveredIssuer: the discovered issuer of a subject-scoped server
// is pinned with subject-scoped grants and nothing else, once. An operator's
// MCPServer that names the same issuer keeps it, whatever the order the two
// were created or registered in: the discovered server goes along with the
// operator's description and is never refused for it.
func TestPinDiscoveredIssuer(t *testing.T) {
	ctx := context.Background()
	handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
	api.RegisterOAuthHandler(handler)
	defer api.RegisterOAuthHandler(nil)
	reset := func() {
		for k := range handler.pins {
			delete(handler.pins, k)
		}
	}
	operatorAuth := func() *api.MCPServerAuth {
		return pinnedAuth(discoveredIssuer, discoveredIssuer+"/authorize", discoveredIssuer+"/token")
	}

	t.Run("pins the discovered issuer subject-scoped", func(t *testing.T) {
		defer reset()
		a := bareAggregator()
		info := registerDiscovered(t, a.registry, "connector", discoveredIssuer, discoveredSubjectAuth())

		require.NoError(t, a.pinDiscoveredIssuer(ctx, info))
		pin, ok := handler.pins[discoveredIssuer]
		require.True(t, ok)
		assert.True(t, pin.SubjectScoped)
		assert.Empty(t, pin.ClientID)
		assert.Empty(t, pin.AuthorizationEndpoint)
		assert.Empty(t, pin.TokenEndpoint)

		// The pin stands: a second call is a lookup, not a re-pin.
		handler.pins[discoveredIssuer] = api.IssuerPin{DescriptionKey: pin.DescriptionKey, SubjectScoped: true, ClientID: "marker"}
		require.NoError(t, a.pinDiscoveredIssuer(ctx, info))
		assert.Equal(t, "marker", handler.pins[discoveredIssuer].ClientID)
	})

	t.Run("nothing to pin before the issuer is known", func(t *testing.T) {
		defer reset()
		a := bareAggregator()
		info := registerDiscovered(t, a.registry, "bare-401", "", discoveredSubjectAuth())
		require.NoError(t, a.pinDiscoveredIssuer(ctx, info))
		assert.Empty(t, handler.pins)
	})

	t.Run("a pinned server is left to pinAuthorizationServer", func(t *testing.T) {
		defer reset()
		a := bareAggregator()
		info := registerDiscovered(t, a.registry, "github", "", &api.MCPServerAuth{
			Type:                api.MCPServerAuthTypeOAuth,
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth", GrantScope: api.GrantScopeSubject},
		})
		require.NoError(t, a.pinDiscoveredIssuer(ctx, info))
		assert.Empty(t, handler.pins)
	})

	for _, discoveredOlder := range []bool{true, false} {
		name := "an operator's pin wins over an older discovered server"
		if !discoveredOlder {
			name = "an operator's pin wins over a newer discovered server"
		}
		t.Run(name, func(t *testing.T) {
			defer reset()
			a := bareAggregator()
			// The discovered server registers first and pins its description.
			connector := registerDiscovered(t, a.registry, "connector", discoveredIssuer, discoveredSubjectAuth())
			require.NoError(t, a.pinDiscoveredIssuer(ctx, connector))
			require.True(t, handler.pins[discoveredIssuer].SubjectScoped)

			operator := registerDiscovered(t, a.registry, "hand-configured", "", operatorAuth())
			if discoveredOlder {
				connector.CreatedAt, operator.CreatedAt = time.Now().Add(-time.Hour), time.Now()
			} else {
				connector.CreatedAt, operator.CreatedAt = time.Now(), time.Now().Add(-time.Hour)
			}

			require.NoError(t, a.pinAuthorizationServer(ctx, operator))
			pin := handler.pins[discoveredIssuer]
			assert.Equal(t, discoveredIssuer+"/authorize", pin.AuthorizationEndpoint, "the operator's description is pinned")
			assert.False(t, pin.SubjectScoped)

			// The discovered server goes along: no conflict, the pin stays.
			require.NoError(t, a.pinDiscoveredIssuer(ctx, connector))
			require.NoError(t, a.connectionConflict(connector))
			require.NoError(t, a.AuthorizationServerConflict("connector"))
			require.NoError(t, a.AuthorizationServerConflict("hand-configured"))
			assert.Equal(t, discoveredIssuer+"/authorize", handler.pins[discoveredIssuer].AuthorizationEndpoint)
		})
	}

	t.Run("an operator naming the issuer without a pin withdraws the discovered one", func(t *testing.T) {
		defer reset()
		a := bareAggregator()
		connector := registerDiscovered(t, a.registry, "connector", discoveredIssuer, discoveredSubjectAuth())
		require.NoError(t, a.pinDiscoveredIssuer(ctx, connector))
		require.True(t, handler.pins[discoveredIssuer].SubjectScoped)

		plain := registerDiscovered(t, a.registry, "plain", "", &api.MCPServerAuth{
			Type:                api.MCPServerAuthTypeOAuth,
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: discoveredIssuer},
		})
		require.NoError(t, a.pinAuthorizationServer(ctx, plain))
		_, still := handler.pins[discoveredIssuer]
		assert.False(t, still, "the issuer an operator named is not subject-scoped by a backend's choice")

		require.NoError(t, a.pinDiscoveredIssuer(ctx, connector))
		_, still = handler.pins[discoveredIssuer]
		assert.False(t, still, "the discovered server does not pin it again")
	})

	t.Run("an operator's subject-only pin and a discovered server agree", func(t *testing.T) {
		defer reset()
		a := bareAggregator()
		operator := registerDiscovered(t, a.registry, "hand-configured", "", &api.MCPServerAuth{
			Type:                api.MCPServerAuthTypeOAuth,
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: discoveredIssuer, GrantScope: api.GrantScopeSubject},
		})
		connector := registerDiscovered(t, a.registry, "connector", discoveredIssuer, discoveredSubjectAuth())
		require.NoError(t, a.pinAuthorizationServer(ctx, operator))
		require.NoError(t, a.pinDiscoveredIssuer(ctx, connector))
		assert.True(t, handler.pins[discoveredIssuer].SubjectScoped)
	})

	t.Run("two servers discovering the issuer with subject scope share it", func(t *testing.T) {
		defer reset()
		a := bareAggregator()
		first := registerDiscovered(t, a.registry, "connector-a", discoveredIssuer, discoveredSubjectAuth())
		second := registerDiscovered(t, a.registry, "connector-b", discoveredIssuer, discoveredSubjectAuth())
		require.NoError(t, a.pinDiscoveredIssuer(ctx, first))
		require.NoError(t, a.pinDiscoveredIssuer(ctx, second))
		assert.True(t, handler.pins[discoveredIssuer].SubjectScoped)
	})

	t.Run("a deregistered server whose definition remains keeps the pin", func(t *testing.T) {
		defer reset()
		a := bareAggregator()
		info := registerDiscovered(t, a.registry, "connector", discoveredIssuer, discoveredSubjectAuth())
		a.discovered.remember("connector", info.URL, info.GetAuthInfo())
		a.definedServers = func() map[string]*ServerInfo {
			definition := &ServerInfo{Name: "connector", URL: info.URL, AuthConfig: discoveredSubjectAuth()}
			a.rememberedDefinition(definition)
			return map[string]*ServerInfo{"connector": definition}
		}
		require.NoError(t, a.pinDiscoveredIssuer(ctx, info))
		previous := serverDescription(info)
		require.NoError(t, a.registry.Deregister("connector"))

		a.releaseAuthorizationServerDescription(ctx, "connector", previous)
		assert.True(t, handler.pins[discoveredIssuer].SubjectScoped, "the definition still describes the issuer")
	})

	t.Run("a server whose definition went releases the discovered issuer", func(t *testing.T) {
		defer reset()
		a := bareAggregator()
		info := registerDiscovered(t, a.registry, "connector", discoveredIssuer, discoveredSubjectAuth())
		require.NoError(t, a.pinDiscoveredIssuer(ctx, info))
		previous := serverDescription(info)
		require.NoError(t, a.registry.Deregister("connector"))

		a.releaseAuthorizationServerDescription(ctx, "connector", previous)
		_, still := handler.pins[discoveredIssuer]
		assert.False(t, still, "nobody describes the issuer any more")
	})
}
