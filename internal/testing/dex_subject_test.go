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
	if got := dexSignInSubject("alice", signIn); got != "CgVhbGljZRIGZ2l0aHVi" {
		t.Errorf("dexSignInSubject = %q, want %q", got, "CgVhbGljZRIGZ2l0aHVi")
	}
}
