package aggregator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// newPRMBackend is a backend that answers every request but its RFC 9728
// resource metadata with a bare 401, and names issuer as its authorization
// server in that metadata ("" answers 404 there). It counts the metadata
// requests.
func newPRMBackend(t *testing.T, issuer string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		requests.Add(1)
		if issuer == "" {
			http.NotFound(w, r)
			return
		}
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              scheme + "://" + r.Host + "/mcp",
			"authorization_servers": []string{issuer},
			"scopes_supported":      []string{"read"},
		})
	}))
	t.Cleanup(backend.Close)
	return backend, &requests
}

// TestWithRememberedIssuer: a re-registration whose 401 names no issuer gets
// the one muster learned for the server at the same URL, without a request;
// an issuer the 401 names is remembered; another URL is another server.
func TestWithRememberedIssuer(t *testing.T) {
	a := bareAggregator()
	registration := func(url string, auth *api.MCPServerAuth, authInfo *AuthInfo) PendingAuthRegistration {
		return PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "connector"},
			URL:                url,
			AuthInfo:           authInfo,
			AuthConfig:         auth,
		}
	}
	const url = "https://connector.example.com/mcp"

	assert.Nil(t, a.withRememberedIssuer(registration(url, discoveredSubjectAuth(), nil)).AuthInfo, "nothing learned yet")

	named := &AuthInfo{Issuer: discoveredIssuer, Scope: "read"}
	assert.Same(t, named, a.withRememberedIssuer(registration(url, discoveredSubjectAuth(), named)).AuthInfo, "an issuer the 401 named is kept")

	got := a.withRememberedIssuer(registration(url, discoveredSubjectAuth(), &AuthInfo{ResourceMetadataURL: "kept"}))
	require.NotNil(t, got.AuthInfo)
	assert.Equal(t, discoveredIssuer, got.AuthInfo.Issuer)
	assert.Equal(t, "read", got.AuthInfo.Scope)
	assert.Equal(t, "kept", got.AuthInfo.ResourceMetadataURL)

	assert.Nil(t, a.withRememberedIssuer(registration("https://elsewhere.example.com/mcp", discoveredSubjectAuth(), nil)).AuthInfo, "another URL learns its own issuer")
	assert.Nil(t, a.withRememberedIssuer(registration(url, &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth}, nil)).AuthInfo, "a session-scoped server is registered as before")

	a.discovered.forget("connector")
	assert.Nil(t, a.withRememberedIssuer(registration(url, discoveredSubjectAuth(), nil)).AuthInfo, "a deleted definition is forgotten")
}

// TestLookupDiscoveredIssuer: the background lookup of a registered server
// whose 401 named no issuer records the issuer on the entry and pins it
// subject-scoped; a failed lookup is not repeated within the retry interval.
func TestLookupDiscoveredIssuer(t *testing.T) {
	handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
	api.RegisterOAuthHandler(handler)
	defer api.RegisterOAuthHandler(nil)

	t.Run("learns and pins the issuer", func(t *testing.T) {
		backend, requests := newPRMBackend(t, discoveredIssuer)
		a := bareAggregator()
		require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "connector"},
			URL:                backend.URL + "/mcp",
			AuthInfo:           &AuthInfo{},
			AuthConfig:         discoveredSubjectAuth(),
		}))

		a.lookupDiscoveredIssuer("connector")
		a.wg.Wait()

		info, _ := a.registry.GetServerInfo("connector")
		assert.Equal(t, discoveredIssuer, info.GetAuthInfo().Issuer)
		assert.True(t, handler.pins[discoveredIssuer].SubjectScoped)
		assert.Equal(t, discoveredIssuer, a.discovered.remembered("connector", backend.URL+"/mcp").Issuer)

		a.lookupDiscoveredIssuer("connector")
		a.wg.Wait()
		assert.Equal(t, int32(1), requests.Load(), "a known issuer is not looked up again")
		delete(handler.pins, discoveredIssuer)
	})

	t.Run("a failed lookup is remembered", func(t *testing.T) {
		backend, requests := newPRMBackend(t, "")
		a := bareAggregator()
		require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "broken"},
			URL:                backend.URL + "/mcp",
			AuthInfo:           &AuthInfo{},
			AuthConfig:         discoveredSubjectAuth(),
		}))

		a.lookupDiscoveredIssuer("broken")
		a.wg.Wait()
		first := requests.Load()
		require.NotZero(t, first)

		a.lookupDiscoveredIssuer("broken")
		a.wg.Wait()
		assert.Equal(t, first, requests.Load(), "not repeated within the retry interval")
		assert.Empty(t, handler.pins)
	})

	t.Run("a session-scoped or pinned server is not looked up", func(t *testing.T) {
		backend, requests := newPRMBackend(t, discoveredIssuer)
		a := bareAggregator()
		registerAt := func(name string, auth *api.MCPServerAuth) {
			require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
				ServerRegistration: ServerRegistration{Name: name},
				URL:                backend.URL + "/mcp",
				AuthInfo:           &AuthInfo{},
				AuthConfig:         auth,
			}))
			a.lookupDiscoveredIssuer(name)
		}
		registerAt("session", &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth})
		pinned := discoveredSubjectAuth()
		pinned.AuthorizationServer = &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth"}
		registerAt("pinned", pinned)
		a.wg.Wait()
		assert.Zero(t, requests.Load())
	})
}

// discoveredGrantHandler is the subject-grant mock with the pins of the real
// handler: an issuer is subject-scoped once a description pinned it so.
type discoveredGrantHandler struct {
	*subjectGrantMockHandler
	pins map[string]api.IssuerPin
}

var _ api.IssuerPinner = (*discoveredGrantHandler)(nil)

func (h *discoveredGrantHandler) PinIssuer(issuer string, pin api.IssuerPin) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pins[issuer] = pin
}

func (h *discoveredGrantHandler) UnpinIssuer(issuer string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.pins, issuer)
}

func (h *discoveredGrantHandler) PinnedDescriptionKey(issuer string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pin, ok := h.pins[issuer]
	return pin.DescriptionKey, ok
}

func (h *discoveredGrantHandler) IssuerSubjectScoped(issuer string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pins[issuer].SubjectScoped
}

// GetFullTokenByIssuerForUser returns the person's grant only for an issuer
// pinned subject-scoped, as the real handler does.
func (h *discoveredGrantHandler) GetFullTokenByIssuerForUser(sessionID, userID, issuer string) *api.OAuthToken {
	if !h.IssuerSubjectScoped(issuer) {
		return h.GetFullTokenByIssuer(sessionID, issuer)
	}
	return h.subjectGrantMockHandler.GetFullTokenByIssuerForUser(sessionID, userID, issuer)
}

// TestAdoptSubjectGrantResolvesTheDiscoveredIssuer: a session of a person
// who holds a grant for a server whose registry entry knows no issuer -- the
// 401 named none and nothing looked it up yet -- is not held up by the
// lookup: the adoption starts it in the background and reports no grant; once
// it ran, the next adoption pins the issuer subject-scoped and connects with
// the person's grant.
func TestAdoptSubjectGrantResolvesTheDiscoveredIssuer(t *testing.T) {
	mcpBackend, initializes := newGrantBackend(t, subjectGrantToken)
	prm, _ := newPRMBackend(t, discoveredIssuer)
	// The backend serves its metadata on its own origin, as the MCP spec
	// asks; the two fixtures are put behind one origin here.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := mcpBackend.URL
		if strings.HasPrefix(r.URL.Path, "/.well-known/") {
			target = prm.URL
		}
		proxy(t, w, r, target)
	}))
	t.Cleanup(func() {
		backend.CloseClientConnections()
		backend.Close()
	})

	handler := &discoveredGrantHandler{subjectGrantMockHandler: newSubjectGrantMockHandler(), pins: map[string]api.IssuerPin{}}
	// The grant alice's sign-in to this server filed: bound to the resource
	// its metadata declares.
	handler.grantFor("alice", discoveredIssuer, subjectGrantToken, backend.URL+"/mcp")
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	a := newTestAggregatorWithPool(t)
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "connector", ToolPrefix: "connector"},
		URL:                backend.URL + "/mcp",
		AuthInfo:           &AuthInfo{},
		AuthConfig:         discoveredSubjectAuth(),
	}))
	info, _ := a.registry.GetServerInfo("connector")
	require.True(t, holdsSubjectGrants(info))

	adopted, err := a.adoptSubjectGrant(sessionContext("alice-2", "alice"), info, "alice-2", "alice")
	require.NoError(t, err)
	assert.False(t, adopted, "the adoption does not wait for the lookup")
	a.wg.Wait()
	assert.Equal(t, discoveredIssuer, info.GetAuthInfo().Issuer, "the lookup recorded the issuer on the entry")

	adopted, err = a.adoptSubjectGrant(sessionContext("alice-2", "alice"), info, "alice-2", "alice")
	require.NoError(t, err)
	assert.True(t, adopted, "the session connects with alice's grant")
	assert.Equal(t, int32(1), initializes.Load())
	assert.True(t, handler.IssuerSubjectScoped(discoveredIssuer), "the discovered issuer is pinned subject-scoped")

	// Another person without a grant is left unauthenticated.
	adopted, err = a.adoptSubjectGrant(sessionContext("bob-1", "bob"), info, "bob-1", "bob")
	require.NoError(t, err)
	assert.False(t, adopted)
}

// proxy forwards one request to target, streaming the answer back.
func proxy(t *testing.T, w http.ResponseWriter, r *http.Request, target string) {
	t.Helper()
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target+r.URL.RequestURI(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	// The fixtures answer as the origin the client reached, so the resource
	// the metadata declares is the backend's own.
	req.Host = r.Host
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

// TestSessionAuthStatus_DiscoveredSubjectScope: core_auth_status reports a
// server that discovers its authorization server with spec.auth.grantScope:
// subject as subject-scoped, with the issuer it discovered, once muster
// applies it -- the issuer is known and pinned subject-scoped -- and as
// session-scoped while it does not: the issuer is unknown, or an operator's
// pin of it is session-scoped.
func TestSessionAuthStatus_DiscoveredSubjectScope(t *testing.T) {
	const operatorIssuer = "https://operator.example.com"
	handler := &discoveredGrantHandler{subjectGrantMockHandler: newSubjectGrantMockHandler(), pins: map[string]api.IssuerPin{
		discoveredIssuer: {SubjectScoped: true},
		operatorIssuer:   {ClientID: "operator-client"},
	}}
	api.RegisterOAuthHandler(handler)
	defer api.RegisterOAuthHandler(nil)

	agg := &AggregatorServer{registry: NewServerRegistry("x")}
	register := func(name, issuer string, auth *api.MCPServerAuth) {
		require.NoError(t, agg.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: name, ToolPrefix: name},
			URL:                "http://127.0.0.1:1/" + name,
			AuthInfo:           &AuthInfo{Issuer: issuer, Scope: "read", Resource: "http://127.0.0.1:1/" + name},
			AuthConfig:         auth,
		}))
	}
	register("connector", discoveredIssuer, discoveredSubjectAuth())
	register("unknown-issuer", "", discoveredSubjectAuth())
	register("under-an-operator-pin", operatorIssuer, discoveredSubjectAuth())
	register("session-connector", discoveredIssuer, &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, GrantScope: api.GrantScopeSession})

	servers := statusByName(agg.sessionAuthStatus(testSessionCtx()).Servers)

	assert.Equal(t, api.GrantScopeSubject, servers["connector"].GrantScope)
	assert.Equal(t, discoveredIssuer, servers["connector"].Issuer)
	assert.Equal(t, api.GrantScopeSession, servers["unknown-issuer"].GrantScope)
	assert.Equal(t, api.GrantScopeSession, servers["under-an-operator-pin"].GrantScope)
	assert.Equal(t, api.GrantScopeSession, servers["session-connector"].GrantScope)
}
