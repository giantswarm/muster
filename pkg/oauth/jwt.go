package oauth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrTokenExpMissing is returned by Expiry when a token parses successfully
// but carries no exp claim. Callers can errors.Is against it to distinguish
// "couldn't decode" from "decoded fine, no exp" without parsing error text.
var ErrTokenExpMissing = errors.New("token missing exp claim")

// jwtParser allows padded base64url segments because some IdPs emit them.
// Signature verification is intentionally not configured: helpers in this
// file extract claims from tokens the caller already trusts (an authenticated
// session or a token-exchange response). Verification belongs to the OAuth
// library and downstream resource servers.
var jwtParser = jwt.NewParser(jwt.WithPaddingAllowed())

// tokenClaims is the parsing target shared by every accessor in this file.
// It embeds RegisteredClaims for sub/exp/iss and adds Email for OIDC ID
// tokens. Using one struct keeps the accessors symmetrical.
type tokenClaims struct {
	jwt.RegisteredClaims
	Email         string `json:"email,omitempty"`
	EmailVerified bool   `json:"email_verified,omitempty"`
}

func parseUnverified(token string) (tokenClaims, error) {
	var c tokenClaims
	_, _, err := jwtParser.ParseUnverified(token, &c)
	return c, err
}

// Subject returns the sub claim of a trusted JWT. Returns "" with a wrapped
// error on decode failure; returns ("", nil) when the token parses but has
// no sub claim.
func Subject(token string) (string, error) {
	c, err := parseUnverified(token)
	if err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	return c.Subject, nil
}

// Email returns the email claim of a trusted ID token. Returns "" with a
// wrapped error on decode failure; returns ("", nil) when the token parses
// but has no email claim. Intended for OIDC ID tokens; access tokens
// typically don't carry an email claim.
func Email(token string) (string, error) {
	c, err := parseUnverified(token)
	if err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	return c.Email, nil
}

// EmailVerified returns the email_verified claim of a trusted ID token. Returns
// false with a wrapped error on decode failure; returns (false, nil) when the
// token parses but has no email_verified claim.
func EmailVerified(token string) (bool, error) {
	c, err := parseUnverified(token)
	if err != nil {
		return false, fmt.Errorf("decode token: %w", err)
	}
	return c.EmailVerified, nil
}

// Expiry returns the exp claim of a trusted JWT. Returns ErrTokenExpMissing
// when the token parses but has no exp; wraps the underlying decode error
// otherwise.
func Expiry(token string) (time.Time, error) {
	c, err := parseUnverified(token)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode token: %w", err)
	}
	if c.ExpiresAt == nil {
		return time.Time{}, ErrTokenExpMissing
	}
	return c.ExpiresAt.Time, nil
}

// Issuer returns the iss claim of a trusted JWT. Returns ("", nil) when the
// token parses but carries no iss; returns a wrapped error on decode failure.
func Issuer(token string) (string, error) {
	c, err := parseUnverified(token)
	if err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	return c.Issuer, nil
}

// IsExpired reports whether a trusted JWT's exp claim is in the past or
// within DefaultExpiryMargin of now. Returns (true, nil) when the token
// parses and is actually past expiry; returns (true, err) when the token
// is unparseable or has no exp — callers should treat both as unusable but
// can use the error to distinguish them. Mirrors Token.IsExpired for
// raw-string JWTs.
func IsExpired(token string) (bool, error) {
	exp, err := Expiry(token)
	if err != nil {
		return true, err
	}
	return time.Now().Add(DefaultExpiryMargin).After(exp), nil
}
