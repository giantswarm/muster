package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

var errMalformedJWT = errors.New("malformed JWT: expected 3 segments")

// DecodeJWT parses a compact JWT into header + payload JSON, deliberately
// discarding the signature segment so the admin UI never has to touch the
// bearer credential. On decode failure, Error is set and the caller still
// gets the label so the user can see *which* token failed.
func DecodeJWT(label, raw string) *DecodedJWT {
	out := &DecodedJWT{Label: label}
	if raw == "" {
		out.Error = "no token stored"
		return out
	}

	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		out.Error = errMalformedJWT.Error()
		return out
	}

	header, err := decodeSegment(parts[0])
	if err != nil {
		out.Error = fmt.Sprintf("header: %v", err)
		return out
	}
	payload, err := decodeSegment(parts[1])
	if err != nil {
		out.Error = fmt.Sprintf("payload: %v", err)
		return out
	}
	// parts[2] (signature) is deliberately dropped.

	out.Header = header
	out.Payload = payload
	return out
}

// ExtractEmailFromIDToken extracts the email claim from a JWT ID token.
// Returns empty string if the token is invalid or doesn't contain an email claim.
func ExtractEmailFromIDToken(idToken string) string {
	return pkgoauth.ParseIDTokenClaims(idToken).Email
}

// decodeSegment decodes a JWT segment (header or payload) and re-indents the
// JSON for display in the admin UI.
func decodeSegment(seg string) (json.RawMessage, error) {
	b, err := pkgoauth.DecodeJWTSegment(seg)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("json parse: %w", err)
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("json re-encode: %w", err)
	}
	return pretty, nil
}
