package oauth

import (
	"context"
	"net/url"
	"strings"
	"testing"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

const (
	pinnedIssuer = "https://github.com/login/oauth"
	pinnedAuthEP = "https://github.com/login/oauth/authorize"
	pinnedTokEP  = "https://github.com/login/oauth/access_token"
)

func pinnedMetadata() *pkgoauth.Metadata {
	return &pkgoauth.Metadata{AuthorizationEndpoint: pinnedAuthEP, TokenEndpoint: pinnedTokEP}
}

func TestClient_PinIssuer_PreregisteredClientDrivesTheFlow(t *testing.T) {
	client := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	defer client.Stop()

	client.PinIssuer(pinnedIssuer+"/", IssuerPin{ClientID: "Iv23liPinned", ClientSecret: "s3cr3t"}, pinnedMetadata())

	// No HTTP server exists for the issuer: the pinned metadata must carry the flow.
	authURL, resolved, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID:  testSubject,
		UserID:     "test-user",
		ServerName: testServerName,
		Issuer:     pinnedIssuer,
		Resource:   testResource,
		Scope:      "repo read:org",
	})
	if err != nil {
		t.Fatalf("GenerateAuthURL against a pinned issuer: %v", err)
	}
	if resolved.Method != ClientIDMethodPreregistered {
		t.Errorf("method = %q, want %q", resolved.Method, ClientIDMethodPreregistered)
	}
	if resolved.ClientID != "Iv23liPinned" || resolved.ClientSecret != "s3cr3t" {
		t.Errorf("resolved client = %+v, want the pinned credentials", resolved)
	}

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse start URL: %v", err)
	}
	state := client.stateStore.Update(parsed.Query().Get("state"), func(*OAuthState) {})
	if state == nil {
		t.Fatal("state should be stored")
	}
	if !strings.HasPrefix(state.AuthorizationURL, pinnedAuthEP+"?") {
		t.Errorf("upstream URL should target the pinned authorization endpoint, got %q", state.AuthorizationURL)
	}
	upstream, _ := url.Parse(state.AuthorizationURL)
	if got := upstream.Query().Get("client_id"); got != "Iv23liPinned" {
		t.Errorf("client_id = %q, want the pinned client", got)
	}
	if upstream.Query().Get("code_challenge_method") != "S256" {
		t.Error("PKCE S256 is still sent against a pinned AS")
	}

	// Transport-level refresh presents the same client.
	id, secret := client.GetClientCredentialsForIssuer(context.Background(), pinnedIssuer)
	if id != "Iv23liPinned" || secret != "s3cr3t" {
		t.Errorf("GetClientCredentialsForIssuer = (%q, %q), want the pinned credentials", id, secret)
	}
}

func TestClient_SubjectScopedGrants(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	defer client.Stop()

	client.PinIssuer(pinnedIssuer, IssuerPin{SubjectScoped: true}, pinnedMetadata())
	token := &pkgoauth.Token{AccessToken: "gho_user", TokenType: "Bearer", Scope: "repo", Issuer: pinnedIssuer}

	if !client.IssuerSubjectScoped(pinnedIssuer) || !client.IssuerSubjectScoped(pinnedIssuer+"/") {
		t.Error("IssuerSubjectScoped should report the pin, trailing slash or not")
	}
	if client.IssuerSubjectScoped("https://auth.example.com") {
		t.Error("an unpinned issuer is session-scoped")
	}

	// Session A completes the flow.
	client.StoreToken("session-a", "alice@example.com", token)

	// Session B of the same person finds the grant, a session of someone else does not.
	if got := client.GetTokenForUser("session-b", "alice@example.com", pinnedIssuer, "repo"); got == nil || got.AccessToken != "gho_user" {
		t.Fatalf("GetTokenForUser for another session of the same subject = %v, want the grant", got)
	}
	if got := client.GetByIssuerForUser("session-b", "alice@example.com", pinnedIssuer); got == nil {
		t.Fatal("GetByIssuerForUser should find the subject grant")
	}
	if got := client.GetTokenForUser("session-c", "bob@example.com", pinnedIssuer, "repo"); got != nil {
		t.Errorf("another subject must not see the grant, got %v", got)
	}
	if got := client.GetTokenForUser("session-b", "", pinnedIssuer, "repo"); got != nil {
		t.Errorf("an anonymous session must not see the grant, got %v", got)
	}

	// Session-only lookups keep their semantics: session B holds nothing itself.
	if got := client.GetToken("session-b", pinnedIssuer, "repo"); got != nil {
		t.Errorf("GetToken without the subject fallback = %v, want nil", got)
	}

	// Signing out of the server as the person removes the grant for every session.
	client.DeleteByIssuerForUser("session-b", "alice@example.com", pinnedIssuer)
	if got := client.GetTokenForUser("session-a", "alice@example.com", pinnedIssuer, "repo"); got != nil {
		t.Errorf("grant should be gone after DeleteByIssuerForUser, got %v", got)
	}
}

func TestClient_SessionScopedIssuerHasNoSubjectFallback(t *testing.T) {
	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	defer client.Stop()

	issuer := "https://auth.example.com"
	client.StoreToken("session-a", "alice@example.com", &pkgoauth.Token{AccessToken: "t", Issuer: issuer, Scope: testScopes})

	if got := client.GetTokenForUser("session-b", "alice@example.com", issuer, testScopes); got != nil {
		t.Errorf("a session-scoped issuer must not share grants across sessions, got %v", got)
	}
	if got := client.GetTokenForUser("session-a", "alice@example.com", issuer, testScopes); got == nil {
		t.Error("the owning session still finds its token")
	}
}
