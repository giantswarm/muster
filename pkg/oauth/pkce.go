package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/oauth2"
)

const (
	// stateBytes is the number of random bytes for the OAuth state parameter.
	// 32 bytes encodes to 43 base64url characters, satisfying OAuth servers that
	// require a minimum of 32 characters.
	stateBytes = 32
)

// GeneratePKCE generates a new PKCE code verifier and challenge.
// The code verifier is 32 random bytes (256 bits), base64url-encoded.
// The code challenge is the S256 (SHA256) hash of the verifier.
//
// This implementation delegates to the standard golang.org/x/oauth2 library
// for RFC 7636 compliant PKCE generation.
//
// Returns a PKCEChallenge ready for use in an authorization request.
func GeneratePKCE() (*PKCEChallenge, error) {
	verifier, challenge := GeneratePKCERaw()

	return &PKCEChallenge{
		CodeVerifier:        verifier,
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}, nil
}

// GeneratePKCERaw generates a PKCE code verifier and challenge as raw strings.
// This is useful when you don't need the full PKCEChallenge struct.
//
// This implementation uses the standard golang.org/x/oauth2 library which
// provides RFC 7636 compliant PKCE generation.
//
// Returns the verifier and S256 challenge. This function cannot fail as it
// uses the standard library's implementation.
func GeneratePKCERaw() (verifier, challenge string) {
	// Use the standard library's GenerateVerifier which creates a
	// cryptographically secure, RFC 7636 compliant code verifier
	verifier = oauth2.GenerateVerifier()

	// Use the standard library's S256 challenge generation
	challenge = oauth2.S256ChallengeFromVerifier(verifier)

	return verifier, challenge
}

// GenerateState generates a random state parameter for OAuth.
// The state is used to prevent CSRF attacks and link the authorization
// response back to the original request.
//
// Returns a base64url-encoded random string.
func GenerateState() (string, error) {
	stateBytes := make([]byte, stateBytes)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(stateBytes), nil
}

// GenerateNonce generates a random nonce for OAuth/OIDC.
// Similar to state but typically used for ID token validation.
func GenerateNonce() (string, error) {
	return GenerateState() // Same implementation, different semantic use
}
