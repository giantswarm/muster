package testing

import (
	"net/url"
	"testing"
)

func TestDexSubject(t *testing.T) {
	if got := dexSubject("alice", "github"); got != "CgVhbGljZRIGZ2l0aHVi" {
		t.Errorf("dexSubject(alice, github) = %q, want %q", got, "CgVhbGljZRIGZ2l0aHVi")
	}
	if got := dexSubject("alice", "ldap"); got != "CgVhbGljZRIEbGRhcA" {
		t.Errorf("dexSubject(alice, ldap) = %q, want %q", got, "CgVhbGljZRIEbGRhcA")
	}
}

func TestDexSignInSubject_UsesTheRequestsConnector(t *testing.T) {
	signIn, err := url.Parse("https://dex.example.com/auth?client_id=muster&connector_id=github")
	if err != nil {
		t.Fatal(err)
	}
	got, err := dexSignInSubject("alice", signIn)
	if err != nil || got != "CgVhbGljZRIGZ2l0aHVi" {
		t.Errorf("dexSignInSubject = %q, %v; want %q", got, err, "CgVhbGljZRIGZ2l0aHVi")
	}
}

// Without a connector in the request Dex shows its chooser; the harness does
// not make up a subject Dex never issues.
func TestDexSignInSubject_RefusesARequestWithoutConnector(t *testing.T) {
	signIn, err := url.Parse("https://dex.example.com/auth?client_id=muster")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := dexSignInSubject("alice", signIn); err == nil {
		t.Errorf("dexSignInSubject = %q, want an error", got)
	}
}

func TestIdentitySignInPersonFromArgs(t *testing.T) {
	if _, err := identitySignInPersonFromArgs(map[string]interface{}{"signed_in_as": "CgVhbGljZRIGZ2l0aHVi", "signed_in_with_user": "alice"}); err == nil {
		t.Error("both arguments: want an error")
	}
	p, err := identitySignInPersonFromArgs(map[string]interface{}{"signed_in_with_user": "alice"})
	if err != nil || p.dexUserID != "alice" || p.subject != "" {
		t.Errorf("identitySignInPersonFromArgs = %+v, %v", p, err)
	}
	p, err = identitySignInPersonFromArgs(map[string]interface{}{})
	if err != nil || p != (identitySignInPerson{}) {
		t.Errorf("no arguments: identitySignInPersonFromArgs = %+v, %v; want the link's owner", p, err)
	}
}
