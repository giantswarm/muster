package oauth

import (
	"context"
	"testing"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// unreachableIssuer is an issuer nothing serves: discovery against it fails
// at once, so a test can tell pinned metadata from a real fetch offline.
const unreachableIssuer = "http://127.0.0.1:9/pinned-as"

func unreachablePinnedMetadata() *pkgoauth.Metadata {
	return &pkgoauth.Metadata{AuthorizationEndpoint: unreachableIssuer + "/authorize", TokenEndpoint: unreachableIssuer + "/token"}
}

func TestClient_UnpinIssuer_ForgetsClientAndMetadata(t *testing.T) {
	client := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	defer client.Stop()

	client.PinIssuer(unreachableIssuer, IssuerPin{ClientID: "Iv23liPinned", ClientSecret: "s3cr3t", SubjectScoped: true}, unreachablePinnedMetadata())
	if client.issuerPin(unreachableIssuer) == nil || !client.IssuerSubjectScoped(unreachableIssuer) {
		t.Fatal("precondition: the issuer is pinned")
	}
	if _, _, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID: testSubject, UserID: "u", ServerName: testServerName, Issuer: unreachableIssuer, Scope: "repo",
	}); err != nil {
		t.Fatalf("precondition: the pinned metadata carries the flow: %v", err)
	}

	client.UnpinIssuer(unreachableIssuer + "/")

	if client.issuerPin(unreachableIssuer) != nil {
		t.Error("the pre-registered client must be forgotten")
	}
	if client.IssuerSubjectScoped(unreachableIssuer) {
		t.Error("the grant scope must be forgotten")
	}
	// The pinned document is gone: the flow has to discover the issuer again,
	// which fails because nothing serves it.
	if _, _, err := client.GenerateAuthURL(context.Background(), AuthChallengeParams{
		SessionID: testSubject, UserID: "u", ServerName: testServerName, Issuer: unreachableIssuer, Scope: "repo",
	}); err == nil {
		t.Error("GenerateAuthURL should no longer be served from the pinned metadata")
	}
}

func TestClient_PinIssuer_WithoutEndpointsDropsPinnedMetadata(t *testing.T) {
	client := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	defer client.Stop()

	// An MCPServer first described the issuer with explicit endpoints ...
	client.PinIssuer(unreachableIssuer, IssuerPin{ClientID: "c"}, unreachablePinnedMetadata())
	if md, err := client.DiscoverMetadata(context.Background(), unreachableIssuer); err != nil || md.AuthorizationEndpoint != unreachableIssuer+"/authorize" {
		t.Fatalf("precondition: pinned metadata serves discovery, got %v / %v", md, err)
	}

	// ... and is now described by the issuer alone: discovery must run again
	// instead of answering from the document the earlier description pinned
	// (giantswarm/muster#1175).
	client.PinIssuer(unreachableIssuer, IssuerPin{ClientID: "c"}, nil)
	if _, err := client.DiscoverMetadata(context.Background(), unreachableIssuer); err == nil {
		t.Error("the endpoints pinned by the earlier description must not stand in for discovery")
	}
	if pin := client.issuerPin(unreachableIssuer); pin == nil || pin.ClientID != "c" {
		t.Error("the client of the new description is kept")
	}
}
