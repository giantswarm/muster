package aggregator

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// discoveredGrantHandler is the subject-grant mock with the pins of the real
// handler: an issuer is subject-scoped once a description pinned it so. It
// builds on subjectGrantMockHandler, not appGrantHandler, for the session
// tokens, the log of cleared grants and the lock the concurrent connects of
// an adoption need.
type discoveredGrantHandler struct {
	*subjectGrantMockHandler
	pins map[string]api.IssuerPin
}

var _ api.IssuerPinner = (*discoveredGrantHandler)(nil)

// The pins are keyed without a trailing slash, as the real client keys them.

func (h *discoveredGrantHandler) PinIssuer(issuer string, pin api.IssuerPin) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pins[strings.TrimSuffix(issuer, "/")] = pin
}

func (h *discoveredGrantHandler) UnpinIssuer(issuer string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.pins, strings.TrimSuffix(issuer, "/"))
}

func (h *discoveredGrantHandler) PinnedDescriptionKey(issuer string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pin, ok := h.pins[strings.TrimSuffix(issuer, "/")]
	return pin.DescriptionKey, ok
}

func (h *discoveredGrantHandler) IssuerSubjectScoped(issuer string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pins[strings.TrimSuffix(issuer, "/")].SubjectScoped
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
	backend := oneOrigin(t, mcpBackend, prm)
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

// oneOrigin serves the resource metadata of prm and everything else of mcp
// on one origin. The proxies keep the Host the client reached, so the
// resource the metadata declares is that origin's.
func oneOrigin(t *testing.T, mcp, prm *httptest.Server) *httptest.Server {
	t.Helper()
	target := func(server *httptest.Server) *httputil.ReverseProxy {
		u, err := url.Parse(server.URL)
		require.NoError(t, err)
		return httputil.NewSingleHostReverseProxy(u)
	}
	mcpProxy, prmProxy := target(mcp), target(prm)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/") {
			prmProxy.ServeHTTP(w, r)
			return
		}
		mcpProxy.ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		backend.CloseClientConnections()
		backend.Close()
	})
	return backend
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
