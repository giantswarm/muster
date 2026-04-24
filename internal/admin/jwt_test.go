package admin

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// buildJWT crafts a compact JWT from raw header/payload/signature bytes.
// Signature content is irrelevant — DecodeJWT must never read it.
func buildJWT(t *testing.T, header, payload string, signature []byte) string {
	t.Helper()
	return strings.Join([]string{
		base64.RawURLEncoding.EncodeToString([]byte(header)),
		base64.RawURLEncoding.EncodeToString([]byte(payload)),
		base64.RawURLEncoding.EncodeToString(signature),
	}, ".")
}

func TestDecodeJWT_happyPath(t *testing.T) {
	token := buildJWT(t,
		`{"alg":"RS256","kid":"abc","typ":"JWT"}`,
		`{"sub":"pau@giantswarm.io","iss":"https://dex","exp":1745314654}`,
		[]byte("this-is-the-signature-it-must-not-appear"),
	)

	got := DecodeJWT("muster → github", token)
	if got.Error != "" {
		t.Fatalf("unexpected decode error: %s", got.Error)
	}
	if got.Label != "muster → github" {
		t.Fatalf("label lost: %q", got.Label)
	}

	// Header
	var hdr map[string]string
	if err := json.Unmarshal(got.Header, &hdr); err != nil {
		t.Fatalf("header not JSON: %v", err)
	}
	if hdr["alg"] != "RS256" || hdr["kid"] != "abc" {
		t.Fatalf("header claims mismatch: %+v", hdr)
	}

	// Payload
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload["sub"] != "pau@giantswarm.io" {
		t.Fatalf("sub claim mismatch: %+v", payload)
	}

	// Signature must not leak into any returned field.
	combined := string(got.Header) + string(got.Payload) + got.Error + got.Label
	if strings.Contains(combined, "this-is-the-signature-it-must-not-appear") {
		t.Fatalf("signature leaked into decoded output")
	}
}

func TestDecodeJWT_emptyToken(t *testing.T) {
	got := DecodeJWT("muster → x", "")
	if got.Error == "" {
		t.Fatal("expected error for empty token")
	}
	if got.Header != nil || got.Payload != nil {
		t.Fatal("header/payload must be nil on error")
	}
	if got.Label != "muster → x" {
		t.Fatalf("label must survive error path: %q", got.Label)
	}
}

func TestDecodeJWT_malformed(t *testing.T) {
	cases := []string{
		"one-segment-only",
		"a.b",
		"a.b.c.d",
	}
	for _, in := range cases {
		got := DecodeJWT("t", in)
		if got.Error == "" {
			t.Fatalf("expected error for malformed token %q", in)
		}
		if !strings.Contains(got.Error, "3 segments") {
			t.Fatalf("unexpected error text for %q: %s", in, got.Error)
		}
	}
}

func TestDecodeJWT_invalidBase64(t *testing.T) {
	// Header is not valid base64url.
	got := DecodeJWT("t", "!!!.payload.sig")
	if got.Error == "" {
		t.Fatal("expected error for invalid base64 header")
	}
}

func TestDecodeJWT_nonJSONPayload(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("not-json-at-all")) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("sig"))

	got := DecodeJWT("t", token)
	if got.Error == "" {
		t.Fatal("expected error when payload is not JSON")
	}
	if !strings.Contains(got.Error, "payload") {
		t.Fatalf("error should mention payload segment: %s", got.Error)
	}
}

func TestDecodeJWT_paddedBase64Fallback(t *testing.T) {
	// Producers sometimes include padding; both variants must work.
	header := base64.URLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := base64.URLEncoding.EncodeToString([]byte(`{"sub":"x"}`))
	sig := base64.URLEncoding.EncodeToString([]byte("sig"))
	token := header + "." + payload + "." + sig

	got := DecodeJWT("t", token)
	if got.Error != "" {
		t.Fatalf("padded base64 must decode: %s", got.Error)
	}
}

func TestExtractEmailFromIDToken(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{
			name:     "empty token",
			token:    "",
			expected: "",
		},
		{
			name:     "malformed token",
			token:    "invalid.token",
			expected: "",
		},
		{
			name:     "valid token with email",
			token:    createTestJWT(map[string]interface{}{"sub": "user123", "email": "test@example.com"}),
			expected: "test@example.com",
		},
		{
			name:     "valid token without email",
			token:    createTestJWT(map[string]interface{}{"sub": "user123", "name": "Test User"}),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractEmailFromIDToken(tt.token)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// createTestJWT creates a simple JWT for testing purposes
func createTestJWT(payload map[string]interface{}) string {
	header := map[string]interface{}{
		"alg": "RS256",
		"typ": "JWT",
	}

	headerBytes, _ := json.Marshal(header)
	payloadBytes, _ := json.Marshal(payload)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerBytes)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	// Create a dummy signature for testing
	signature := base64.RawURLEncoding.EncodeToString([]byte("dummy-signature"))

	return headerB64 + "." + payloadB64 + "." + signature
}
