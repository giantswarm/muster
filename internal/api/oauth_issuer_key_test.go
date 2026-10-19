package api

import "testing"

// The issuer key is a storage contract: the Valkey token fields, the
// credential and pin maps and the stored flow state are filed under it. A
// server of muster's namespace must keep the plain issuer, or every person
// signs in again after the upgrade; a server outside it must never share a key
// with muster's namespace or with another namespace.
func TestIssuerKey(t *testing.T) {
	const issuer = "https://github.com/login/oauth"
	tests := []struct {
		name          string
		serverKey     string
		issuer        string
		wantKey       string
		wantNamespace string
		wantPlain     string
	}{
		{name: "muster's namespace keeps the plain issuer", serverKey: "github", issuer: issuer + "/", wantKey: issuer + "/", wantPlain: issuer + "/"},
		{name: "a tenant carries its namespace", serverKey: "org-a/github", issuer: issuer + "/", wantKey: "ns:org-a:" + issuer, wantNamespace: "org-a", wantPlain: issuer},
		{name: "another tenant gets another key", serverKey: "org-b/github", issuer: issuer, wantKey: "ns:org-b:" + issuer, wantNamespace: "org-b", wantPlain: issuer},
		{name: "no issuer stays empty", serverKey: "org-a/github", issuer: "", wantKey: ""},
		{name: "muster's namespace cannot name a tenant's key as its issuer", serverKey: "github", issuer: "ns:org-a:" + issuer, wantKey: ""},
		{name: "nor can a tenant name another tenant's key", serverKey: "org-b/github", issuer: "ns:org-a:" + issuer, wantKey: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := ServerIssuerKey(tc.serverKey, tc.issuer)
			if key != tc.wantKey {
				t.Fatalf("ServerIssuerKey(%q, %q) = %q, want %q", tc.serverKey, tc.issuer, key, tc.wantKey)
			}
			namespace, plain := SplitIssuerKey(key)
			if namespace != tc.wantNamespace {
				t.Errorf("namespace = %q, want %q", namespace, tc.wantNamespace)
			}
			if IsTenantIssuerKey(key) != (tc.wantNamespace != "") {
				t.Errorf("IsTenantIssuerKey(%q) = %v", key, IsTenantIssuerKey(key))
			}
			if plain != tc.wantPlain || IssuerURL(key) != tc.wantPlain {
				t.Errorf("plain issuer of %q = %q, want %q", key, plain, tc.wantPlain)
			}
		})
	}
}
