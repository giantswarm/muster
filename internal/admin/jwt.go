package admin

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// errMalformedJWT is returned when a token doesn't have exactly three
// dot-separated segments. JWTs that deviate are rejected outright; we never
// attempt to decode ad-hoc formats.
var errMalformedJWT = errors.New("malformed JWT: expected 3 segments")

// DecodeJWT parses a compact JWT and returns the header and payload as raw
// JSON. The signature segment is intentionally discarded — this function
// exists specifically so that the admin UI never has to touch the bearer
// credential.
//
// The returned DecodedJWT always carries the supplied label. If decoding
// fails, Error is populated and Header/Payload may be nil — callers still
// render the label so the user sees *which* token failed.
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

// decodeSegment base64url-decodes a JWT segment and canonicalises the JSON
// so templates render with stable formatting. Both padded and unpadded
// base64url inputs are accepted because producers vary.
func decodeSegment(seg string) (json.RawMessage, error) {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		// Some producers include padding — try the padded variant as a fallback.
		if b2, err2 := base64.URLEncoding.DecodeString(seg); err2 == nil {
			b = b2
		} else {
			return nil, fmt.Errorf("base64 decode: %w", err)
		}
	}

	// Validate it parses as JSON, then re-indent for readable display.
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
