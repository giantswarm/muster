package callerwrite

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

// testJWT builds an unsigned-but-JWT-shaped token with the given aud claim
// (string or []string). The gate never verifies signatures — the apiserver
// does — so a fake signature part suffices.
func testJWT(t *testing.T, aud interface{}) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := map[string]interface{}{"sub": "alice@example.com", "iss": "https://dex.example.com"}
	if aud != nil {
		claims["aud"] = aud
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestTokenMissingAudience(t *testing.T) {
	jwtWith := func(aud interface{}) string { return testJWT(t, aud) }
	cases := []struct {
		name    string
		token   string
		missing bool
	}{
		{"string-match", jwtWith(DefaultKubernetesAudience), false},
		{"list-match", jwtWith([]string{"x", DefaultKubernetesAudience}), false},
		{"string-mismatch", jwtWith("other"), true},
		{"list-mismatch", jwtWith([]string{"a", "b"}), true},
		{"absent", jwtWith(nil), true},
		{"opaque-token-passes-through", "not-a-jwt", false},
	}
	for _, tc := range cases {
		if got := TokenMissingAudience(tc.token, DefaultKubernetesAudience); got != tc.missing {
			t.Errorf("%s: TokenMissingAudience=%v, want %v", tc.name, got, tc.missing)
		}
	}
}
