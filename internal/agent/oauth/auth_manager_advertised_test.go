package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// challengingServer is a server that answers every request outside the
// well-known path with a 401 carrying a WWW-Authenticate pointer, and serves
// its own RFC 9728 document at the well-known path. Both the pointer and the
// declared resource are settable after the server starts, because a test
// needs the server's own URL to build them.
type challengingServer struct {
	*httptest.Server
	pointer          string
	declaredResource string
}

func newChallengingServer(t *testing.T, authorizationServer string) *challengingServer {
	t.Helper()

	server := &challengingServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              server.declaredResource,
				"authorization_servers": []string{authorizationServer},
			})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+server.pointer+`"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	return server
}

// resourceOnAuthURL runs discovery and starts the flow, then returns the RFC
// 8707 value the agent put on the authorization request.
func resourceOnAuthURL(t *testing.T, serverURL string) string {
	t.Helper()

	manager, err := NewAuthManager(AuthManagerConfig{TokenStorageDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	if _, err := manager.CheckConnection(t.Context(), serverURL); err != nil {
		t.Fatalf("CheckConnection: %v", err)
	}
	authURL, err := manager.StartAuthFlow(t.Context())
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	return parsed.Query().Get("resource")
}

// TestProbeEndpoint_IgnoresForeignMetadataPointer pins that a resource_metadata
// pointer to another origin is neither followed nor trusted. RFC 9728 §3.2
// requires a protected resource to serve its own metadata, and that document
// states both the authorization server and the resource identifier, so
// following a foreign pointer would let a server obtain a token for a resource
// it does not own.
func TestProbeEndpoint_IgnoresForeignMetadataPointer(t *testing.T) {
	authServer := newMetadataServer(t, false)

	var foreignHits atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		foreignHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              "https://payments.example.com/api",
			"authorization_servers": []string{authServer.URL},
		})
	}))
	t.Cleanup(foreign.Close)

	musterServer := newChallengingServer(t, authServer.URL)
	musterServer.pointer = foreign.URL + "/.well-known/oauth-protected-resource"

	got := resourceOnAuthURL(t, musterServer.URL+"/mcp")

	if hits := foreignHits.Load(); hits != 0 {
		t.Errorf("expected the foreign metadata document never to be fetched, got %d requests", hits)
	}
	want, err := pkgoauth.DeriveResourceURI(musterServer.URL)
	if err != nil {
		t.Fatalf("DeriveResourceURI: %v", err)
	}
	if got != want {
		t.Errorf("expected resource %q on the authorization URL, got %q", want, got)
	}
}

// TestProbeEndpoint_DropsForeignDeclaredResource covers a same-origin pointer
// whose document declares a resource identifier belonging to another party.
// That document is bound to the server only by the pointer, so the declared
// value is dropped and the indicator is derived from the server URL instead.
func TestProbeEndpoint_DropsForeignDeclaredResource(t *testing.T) {
	authServer := newMetadataServer(t, false)

	musterServer := newChallengingServer(t, authServer.URL)
	musterServer.pointer = musterServer.URL + "/.well-known/oauth-protected-resource"
	musterServer.declaredResource = "https://payments.example.com/api"

	got := resourceOnAuthURL(t, musterServer.URL+"/mcp")

	want, err := pkgoauth.DeriveResourceURI(musterServer.URL)
	if err != nil {
		t.Fatalf("DeriveResourceURI: %v", err)
	}
	if got != want {
		t.Errorf("expected resource %q on the authorization URL, got %q", want, got)
	}
}

// TestProbeEndpoint_KeepsOwnDeclaredResource covers the same pointer with a
// document that declares an identifier on the server's own origin. An
// mcp-oauth backend serving at "<base>/mcp" declares "<base>", so the path
// legitimately differs from the endpoint. The value is sent as declared.
func TestProbeEndpoint_KeepsOwnDeclaredResource(t *testing.T) {
	authServer := newMetadataServer(t, false)

	musterServer := newChallengingServer(t, authServer.URL)
	musterServer.pointer = musterServer.URL + "/.well-known/oauth-protected-resource"
	musterServer.declaredResource = musterServer.URL

	got := resourceOnAuthURL(t, musterServer.URL+"/mcp")

	if got != musterServer.declaredResource {
		t.Errorf("expected resource %q on the authorization URL, got %q", musterServer.declaredResource, got)
	}
}
