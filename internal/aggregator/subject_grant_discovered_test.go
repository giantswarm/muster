package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
// is pinned with subject-scoped grants and nothing else, once; a server of
// the same namespace that pins the issuer differently and is older keeps it,
// and the discovered server is refused like any other conflicting description.
func TestPinDiscoveredIssuer(t *testing.T) {
	ctx := context.Background()
	handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
	api.RegisterOAuthHandler(handler)
	defer api.RegisterOAuthHandler(nil)

	t.Run("pins the discovered issuer subject-scoped", func(t *testing.T) {
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
		delete(handler.pins, discoveredIssuer)
	})

	t.Run("nothing to pin before the issuer is known", func(t *testing.T) {
		a := bareAggregator()
		info := registerDiscovered(t, a.registry, "bare-401", "", discoveredSubjectAuth())
		require.NoError(t, a.pinDiscoveredIssuer(ctx, info))
		assert.Empty(t, handler.pins)
	})

	t.Run("a pinned server is left to pinAuthorizationServer", func(t *testing.T) {
		a := bareAggregator()
		info := registerDiscovered(t, a.registry, "github", "", &api.MCPServerAuth{
			Type:                api.MCPServerAuthTypeOAuth,
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth", GrantScope: api.GrantScopeSubject},
		})
		require.NoError(t, a.pinDiscoveredIssuer(ctx, info))
		assert.Empty(t, handler.pins)
	})

	t.Run("an older server describing the issuer differently keeps it", func(t *testing.T) {
		a := bareAggregator()
		older := registerDiscovered(t, a.registry, "hand-configured", "", pinnedAuth(discoveredIssuer, "https://as.connector.example.com/authorize", "https://as.connector.example.com/token"))
		older.CreatedAt = time.Now().Add(-time.Hour)
		info := registerDiscovered(t, a.registry, "connector", discoveredIssuer, discoveredSubjectAuth())
		info.CreatedAt = time.Now()

		err := a.pinDiscoveredIssuer(ctx, info)
		require.Error(t, err)
		assert.True(t, errors.Is(err, api.ErrAuthorizationServerConflict), "got %v", err)
		assert.Empty(t, handler.pins, "the refused description pins nothing")
	})

	t.Run("two servers discovering the issuer with subject scope share it", func(t *testing.T) {
		a := bareAggregator()
		first := registerDiscovered(t, a.registry, "connector-a", discoveredIssuer, discoveredSubjectAuth())
		second := registerDiscovered(t, a.registry, "connector-b", discoveredIssuer, discoveredSubjectAuth())
		require.NoError(t, a.pinDiscoveredIssuer(ctx, first))
		require.NoError(t, a.pinDiscoveredIssuer(ctx, second))
		assert.True(t, handler.pins[discoveredIssuer].SubjectScoped)
		delete(handler.pins, discoveredIssuer)
	})

	t.Run("a server that goes releases the discovered issuer", func(t *testing.T) {
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
// server in that metadata. It counts the metadata requests.
func newPRMBackend(t *testing.T, issuer string) (*httptest.Server, *int) {
	t.Helper()
	requests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		requests++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              "https://" + r.Host + "/mcp",
			"authorization_servers": []string{issuer},
			"scopes_supported":      []string{"read"},
		})
	}))
	t.Cleanup(backend.Close)
	return backend, &requests
}

// TestWithDiscoveredIssuer: the registration of a server that discovers a
// subject-scoped issuer learns the issuer from the server's resource metadata
// when the 401 did not name it; no other registration makes the request.
func TestWithDiscoveredIssuer(t *testing.T) {
	ctx := context.Background()
	backend, requests := newPRMBackend(t, discoveredIssuer)
	registration := func(auth *api.MCPServerAuth, authInfo *AuthInfo) PendingAuthRegistration {
		return PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "connector"},
			URL:                backend.URL + "/mcp",
			AuthInfo:           authInfo,
			AuthConfig:         auth,
		}
	}

	got := withDiscoveredIssuer(ctx, registration(discoveredSubjectAuth(), &AuthInfo{ResourceMetadataURL: "kept"}))
	require.NotNil(t, got.AuthInfo)
	assert.Equal(t, discoveredIssuer, got.AuthInfo.Issuer)
	assert.Equal(t, "read", got.AuthInfo.Scope)
	assert.NotEmpty(t, got.AuthInfo.Resource)
	assert.Equal(t, "kept", got.AuthInfo.ResourceMetadataURL)
	assert.Equal(t, 1, *requests)

	got = withDiscoveredIssuer(ctx, registration(discoveredSubjectAuth(), nil))
	require.NotNil(t, got.AuthInfo, "a registration without AuthInfo gets one")
	assert.Equal(t, discoveredIssuer, got.AuthInfo.Issuer)
	assert.Equal(t, 2, *requests)

	*requests = 0
	named := &AuthInfo{Issuer: "https://named.example.com"}
	assert.Same(t, named, withDiscoveredIssuer(ctx, registration(discoveredSubjectAuth(), named)).AuthInfo, "an issuer the 401 named is kept")
	assert.Nil(t, withDiscoveredIssuer(ctx, registration(&api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth}, nil)).AuthInfo, "a session-scoped server is registered as before")
	pinned := discoveredSubjectAuth()
	pinned.AuthorizationServer = &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth"}
	assert.Nil(t, withDiscoveredIssuer(ctx, registration(pinned, nil)).AuthInfo, "a pinned server needs no discovery")
	assert.Zero(t, *requests)

	unreachable := registration(discoveredSubjectAuth(), nil)
	unreachable.URL = "http://127.0.0.1:1/mcp"
	assert.Nil(t, withDiscoveredIssuer(ctx, unreachable).AuthInfo, "a failed lookup leaves the registration as it was")
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
// 401 named none and its metadata could not be read when it registered --
// resolves the issuer from the server's RFC 9728 metadata, pins it
// subject-scoped and connects with the person's grant.
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
	handler.grant("alice", discoveredIssuer, subjectGrantToken)
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
	assert.True(t, adopted, "the session connects with alice's grant")
	assert.Equal(t, int32(1), initializes.Load())
	assert.True(t, handler.IssuerSubjectScoped(discoveredIssuer), "the discovered issuer is pinned subject-scoped")
	assert.Equal(t, discoveredIssuer, info.GetAuthInfo().Issuer, "the entry records the issuer for later callers")

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
// subject as subject-scoped, with the issuer it discovered, so a client can
// tell the person their sign-in reaches their other sessions.
func TestSessionAuthStatus_DiscoveredSubjectScope(t *testing.T) {
	agg := &AggregatorServer{registry: NewServerRegistry("x")}
	require.NoError(t, agg.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "connector", ToolPrefix: "connector"},
		URL:                "http://127.0.0.1:1/mcp",
		AuthInfo:           &AuthInfo{Issuer: discoveredIssuer, Scope: "read", Resource: "http://127.0.0.1:1/mcp"},
		AuthConfig:         discoveredSubjectAuth(),
	}))
	require.NoError(t, agg.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "session-connector", ToolPrefix: "session-connector"},
		URL:                "http://127.0.0.1:1/other",
		AuthInfo:           &AuthInfo{Issuer: discoveredIssuer, Scope: "read", Resource: "http://127.0.0.1:1/other"},
		AuthConfig:         &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, GrantScope: api.GrantScopeSession},
	}))

	servers := statusByName(agg.sessionAuthStatus(testSessionCtx()).Servers)

	assert.Equal(t, api.GrantScopeSubject, servers["connector"].GrantScope)
	assert.Equal(t, discoveredIssuer, servers["connector"].Issuer)
	assert.Equal(t, api.GrantScopeSession, servers["session-connector"].GrantScope)
}
