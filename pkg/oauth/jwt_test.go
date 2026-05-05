package oauth

import (
	"encoding/base64"
	"strings"
	"testing"
)

const samplePayloadJSON = `{"sub":"alice","email":"alice@example.com","iss":"https://issuer.example","exp":9999999999}`

// jwtFromPayload builds a fake JWT (header.payload.signature) using the given
// base64 encoder for the payload. The header and signature segments are static
// raw-url-encoded strings — only the payload encoding varies.
func jwtFromPayload(t *testing.T, enc *base64.Encoding) string {
	t.Helper()
	return "eyJhbGciOiJSUzI1NiJ9." + enc.EncodeToString([]byte(samplePayloadJSON)) + ".sig"
}

func TestDecodeJWTPayload(t *testing.T) {
	t.Run("returns error for empty token", func(t *testing.T) {
		_, err := DecodeJWTPayload("")
		if err == nil || !strings.Contains(err.Error(), "token is empty") {
			t.Fatalf("expected 'token is empty' error, got %v", err)
		}
	})

	t.Run("returns error for invalid JWT format", func(t *testing.T) {
		_, err := DecodeJWTPayload("not-a-jwt")
		if err == nil || !strings.Contains(err.Error(), "invalid JWT format") {
			t.Fatalf("expected 'invalid JWT format' error, got %v", err)
		}
	})

	t.Run("decodes valid JWT payload", func(t *testing.T) {
		// Spec form: unpadded base64url.
		token := jwtFromPayload(t, base64.RawURLEncoding)
		decoded, err := DecodeJWTPayload(token)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(decoded) != samplePayloadJSON {
			t.Errorf("payload mismatch:\n got: %s\nwant: %s", decoded, samplePayloadJSON)
		}
	})

	t.Run("handles token with only two parts", func(t *testing.T) {
		// Some tools strip the signature for read-only inspection.
		token := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(samplePayloadJSON))
		decoded, err := DecodeJWTPayload(token)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(decoded) != samplePayloadJSON {
			t.Errorf("payload mismatch")
		}
	})

	encs := []struct {
		name string
		enc  *base64.Encoding
	}{
		{"RawURLEncoding (RFC 7515 spec form)", base64.RawURLEncoding},
		{"URLEncoding (padded url-safe)", base64.URLEncoding},
		{"RawStdEncoding (unpadded standard alphabet)", base64.RawStdEncoding},
		{"StdEncoding (padded standard alphabet)", base64.StdEncoding},
	}
	for _, tc := range encs {
		t.Run("accepts payload encoded with "+tc.name, func(t *testing.T) {
			token := jwtFromPayload(t, tc.enc)
			decoded, err := DecodeJWTPayload(token)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(decoded) != samplePayloadJSON {
				t.Errorf("payload mismatch for %s:\n got: %s\nwant: %s", tc.name, decoded, samplePayloadJSON)
			}
		})
	}

	t.Run("returns error when no base64 variant matches", func(t *testing.T) {
		// Force a payload that's not valid in any base64 alphabet.
		_, err := DecodeJWTPayload("header.!!!not-base64!!!.sig")
		if err == nil {
			t.Fatal("expected error for un-decodable payload")
		}
	})
}

func TestParseIDTokenClaims(t *testing.T) {
	t.Run("returns zero claims on empty token", func(t *testing.T) {
		claims := ParseIDTokenClaims("")
		if claims.Subject != "" || claims.Email != "" {
			t.Errorf("expected zero claims, got %+v", claims)
		}
	})

	t.Run("returns zero claims on malformed JWT", func(t *testing.T) {
		// Matches the original CLI parseIDTokenClaims behavior: any parse
		// failure yields zero claims, no panic.
		claims := ParseIDTokenClaims("not.a.jwt")
		if claims.Subject != "" || claims.Email != "" {
			t.Errorf("expected zero claims, got %+v", claims)
		}
	})

	t.Run("extracts sub and email", func(t *testing.T) {
		token := jwtFromPayload(t, base64.RawURLEncoding)
		claims := ParseIDTokenClaims(token)
		if claims.Subject != "alice" {
			t.Errorf("Subject = %q, want %q", claims.Subject, "alice")
		}
		if claims.Email != "alice@example.com" {
			t.Errorf("Email = %q, want %q", claims.Email, "alice@example.com")
		}
	})
}

// TestDecodeJWTPayload_Combinations exercises every combination of part-count
// and base64 variant the helper must accept.
func TestDecodeJWTPayload_Combinations(t *testing.T) {
	cases := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{
			name:  "2-part token + unpadded url-safe payload",
			token: "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(samplePayloadJSON)),
		},
		{
			name:  "3-part token + unpadded standard payload",
			token: "eyJhbGciOiJSUzI1NiJ9." + base64.RawStdEncoding.EncodeToString([]byte(samplePayloadJSON)) + ".sig",
		},
		{
			name:  "3-part token + unpadded url-safe payload",
			token: "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(samplePayloadJSON)) + ".sig",
		},
		{
			name:  "3-part token + padded url-safe payload",
			token: "eyJhbGciOiJSUzI1NiJ9." + base64.URLEncoding.EncodeToString([]byte(samplePayloadJSON)) + ".sig",
		},
		{
			name:    "rejects empty",
			token:   "",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := DecodeJWTPayload(tc.token)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(decoded) != samplePayloadJSON {
				t.Errorf("payload mismatch:\n got: %s\nwant: %s", decoded, samplePayloadJSON)
			}
		})
	}
}
