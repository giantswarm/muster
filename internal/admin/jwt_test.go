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

	var hdr map[string]string
	if err := json.Unmarshal(got.Header, &hdr); err != nil {
		t.Fatalf("header not JSON: %v", err)
	}
	if hdr["alg"] != "RS256" || hdr["kid"] != "abc" {
		t.Fatalf("header claims mismatch: %+v", hdr)
	}

	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload["sub"] != "pau@giantswarm.io" {
		t.Fatalf("sub claim mismatch: %+v", payload)
	}

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

func TestDecodeJWT_acceptsTwoPartToken(t *testing.T) {
	// Operators routinely paste truncated tokens from logs (signature stripped).
	// The admin UI must render them.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`))
	got := DecodeJWT("t", header+"."+payload)
	if got.Error != "" {
		t.Fatalf("2-part token must decode: %s", got.Error)
	}
	if len(got.Header) == 0 || len(got.Payload) == 0 {
		t.Fatal("expected both segments populated")
	}
}

func TestDecodeJWT_malformed(t *testing.T) {
	cases := []string{
		"one-segment-only",
	}
	for _, in := range cases {
		got := DecodeJWT("t", in)
		if got.Error == "" {
			t.Fatalf("expected error for malformed token %q", in)
		}
		if !strings.Contains(got.Error, "header.payload") {
			t.Fatalf("unexpected error text for %q: %s", in, got.Error)
		}
	}
}

func TestDecodeJWT_invalidBase64(t *testing.T) {
	got := DecodeJWT("t", "!!!.payload.sig")
	if got.Error == "" {
		t.Fatal("expected error for invalid base64 header")
	}
	if !strings.Contains(got.Error, "header") {
		t.Fatalf("error should mention header segment: %s", got.Error)
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

func TestDecodeJWT_paddedBase64(t *testing.T) {
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

func TestDecodeJWT_dropsSignature(t *testing.T) {
	// Sanity: a 4-part input should still produce header+payload from the
	// first two segments. The trailing junk is ignored.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`))
	got := DecodeJWT("t", header+"."+payload+".sig.extra")
	if got.Error != "" {
		t.Fatalf("4-segment input should still parse first two: %s", got.Error)
	}
}
