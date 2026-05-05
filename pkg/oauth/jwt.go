package oauth

import (
	"fmt"
	"log/slog"
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
	return DecodeJWTSegment(parts[1])
}

// DecodeJWTSegment base64url-decodes a single JWT segment (header, payload,
// or signature). Padded and unpadded variants are both accepted. Use this
// when the caller is iterating segments — DecodeJWTPayload covers the common
// "give me the payload of this token" path.
func DecodeJWTSegment(seg string) ([]byte, error) {
	return jwtParser.DecodeSegment(seg)
}

// idTokenClaimsImpl mirrors IDTokenClaims but embeds jwt.RegisteredClaims so
// it satisfies jwt.Claims for parsing.
type idTokenClaimsImpl struct {
	jwt.RegisteredClaims
	Email string `json:"email,omitempty"`
}

// ParseIDTokenClaims extracts identity claims from a JWT ID token. Returns
// a zero-value struct on parse failure so callers don't have to branch on
// errors for read-only inspection. Failures are logged at debug level so
// observability is preserved when "why is the email empty?" comes up.
//
// Same trust model as DecodeJWTPayload.
func ParseIDTokenClaims(idToken string) IDTokenClaims {
	var impl idTokenClaimsImpl
	if _, _, err := jwtParser.ParseUnverified(idToken, &impl); err != nil {
		slog.Debug("ParseIDTokenClaims: parse failed, returning zero claims", "err", err)
		return IDTokenClaims{}
	}
	return IDTokenClaims{
		Subject: impl.Subject,
		Email:   impl.Email,
	}
}
