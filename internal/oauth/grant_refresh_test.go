package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// rotatingAS is a token endpoint that behaves like GitHub's: every
// refresh_token grant answers a new access token and a new refresh token,
// and a refresh token that was already redeemed (or never issued) is
// rejected with invalid_grant. It counts the refresh requests it served.
type rotatingAS struct {
	mu       sync.Mutex
	valid    map[string]bool // refresh tokens still redeemable
	issued   int
	requests atomic.Int32
	// respondError, when set, is the RFC 6749 error code answered with HTTP
	// 200 -- GitHub's way of reporting a dead refresh token.
	respondError string
	// statusOnError, when set with respondError, is answered instead of 200.
	statusOnError int
	// expiresIn is the lifetime the AS grants refreshed tokens.
	expiresIn int
	// delay stretches every request so concurrent callers overlap.
	delay time.Duration
}

func newRotatingAS(initialRefreshToken string) *rotatingAS {
	return &rotatingAS{valid: map[string]bool{initialRefreshToken: true}, expiresIn: 3600}
}

func (a *rotatingAS) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		a.requests.Add(1)
		if a.delay > 0 {
			time.Sleep(a.delay)
		}
		w.Header().Set("Content-Type", "application/json")
		if a.respondError != "" {
			if a.statusOnError != 0 {
				w.WriteHeader(a.statusOnError)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"error": a.respondError, "error_description": "The refresh token passed is incorrect or expired."})
			return
		}
		if r.Form.Get("grant_type") != "refresh_token" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unsupported_grant_type"})
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		rt := r.Form.Get("refresh_token")
		if !a.valid[rt] {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "refresh token already used"})
			return
		}
		delete(a.valid, rt)
		a.issued++
		newRT := "ghr_rotated_" + strings.Repeat("x", a.issued)
		a.valid[newRT] = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "ghu_refreshed_" + strings.Repeat("y", a.issued),
			"token_type":    "bearer",
			"refresh_token": newRT,
			"expires_in":    a.expiresIn,
		})
	}
}

// newGrantClient builds a Client whose pinned issuer is served by the given
// test server (its /token endpoint), with a subject-scoped pin.
func newGrantClient(t *testing.T, srv *httptest.Server) (*Client, string) {
	t.Helper()
	issuer := srv.URL
	client := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	client.oauthClient = pkgoauth.NewClient(pkgoauth.WithHTTPClient(srv.Client()))
	client.PinIssuer(issuer, IssuerPin{ClientID: "Iv23liPinned", ClientSecret: "s3cr3t", SubjectScoped: true}, &pkgoauth.Metadata{
		AuthorizationEndpoint: issuer + "/authorize",
		TokenEndpoint:         issuer + "/token",
	})
	t.Cleanup(client.Stop)
	return client, issuer
}

// storeDueGrant files a grant for alice under session-a that is inside its
// refresh margin (expires in 20 s out of a 3600 s lifetime).
func storeDueGrant(client *Client, issuer, refreshToken string, sessions ...string) {
	for _, session := range sessions {
		client.StoreToken(session, "alice", &pkgoauth.Token{
			AccessToken:  "ghu_original",
			TokenType:    "Bearer",
			RefreshToken: refreshToken,
			ExpiresIn:    3600,
			ExpiresAt:    time.Now().Add(20 * time.Second),
			Scope:        "repo",
			Issuer:       issuer,
		})
	}
}

func TestSubjectGrant_RefreshesDueGrantAndRotatesEveryCopy(t *testing.T) {
	as := newRotatingAS("ghr_initial")
	srv := httptest.NewTLSServer(as.handler(t))
	defer srv.Close()
	client, issuer := newGrantClient(t, srv)

	storeDueGrant(client, issuer, "ghr_initial", "session-a", "session-b")

	got := client.GetByIssuerForUser("session-b", "alice", issuer)
	if got == nil || got.AccessToken != "ghu_refreshed_y" {
		t.Fatalf("GetByIssuerForUser = %+v, want the refreshed token", got)
	}
	if got.RefreshToken != "ghr_rotated_x" {
		t.Errorf("refresh token = %q, want the rotated one", got.RefreshToken)
	}
	if got.Scope != "repo" || got.Issuer != issuer {
		t.Errorf("scope/issuer not carried over: %+v", got)
	}
	if got.ExpiresAt.Before(time.Now().Add(50 * time.Minute)) {
		t.Errorf("expiry not taken from the refresh response: %v", got.ExpiresAt)
	}
	if n := as.requests.Load(); n != 1 {
		t.Errorf("token endpoint requests = %d, want 1", n)
	}

	// Every copy -- both sessions and the grant itself -- carries the rotated
	// token now, so no session can redeem the dead refresh token again.
	for _, session := range []string{"session-a", "session-b", subjectSessionID("alice")} {
		copy := client.tokenStore.GetByIssuerIncludingExpired(session, issuer)
		if copy == nil || copy.AccessToken != "ghu_refreshed_y" || copy.RefreshToken != "ghr_rotated_x" {
			t.Errorf("copy under %s = %+v, want the rotated token", session, copy)
		}
	}

	// A second lookup is served from the store without another request.
	if again := client.GetTokenForUser("session-a", "alice", issuer, "repo"); again == nil || again.AccessToken != "ghu_refreshed_y" {
		t.Fatalf("second lookup = %+v", again)
	}
	if n := as.requests.Load(); n != 1 {
		t.Errorf("a fresh grant must not be refreshed again, requests = %d", n)
	}
}

func TestSubjectGrant_ConcurrentLookupsRefreshOnce(t *testing.T) {
	as := newRotatingAS("ghr_initial")
	as.delay = 50 * time.Millisecond
	srv := httptest.NewTLSServer(as.handler(t))
	defer srv.Close()
	client, issuer := newGrantClient(t, srv)

	storeDueGrant(client, issuer, "ghr_initial", "session-a", "session-b", "session-c")

	const callers = 24
	results := make([]*pkgoauth.Token, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			session := []string{"session-a", "session-b", "session-c"}[i%3]
			results[i] = client.GetByIssuerForUser(session, "alice", issuer)
		}(i)
	}
	wg.Wait()

	for i, got := range results {
		if got == nil || got.AccessToken != "ghu_refreshed_y" {
			t.Errorf("caller %d got %+v, want the single refreshed token", i, got)
		}
	}
	// One redemption for the whole herd: a second one would have been
	// rejected by the rotating AS and cleared the grant for everyone.
	if n := as.requests.Load(); n != 1 {
		t.Errorf("token endpoint requests = %d, want exactly 1", n)
	}
	if grant := client.SubjectGrant(context.Background(), "alice", issuer); grant == nil {
		t.Fatal("the grant must survive the concurrent refresh")
	}
}

func TestSubjectGrant_FreshGrantIsNotRefreshed(t *testing.T) {
	as := newRotatingAS("ghr_initial")
	srv := httptest.NewTLSServer(as.handler(t))
	defer srv.Close()
	client, issuer := newGrantClient(t, srv)

	client.StoreToken("session-a", "alice", &pkgoauth.Token{
		AccessToken: "ghu_original", RefreshToken: "ghr_initial", ExpiresIn: 28800,
		ExpiresAt: time.Now().Add(8 * time.Hour), Scope: "repo", Issuer: issuer,
	})
	got := client.GetByIssuerForUser("session-b", "alice", issuer)
	if got == nil || got.AccessToken != "ghu_original" {
		t.Fatalf("got %+v, want the stored token untouched", got)
	}
	if n := as.requests.Load(); n != 0 {
		t.Errorf("no refresh expected for a fresh grant, requests = %d", n)
	}
}

func TestSubjectGrant_RejectedRefreshTokenClearsTheGrant(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   string
		status int
	}{
		{name: "invalid_grant with 400", code: "invalid_grant", status: http.StatusBadRequest},
		{name: "GitHub bad_refresh_token with 200", code: "bad_refresh_token", status: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			as := newRotatingAS("ghr_initial")
			as.respondError, as.statusOnError = tc.code, tc.status
			srv := httptest.NewTLSServer(as.handler(t))
			defer srv.Close()
			client, issuer := newGrantClient(t, srv)

			storeDueGrant(client, issuer, "ghr_initial", "session-a")

			if got := client.GetByIssuerForUser("session-a", "alice", issuer); got != nil {
				t.Fatalf("got %+v, want nil: the grant is dead", got)
			}
			// The grant and the session copy are gone, so the next use
			// asks the person to sign in instead of retrying a dead token.
			if copy := client.tokenStore.GetByIssuerIncludingExpired(subjectSessionID("alice"), issuer); copy != nil {
				t.Errorf("grant still stored after rejection: %+v", copy)
			}
			if copy := client.tokenStore.GetByIssuerIncludingExpired("session-a", issuer); copy != nil {
				t.Errorf("session copy still stored after rejection: %+v", copy)
			}
		})
	}
}

func TestSubjectGrant_TransientFailureKeepsAUsableToken(t *testing.T) {
	as := newRotatingAS("ghr_initial")
	as.respondError, as.statusOnError = "server_error", http.StatusInternalServerError
	srv := httptest.NewTLSServer(as.handler(t))
	defer srv.Close()
	client, issuer := newGrantClient(t, srv)

	// Due for refresh (within the margin) but still accepted by the server.
	client.StoreToken("session-a", "alice", &pkgoauth.Token{
		AccessToken: "ghu_original", RefreshToken: "ghr_initial", ExpiresIn: 3600,
		ExpiresAt: time.Now().Add(2 * time.Minute), Scope: "repo", Issuer: issuer,
	})
	got := client.GetByIssuerForUser("session-a", "alice", issuer)
	if got == nil || got.AccessToken != "ghu_original" {
		t.Fatalf("got %+v, want the still-valid original while the AS is down", got)
	}
	if copy := client.tokenStore.GetByIssuerIncludingExpired(subjectSessionID("alice"), issuer); copy == nil {
		t.Error("a transient failure must not clear the grant")
	}

	// Past expiry there is nothing usable to hand out, but the grant stays
	// for the next attempt.
	client.StoreToken("session-a", "alice", &pkgoauth.Token{
		AccessToken: "ghu_expired", RefreshToken: "ghr_initial", ExpiresIn: 3600,
		ExpiresAt: time.Now().Add(-time.Minute), Scope: "repo", Issuer: issuer,
	})
	if got := client.GetByIssuerForUser("session-a", "alice", issuer); got != nil {
		t.Errorf("an expired token must not be handed out, got %+v", got)
	}
	if copy := client.tokenStore.GetByIssuerIncludingExpired(subjectSessionID("alice"), issuer); copy == nil {
		t.Error("a transient failure must not clear the expired grant either")
	}
}

func TestSubjectGrant_ExpiredWithoutRefreshTokenIsGone(t *testing.T) {
	as := newRotatingAS("unused")
	srv := httptest.NewTLSServer(as.handler(t))
	defer srv.Close()
	client, issuer := newGrantClient(t, srv)

	client.StoreToken("session-a", "alice", &pkgoauth.Token{
		AccessToken: "ghu_old", ExpiresAt: time.Now().Add(-time.Minute), Issuer: issuer, Scope: "repo",
	})
	if got := client.GetByIssuerForUser("session-a", "alice", issuer); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
	if n := as.requests.Load(); n != 0 {
		t.Errorf("nothing to redeem, requests = %d", n)
	}
}

func TestSubjectGrant_SessionScopedIssuerIsUntouched(t *testing.T) {
	as := newRotatingAS("ghr_initial")
	srv := httptest.NewTLSServer(as.handler(t))
	defer srv.Close()
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	client.oauthClient = pkgoauth.NewClient(pkgoauth.WithHTTPClient(srv.Client()))
	defer client.Stop()
	issuer := srv.URL
	// Pinned, but session-scoped: the refresh path does not apply.
	client.PinIssuer(issuer, IssuerPin{ClientID: "c"}, &pkgoauth.Metadata{AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token"})

	// Inside the subject-grant refresh margin (5 min), outside the store's
	// 30 s hiding margin: a subject-scoped pin would refresh this.
	client.StoreToken("session-a", "alice", &pkgoauth.Token{
		AccessToken: "t", RefreshToken: "ghr_initial", ExpiresIn: 3600,
		ExpiresAt: time.Now().Add(2 * time.Minute), Issuer: issuer, Scope: "repo",
	})
	if got := client.SubjectGrant(context.Background(), "alice", issuer); got != nil {
		t.Errorf("SubjectGrant for a session-scoped issuer = %+v, want nil", got)
	}
	if got := client.GetByIssuerForUser("session-a", "alice", issuer); got == nil || got.AccessToken != "t" {
		t.Errorf("the session's own token is returned as before, got %+v", got)
	}
	if n := as.requests.Load(); n != 0 {
		t.Errorf("no refresh for a session-scoped issuer, requests = %d", n)
	}
}

func TestRefreshMargin(t *testing.T) {
	cases := []struct {
		name      string
		expiresIn int
		want      time.Duration
	}{
		{"GitHub eight hours", 28800, 5 * time.Minute},
		{"ten minute token: half the lifetime", 600, 5 * time.Minute},
		{"two minute token: half the lifetime", 120, time.Minute},
		{"forty second token: floored at the hiding margin", 40, 30 * time.Second},
		{"unknown lifetime", 0, 5 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := refreshMargin(&pkgoauth.Token{ExpiresIn: tc.expiresIn}); got != tc.want {
				t.Errorf("refreshMargin = %v, want %v", got, tc.want)
			}
		})
	}
	if refreshDue(&pkgoauth.Token{}) {
		t.Error("a token without expiry is never due")
	}
	if !refreshDue(&pkgoauth.Token{ExpiresIn: 28800, ExpiresAt: time.Now().Add(4 * time.Minute)}) {
		t.Error("four minutes left of eight hours is due")
	}
	if refreshDue(&pkgoauth.Token{ExpiresIn: 28800, ExpiresAt: time.Now().Add(6 * time.Minute)}) {
		t.Error("six minutes left of eight hours is not due")
	}
}
