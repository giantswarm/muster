package oauth

import (
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// jwtParser is the shared parser. Padding is allowed because some IdPs emit
// padded base64 segments. Signature verification is never enabled here — the
// helpers in this file extract claims from tokens the caller already trusts
// (an authenticated session, never untrusted user input). Verification
// happens elsewhere via the OAuth library.
var jwtParser = jwt.NewParser(jwt.WithPaddingAllowed())

// DecodeJWTPayload returns the raw payload bytes of a JWT.
//
// Trust model: callers must guarantee the token originates from a trusted
// source. This helper exists for read-only claim extraction (cache keys,
// expiry probes, identity extraction for tracing) — never for security
// decisions.
func DecodeJWTPayload(token string) ([]byte, error) {
	if token == "" {
		return nil, fmt.Errorf("token is empty")
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid JWT format: expected at least 2 parts")
	}
	return jwtParser.DecodeSegment(parts[1])
}

// idTokenClaimsImpl mirrors IDTokenClaims but embeds jwt.RegisteredClaims so
// it satisfies jwt.Claims for parsing.
type idTokenClaimsImpl struct {
	jwt.RegisteredClaims
	Email string `json:"email,omitempty"`
}

// ParseIDTokenClaims extracts identity claims from a JWT ID token. Returns
// a zero-value struct on parse failure so callers don't have to branch on
// errors for read-only inspection.
//
// Same trust model as DecodeJWTPayload.
func ParseIDTokenClaims(idToken string) IDTokenClaims {
	var impl idTokenClaimsImpl
	_, _, _ = jwtParser.ParseUnverified(idToken, &impl)
	return IDTokenClaims{
		Subject: impl.Subject,
		Email:   impl.Email,
	}
}
