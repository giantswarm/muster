package oauth

import (
	"context"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/config"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// A grant stored through the manager (the device-code sign-in's path) is the
// person's for a subject-scoped issuer: the credential provider releases it
// to any of their sessions. Another issuer's token stays with its session.
func TestManager_StoreToken_FilesSubjectScopedGrantUnderThePerson(t *testing.T) {
	manager := NewManager(config.OAuthMCPClientConfig{
		Enabled: true, PublicURL: "https://muster.example.com", ClientID: "client-id", CallbackPath: "/oauth/proxy/callback",
	})
	defer manager.Stop()
	const (
		pinned   = "https://auth.openai.com"
		unpinned = "https://idp.example.com"
	)
	manager.client.PinIssuer(pinned, IssuerPin{SubjectScoped: true}, nil)
	exp := time.Now().Add(time.Hour)

	manager.StoreToken("asking-session", "alice", pinned, &pkgoauth.Token{AccessToken: "chatgpt-at", ExpiresAt: exp, Issuer: pinned})
	manager.StoreToken("asking-session", "alice", unpinned, &pkgoauth.Token{AccessToken: "idp-at", ExpiresAt: exp, Issuer: unpinned})

	if got := manager.SubjectGrantForRelease(context.Background(), "alice", pinned); got == nil || got.AccessToken != "chatgpt-at" {
		t.Fatalf("release of the subject-scoped grant = %+v, want the stored access token", got)
	}
	if got := manager.SubjectGrantForRelease(context.Background(), "bob", pinned); got != nil {
		t.Fatalf("release for another person = %+v, want none", got)
	}
	if got := manager.client.tokenStore.GetByIssuerIncludingExpired(subjectSessionID("alice"), unpinned); got != nil {
		t.Fatalf("token of an issuer that is not subject-scoped filed under the person: %+v", got)
	}
	if got := manager.client.tokenStore.GetByIssuerIncludingExpired("asking-session", pinned); got == nil || got.AccessToken != "chatgpt-at" {
		t.Fatalf("session copy = %+v, want the stored access token", got)
	}
}
