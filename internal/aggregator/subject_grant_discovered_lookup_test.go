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

// newPRMBackend is a backend that answers every request but its RFC 9728
// resource metadata with a bare 401, and names issuer as its authorization
// server in that metadata ("" answers 404 there). It counts the metadata
// requests.
func newPRMBackend(t *testing.T, issuer string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	backend, requests, _ := newMovablePRMBackend(t, issuer)
	return backend, requests
}

// newMovablePRMBackend is newPRMBackend whose metadata can name another
// authorization server later (move).
func newMovablePRMBackend(t *testing.T, issuer string) (*httptest.Server, *atomic.Int32, func(string)) {
	t.Helper()
	var requests atomic.Int32
	var named atomic.Value
	named.Store(issuer)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		requests.Add(1)
		issuer, _ := named.Load().(string)
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
	return backend, &requests, func(issuer string) { named.Store(issuer) }
}

// registerBare registers a pending-auth server at url from a bare 401.
func registerBare(t *testing.T, a *AggregatorServer, name, url string, auth *api.MCPServerAuth) {
	t.Helper()
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: name},
		URL:                url,
		AuthInfo:           &AuthInfo{},
		AuthConfig:         auth,
	}))
}

// newLookupPins registers a fresh pin-recording OAuth handler for one test.
func newLookupPins(t *testing.T) *pinCaptureHandler {
	t.Helper()
	handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
	return handler
}

// TestWithRememberedIssuer: a re-registration whose 401 names no issuer gets
// the one muster learned for the server at the same URL, without a request,
// and says so (the caller checks it again in the background); an issuer the
// 401 names is remembered without a trailing slash; another URL is another
// server.
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
	remembered := func(url string, auth *api.MCPServerAuth, authInfo *AuthInfo) (*AuthInfo, bool) {
		got, fromMemory := a.withRememberedIssuer(registration(url, auth, authInfo))
		return got.AuthInfo, fromMemory
	}
	const url = "https://connector.example.com/mcp"

	got, fromMemory := remembered(url, discoveredSubjectAuth(), nil)
	assert.Nil(t, got, "nothing learned yet")
	assert.False(t, fromMemory)

	got, fromMemory = remembered(url, discoveredSubjectAuth(), &AuthInfo{Issuer: discoveredIssuer + "/", Scope: "read"})
	require.NotNil(t, got)
	assert.Equal(t, discoveredIssuer, got.Issuer, "an issuer the 401 named is kept, without its trailing slash")
	assert.False(t, fromMemory)

	got, fromMemory = remembered(url, discoveredSubjectAuth(), &AuthInfo{ResourceMetadataURL: "kept"})
	require.NotNil(t, got)
	assert.Equal(t, discoveredIssuer, got.Issuer)
	assert.Equal(t, "read", got.Scope)
	assert.Equal(t, "kept", got.ResourceMetadataURL)
	assert.True(t, fromMemory)

	got, _ = remembered("https://elsewhere.example.com/mcp", discoveredSubjectAuth(), nil)
	assert.Nil(t, got, "another URL learns its own issuer")
	got, _ = remembered(url, &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth}, nil)
	assert.Nil(t, got, "a session-scoped server is registered as before")

	a.discovered.forget("connector")
	got, _ = remembered(url, discoveredSubjectAuth(), nil)
	assert.Nil(t, got, "a deleted definition is forgotten")
}

// TestLookupDiscoveredIssuer: the background lookup of a registered server
// whose 401 named no issuer records the issuer on the entry and pins it
// subject-scoped; a failed lookup is not repeated within the retry interval.
func TestLookupDiscoveredIssuer(t *testing.T) {
	t.Run("learns and pins the issuer", func(t *testing.T) {
		handler := newLookupPins(t)
		backend, requests := newPRMBackend(t, discoveredIssuer)
		a := bareAggregator()
		registerBare(t, a, "connector", backend.URL+"/mcp", discoveredSubjectAuth())

		a.lookupDiscoveredIssuer("connector", false)
		a.wg.Wait()

		info, _ := a.registry.GetServerInfo("connector")
		assert.Equal(t, discoveredIssuer, info.GetAuthInfo().Issuer)
		assert.True(t, handler.pins[discoveredIssuer].SubjectScoped)
		assert.Equal(t, discoveredIssuer, a.discovered.remembered("connector", backend.URL+"/mcp").Issuer)

		a.lookupDiscoveredIssuer("connector", false)
		a.wg.Wait()
		assert.Equal(t, int32(1), requests.Load(), "a known issuer is not looked up again")
	})

	t.Run("a trailing slash is recorded as the pin's key", func(t *testing.T) {
		handler := newLookupPins(t)
		backend, _ := newPRMBackend(t, discoveredIssuer+"/")
		a := bareAggregator()
		registerBare(t, a, "connector", backend.URL+"/mcp", discoveredSubjectAuth())

		a.lookupDiscoveredIssuer("connector", false)
		a.wg.Wait()

		info, _ := a.registry.GetServerInfo("connector")
		assert.Equal(t, discoveredIssuer, info.GetAuthInfo().Issuer, "the grant is filed under the key the pin has")
		issuer, _ := sessionOAuthIssuer(info)
		assert.Equal(t, discoveredIssuer, issuer)
		assert.True(t, handler.pins[discoveredIssuer].SubjectScoped)
	})

	t.Run("a failed lookup is remembered", func(t *testing.T) {
		handler := newLookupPins(t)
		backend, requests := newPRMBackend(t, "")
		a := bareAggregator()
		registerBare(t, a, "broken", backend.URL+"/mcp", discoveredSubjectAuth())

		a.lookupDiscoveredIssuer("broken", false)
		a.wg.Wait()
		first := requests.Load()
		require.NotZero(t, first)

		a.lookupDiscoveredIssuer("broken", false)
		a.wg.Wait()
		assert.Equal(t, first, requests.Load(), "not repeated within the retry interval")
		assert.Empty(t, handler.pins)
	})

	t.Run("a session-scoped or pinned server is not looked up", func(t *testing.T) {
		newLookupPins(t)
		backend, requests := newPRMBackend(t, discoveredIssuer)
		a := bareAggregator()
		registerBare(t, a, "session", backend.URL+"/mcp", &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth})
		a.lookupDiscoveredIssuer("session", false)
		pinned := discoveredSubjectAuth()
		pinned.AuthorizationServer = &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth"}
		registerBare(t, a, "pinned", backend.URL+"/mcp", pinned)
		a.lookupDiscoveredIssuer("pinned", true)
		a.wg.Wait()
		assert.Zero(t, requests.Load())
	})

	t.Run("a remembered issuer the backend no longer names is replaced", func(t *testing.T) {
		const movedIssuer = "https://as-new.connector.example.com"
		handler := newLookupPins(t)
		backend, _, move := newMovablePRMBackend(t, discoveredIssuer)
		a := bareAggregator()
		url := backend.URL + "/mcp"
		registerBare(t, a, "connector", url, discoveredSubjectAuth())
		a.lookupDiscoveredIssuer("connector", false)
		a.wg.Wait()
		require.True(t, handler.pins[discoveredIssuer].SubjectScoped)

		// The backend moves; the next registration gets the old issuer from
		// memory and checks it in the background.
		move(movedIssuer)
		registration, fromMemory := a.withRememberedIssuer(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "connector"},
			URL:                url,
			AuthInfo:           &AuthInfo{},
			AuthConfig:         discoveredSubjectAuth(),
		})
		require.True(t, fromMemory)
		require.NoError(t, a.registry.RegisterPendingAuth(registration))
		a.lookupDiscoveredIssuer("connector", fromMemory)
		a.wg.Wait()

		info, _ := a.registry.GetServerInfo("connector")
		assert.Equal(t, movedIssuer, info.GetAuthInfo().Issuer)
		assert.Equal(t, movedIssuer, a.discovered.remembered("connector", url).Issuer)
		assert.True(t, handler.pins[movedIssuer].SubjectScoped)
		_, still := handler.pins[discoveredIssuer]
		assert.False(t, still, "the issuer the backend left is released")
	})

	t.Run("a lookup that outlives its server changes nothing", func(t *testing.T) {
		handler := newLookupPins(t)
		backend, _ := newPRMBackend(t, discoveredIssuer)
		a := bareAggregator()
		url := backend.URL + "/mcp"
		registerBare(t, a, "connector", url, discoveredSubjectAuth())

		lookup, ok := a.discovered.beginLookup("connector", url, time.Now())
		require.True(t, ok)
		require.NoError(t, a.registry.Deregister("connector"))
		a.discovered.forget("connector")
		a.runDiscoveredIssuerLookup(context.Background(), "connector", url, lookup)

		assert.Nil(t, a.discovered.remembered("connector", url), "a forgotten server is not re-created")
		assert.Empty(t, handler.pins, "nothing is pinned for a deleted server")
	})

	t.Run("a lookup that ends after a URL change keeps the new URL's entry", func(t *testing.T) {
		a := bareAggregator()
		old, ok := a.discovered.beginLookup("connector", "https://old.example.com/mcp", time.Now())
		require.True(t, ok)
		a.discovered.remember("connector", "https://new.example.com/mcp", &AuthInfo{Issuer: discoveredIssuer})
		newer, ok := a.discovered.beginLookup("connector", "https://new.example.com/mcp", time.Now())
		require.True(t, ok)

		assert.False(t, a.discovered.endLookup("connector", "https://old.example.com/mcp", old, nil, time.Now()))
		assert.Equal(t, discoveredIssuer, a.discovered.remembered("connector", "https://new.example.com/mcp").Issuer)
		_, again := a.discovered.beginLookup("connector", "https://new.example.com/mcp", time.Now())
		assert.False(t, again, "the new URL's lookup is still running")
		assert.True(t, a.discovered.endLookup("connector", "https://new.example.com/mcp", newer, nil, time.Now()))
	})

	t.Run("no lookup starts once the aggregator stops", func(t *testing.T) {
		newLookupPins(t)
		backend, requests := newPRMBackend(t, discoveredIssuer)
		a := bareAggregator()
		registerBare(t, a, "connector", backend.URL+"/mcp", discoveredSubjectAuth())
		a.stopBackground()

		a.lookupDiscoveredIssuer("connector", false)
		a.wg.Wait()
		assert.Zero(t, requests.Load())
		_, ok := a.discovered.beginLookup("connector", backend.URL+"/mcp", time.Now())
		assert.True(t, ok, "the refused lookup is not left marked as running")
	})
}
