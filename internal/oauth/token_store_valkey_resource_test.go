package oauth

import (
	"testing"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// The resource a grant is bound to survives the Valkey entry, so another
// replica tells which server the person's grant was issued for.
func TestValkeyTokenEntry_KeepsResource(t *testing.T) {
	token := &pkgoauth.Token{AccessToken: "at", Issuer: "https://as.example.com", Resource: "https://connector.example.com/mcp"}
	if got := entryToToken(tokenToEntry(token, "alice")).Resource; got != token.Resource {
		t.Fatalf("resource = %q, want %q", got, token.Resource)
	}
}
