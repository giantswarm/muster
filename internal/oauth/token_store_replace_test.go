package oauth

import (
	"testing"
	"time"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

func TestTokenStore_GetByIssuerIncludingExpired(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()
	issuer := "https://github.com/login/oauth"

	if got := ts.GetByIssuerIncludingExpired("s", issuer); got != nil {
		t.Fatalf("empty store returned %+v", got)
	}

	expired := &pkgoauth.Token{AccessToken: "old", RefreshToken: "ghr", ExpiresAt: time.Now().Add(-time.Hour), Issuer: issuer}
	ts.Store(TokenKey{SessionID: "s", Issuer: issuer, Scope: "repo"}, expired, "alice")

	if got := ts.GetByIssuer("s", issuer); got != nil {
		t.Errorf("GetByIssuer must hide the expired token, got %+v", got)
	}
	got := ts.GetByIssuerIncludingExpired("s", issuer)
	if got == nil || got.AccessToken != "old" || got.RefreshToken != "ghr" {
		t.Fatalf("GetByIssuerIncludingExpired = %+v, want the expired token", got)
	}

	// An ID-only entry for the same issuer must not shadow the access token.
	ts.Store(TokenKey{SessionID: "s", Issuer: issuer, Scope: ""}, &pkgoauth.Token{IDToken: "id", Issuer: issuer}, "alice")
	if got := ts.GetByIssuerIncludingExpired("s", issuer); got == nil || got.AccessToken != "old" {
		t.Errorf("an entry with an access token wins, got %+v", got)
	}
}

func TestTokenStore_ReplaceByUserAndIssuer(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()
	issuer := "https://github.com/login/oauth"
	other := "https://auth.example.com"

	old := func() *pkgoauth.Token {
		return &pkgoauth.Token{AccessToken: "old", RefreshToken: "ghr_old", ExpiresAt: time.Now().Add(-time.Minute), Issuer: issuer, Scope: "repo"}
	}
	ts.Store(TokenKey{SessionID: "session-a", Issuer: issuer, Scope: "repo"}, old(), "alice")
	ts.Store(TokenKey{SessionID: "session-b", Issuer: issuer, Scope: "repo"}, old(), "alice")
	ts.Store(TokenKey{SessionID: "subject:alice", Issuer: issuer, Scope: "repo"}, old(), "alice")
	// Untouched: another person's copy, and alice's token for another issuer.
	ts.Store(TokenKey{SessionID: "session-bob", Issuer: issuer, Scope: "repo"}, old(), "bob")
	ts.Store(TokenKey{SessionID: "session-a", Issuer: other, Scope: "openid"}, &pkgoauth.Token{AccessToken: "other", Issuer: other}, "alice")

	fresh := &pkgoauth.Token{AccessToken: "new", RefreshToken: "ghr_new", ExpiresIn: 28800, Issuer: issuer, Scope: "repo"}
	if n := ts.ReplaceByUserAndIssuer("alice", issuer, fresh); n != 3 {
		t.Fatalf("replaced %d entries, want 3", n)
	}
	for _, session := range []string{"session-a", "session-b", "subject:alice"} {
		got := ts.Get(TokenKey{SessionID: session, Issuer: issuer, Scope: "repo"})
		if got == nil || got.AccessToken != "new" || got.RefreshToken != "ghr_new" || got.ExpiresAt.IsZero() {
			t.Errorf("%s = %+v, want the replacement with a computed expiry", session, got)
		}
	}
	if got := ts.GetByIssuerIncludingExpired("session-bob", issuer); got == nil || got.AccessToken != "old" {
		t.Errorf("bob's copy changed: %+v", got)
	}
	if got := ts.Get(TokenKey{SessionID: "session-a", Issuer: other, Scope: "openid"}); got == nil || got.AccessToken != "other" {
		t.Errorf("alice's other issuer changed: %+v", got)
	}
	if n := ts.ReplaceByUserAndIssuer("nobody", issuer, fresh); n != 0 {
		t.Errorf("replaced %d for an unknown user, want 0", n)
	}
}

func TestTokenStore_CleanupKeepsRefreshableTokens(t *testing.T) {
	ts := NewTokenStore()
	defer ts.Stop()
	issuer := "https://github.com/login/oauth"

	ts.Store(TokenKey{SessionID: "refreshable", Issuer: issuer, Scope: "repo"},
		&pkgoauth.Token{AccessToken: "old", RefreshToken: "ghr", ExpiresAt: time.Now().Add(-time.Hour), Issuer: issuer}, "alice")
	ts.Store(TokenKey{SessionID: "dead", Issuer: issuer, Scope: "repo"},
		&pkgoauth.Token{AccessToken: "old", ExpiresAt: time.Now().Add(-time.Hour), Issuer: issuer}, "alice")
	ts.Store(TokenKey{SessionID: "ancient", Issuer: issuer, Scope: "repo"},
		&pkgoauth.Token{AccessToken: "old", RefreshToken: "ghr", ExpiresAt: time.Now().Add(-refreshableTokenRetention - time.Hour), Issuer: issuer}, "alice")

	ts.cleanup()

	if got := ts.GetByIssuerIncludingExpired("refreshable", issuer); got == nil {
		t.Error("an expired token with a refresh token must survive cleanup")
	}
	if got := ts.GetByIssuerIncludingExpired("dead", issuer); got != nil {
		t.Errorf("an expired token without a refresh token is swept, got %+v", got)
	}
	if got := ts.GetByIssuerIncludingExpired("ancient", issuer); got != nil {
		t.Errorf("a refreshable token past the retention is swept, got %+v", got)
	}
}

func TestPreferToken(t *testing.T) {
	later := &pkgoauth.Token{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)}
	sooner := &pkgoauth.Token{AccessToken: "a", ExpiresAt: time.Now().Add(time.Minute)}
	forever := &pkgoauth.Token{AccessToken: "a"}
	idOnly := &pkgoauth.Token{IDToken: "id", ExpiresAt: time.Now().Add(24 * time.Hour)}

	if !preferToken(nil, sooner) || preferToken(sooner, nil) {
		t.Error("anything beats nothing")
	}
	if !preferToken(sooner, later) || preferToken(later, sooner) {
		t.Error("the later expiry wins")
	}
	if !preferToken(later, forever) || preferToken(forever, later) {
		t.Error("no expiry counts as latest")
	}
	if !preferToken(idOnly, sooner) || preferToken(sooner, idOnly) {
		t.Error("an access token beats an ID-only entry whatever the expiry")
	}
}
