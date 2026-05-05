package admin

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// jwtSegmentDecoder base64url-decodes JWT segments for the operator-debug
// UI. Configuration intentionally mirrors pkg/oauth/jwt.go's parser; the two
// adapters are kept independent because their use cases (typed claim
// extraction vs. raw segment display for diagnostics) differ.
var jwtSegmentDecoder = jwt.NewParser(jwt.WithPaddingAllowed())

// DecodeJWT renders a JWT's header and payload as pretty-printed JSON for
// display. The signature segment is dropped — this is a diagnostic helper
// for trusted contexts (operators inspecting stored tokens), never a
// verification path. Accepts 2- and 3-part tokens; the admin UI is
// deliberately lenient because operators routinely paste truncated tokens
// from logs.
func DecodeJWT(label, raw string) *DecodedJWT {
	out := &DecodedJWT{Label: label}
	if raw == "" {
		out.Error = "no token stored"
		return out
	}

	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		out.Error = "malformed JWT: need at least header.payload"
		return out
	}

	header, err := decodeAndPretty(parts[0])
	if err != nil {
		out.Error = fmt.Sprintf("header: %v", err)
		return out
	}
	payload, err := decodeAndPretty(parts[1])
	if err != nil {
		out.Error = fmt.Sprintf("payload: %v", err)
		return out
	}
	out.Header = header
	out.Payload = payload
	return out
}

func decodeAndPretty(seg string) (json.RawMessage, error) {
	b, err := jwtSegmentDecoder.DecodeSegment(seg)
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
