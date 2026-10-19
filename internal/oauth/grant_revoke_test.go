package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// revokedToken is one RFC 7009 request a revocationAS received.
type revokedToken struct {
	token, hint, clientID, clientSecret string
}

// revocationAS is an authorization server like GitLab's: it publishes RFC
// 8414 metadata with a revocation_endpoint and records every revocation
// request, answering status (200 when zero).
type revocationAS struct {
	mu        sync.Mutex
	revoked   []revokedToken
	status    int
	advertise bool
}

func (a *revocationAS) handler(t *testing.T, issuer *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			metadata := map[string]any{
				"issuer":                           *issuer,
				"authorization_endpoint":           *issuer + "/oauth/authorize",
				"token_endpoint":                   *issuer + "/oauth/token",
				"response_types_supported":         []string{"code"},
				"code_challenge_methods_supported": []string{"S256"},
			}
			if a.advertise {
				metadata["revocation_endpoint"] = *issuer + "/oauth/revoke"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(metadata)
		case "/oauth/revoke", "/pinned/revoke":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			a.mu.Lock()
			a.revoked = append(a.revoked, revokedToken{
				token:        r.PostForm.Get("token"),
				hint:         r.PostForm.Get("token_type_hint"),
				clientID:     r.PostForm.Get("client_id"),
				clientSecret: r.PostForm.Get("client_secret"),
			})
			a.mu.Unlock()
			if a.status != 0 {
				w.WriteHeader(a.status)
			}
		default:
			http.NotFound(w, r)
		}
	}
}

func (a *revocationAS) requests() []revokedToken {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]revokedToken(nil), a.revoked...)
}

// newRevocationClient serves as on a TLS test server and returns a client
// with the server's issuer pinned subject-scoped under a pre-registered
// client, discovered (no pinned endpoints).
func newRevocationClient(t *testing.T, as *revocationAS, pin IssuerPin) (*Client, string) {
	t.Helper()
	var issuer string
	srv := httptest.NewTLSServer(as.handler(t, &issuer))
	t.Cleanup(srv.Close)
	issuer = srv.URL

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	client.oauthClient = pkgoauth.NewClient(pkgoauth.WithHTTPClient(srv.Client()))
	pin.ClientID, pin.ClientSecret, pin.SubjectScoped = "gitlab-app", "gitlab-secret", true
	if pin.RevocationEndpoint != "" {
		pin.RevocationEndpoint = issuer + pin.RevocationEndpoint
	}
	client.PinIssuer(issuer, pin, nil)
	t.Cleanup(client.Stop)

	client.StoreToken("session-a", "alice", &pkgoauth.Token{
		AccessToken:  "glpat_access",
		TokenType:    "Bearer",
		RefreshToken: "glrt_refresh",
		ExpiresIn:    7200,
		ExpiresAt:    time.Now().Add(time.Hour),
		Scope:        "api",
		Issuer:       issuer,
	})
	return client, issuer
}

func assertGrantGone(t *testing.T, client *Client, issuer string) {
	t.Helper()
	if got := client.GetByIssuerForUser("session-b", "alice", issuer); got != nil {
		t.Errorf("grant still in muster after the disconnect: %+v", got)
	}
}

func TestDeleteByIssuerForUser_RevokesTheGrantAtTheProvider(t *testing.T) {
	as := &revocationAS{advertise: true}
	client, issuer := newRevocationClient(t, as, IssuerPin{})

	client.DeleteByIssuerForUser("session-b", "alice", issuer)

	want := []revokedToken{
		{token: "glrt_refresh", hint: "refresh_token", clientID: "gitlab-app", clientSecret: "gitlab-secret"},
		{token: "glpat_access", hint: "access_token", clientID: "gitlab-app", clientSecret: "gitlab-secret"},
	}
	got := as.requests()
	if len(got) != len(want) {
		t.Fatalf("revocation requests = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("revocation %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	assertGrantGone(t, client, issuer)
}

func TestDeleteByIssuerForUser_PinnedRevocationEndpointWins(t *testing.T) {
	as := &revocationAS{advertise: true}
	client, issuer := newRevocationClient(t, as, IssuerPin{RevocationEndpoint: "/pinned/revoke"})

	client.DeleteByIssuerForUser("session-a", "alice", issuer)

	if got := as.requests(); len(got) != 2 {
		t.Fatalf("revocation requests at the pinned endpoint = %+v, want 2", got)
	}
	assertGrantGone(t, client, issuer)
}

func TestDeleteByIssuerForUser_NoRevocationEndpointDropsTheGrant(t *testing.T) {
	as := &revocationAS{}
	client, issuer := newRevocationClient(t, as, IssuerPin{})

	client.DeleteByIssuerForUser("session-a", "alice", issuer)

	if got := as.requests(); len(got) != 0 {
		t.Errorf("revocation requests = %+v, want none without a revocation endpoint", got)
	}
	assertGrantGone(t, client, issuer)
}

func TestDeleteByIssuerForUser_RefusedRevocationStillDropsTheGrant(t *testing.T) {
	as := &revocationAS{advertise: true, status: http.StatusServiceUnavailable}
	client, issuer := newRevocationClient(t, as, IssuerPin{})

	client.DeleteByIssuerForUser("session-a", "alice", issuer)

	// The access token is still tried after the refresh token failed.
	if got := as.requests(); len(got) != 2 {
		t.Errorf("revocation requests = %+v, want both tokens tried", got)
	}
	assertGrantGone(t, client, issuer)
}

func TestDeleteByIssuerForUser_UnreachableProviderStillDropsTheGrant(t *testing.T) {
	var issuer string
	as := &revocationAS{advertise: true}
	srv := httptest.NewTLSServer(as.handler(t, &issuer))
	issuer = srv.URL

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	client.oauthClient = pkgoauth.NewClient(pkgoauth.WithHTTPClient(srv.Client()))
	client.PinIssuer(issuer, IssuerPin{SubjectScoped: true}, nil)
	defer client.Stop()
	client.StoreToken("session-a", "alice", &pkgoauth.Token{AccessToken: "glpat_access", RefreshToken: "glrt_refresh", Issuer: issuer, Scope: "api"})
	srv.Close()

	client.DeleteByIssuerForUser("session-a", "alice", issuer)

	assertGrantGone(t, client, issuer)
}

func TestDeleteByIssuerForUser_SessionScopedIssuerRevokesNothing(t *testing.T) {
	var issuer string
	as := &revocationAS{advertise: true}
	srv := httptest.NewTLSServer(as.handler(t, &issuer))
	defer srv.Close()
	issuer = srv.URL

	client := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	client.oauthClient = pkgoauth.NewClient(pkgoauth.WithHTTPClient(srv.Client()))
	defer client.Stop()
	client.StoreToken("session-a", "alice", &pkgoauth.Token{AccessToken: "session_access", RefreshToken: "session_refresh", Issuer: issuer, Scope: "api"})

	client.DeleteByIssuerForUser("session-a", "alice", issuer)

	if got := as.requests(); len(got) != 0 {
		t.Errorf("revocation requests = %+v, want none for a session-scoped issuer", got)
	}
}
