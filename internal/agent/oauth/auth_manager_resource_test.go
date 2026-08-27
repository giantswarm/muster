package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// newProtectedMusterServer starts a muster-like server that publishes RFC 9728
// protected resource metadata. declaredResource is the canonical URI the
// server states for itself, which is the value it validates incoming tokens
// against.
func newProtectedMusterServer(t *testing.T, authorizationServer, declaredResource string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              declaredResource,
				"authorization_servers": []string{authorizationServer},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestAuthManager_SendsDeclaredResource pins the RFC 8707 value the agent
// sends: the URI the muster server declares for itself, not one derived from
// the endpoint the user typed. muster validates access tokens against its own
// resource identifier, so a derived value produces a token it then refuses.
func TestAuthManager_SendsDeclaredResource(t *testing.T) {
	authServer := newMetadataServer(t, false)
	// Deliberately not canonical: a trailing slash and an explicit default
	// port. muster must send it as declared, because the authorization server
	// compares it against the identifier registered for that server, and
	// mcp-go sends the same declared value on token refresh.
	const declaredResource = "https://muster.example.com:443/mcp/"
	musterServer := newProtectedMusterServer(t, authServer.URL, declaredResource)

	manager, err := NewAuthManager(AuthManagerConfig{TokenStorageDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	defer func() { _ = manager.Close() }()

	state, err := manager.CheckConnection(t.Context(), musterServer.URL+"/mcp")
	if err != nil {
		t.Fatalf("CheckConnection: %v", err)
	}
	if state != AuthStatePendingAuth {
		t.Fatalf("expected state %v, got %v", AuthStatePendingAuth, state)
	}

	authURL, err := manager.StartAuthFlow(t.Context())
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	if got := parsed.Query().Get("resource"); got != declaredResource {
		t.Errorf("expected resource %q on the authorization URL, got %q", declaredResource, got)
	}
}

// TestAuthManager_FallsBackToServerURL covers a server whose metadata omits
// the REQUIRED `resource` field: the canonical form of the endpoint stays the
// fallback.
func TestAuthManager_FallsBackToServerURL(t *testing.T) {
	authServer := newMetadataServer(t, false)
	musterServer := newProtectedMusterServer(t, authServer.URL, "")

	manager, err := NewAuthManager(AuthManagerConfig{TokenStorageDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	defer func() { _ = manager.Close() }()

	if _, err := manager.CheckConnection(t.Context(), musterServer.URL+"/mcp"); err != nil {
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
	// The fallback is derived from the endpoint URL, which is the value
	// mcp-go derives for the refresh request.
	want, err := pkgoauth.DeriveResourceURI(musterServer.URL + "/mcp")
	if err != nil {
		t.Fatalf("DeriveResourceURI: %v", err)
	}
	if got := parsed.Query().Get("resource"); got != want {
		t.Errorf("expected resource %q on the authorization URL, got %q", want, got)
	}
}
