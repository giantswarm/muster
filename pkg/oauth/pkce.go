package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const (
	// pkceVerifierBytes is the number of random bytes for the PKCE code verifier.
	// 32 bytes provides 256 bits of entropy, which is recommended for security.
	pkceVerifierBytes = 32

	// stateBytes is the number of random bytes for the OAuth state parameter.
	// 32 bytes encodes to 43 base64url characters, satisfying OAuth servers that
	// require a minimum of 32 characters.
	stateBytes = 32
)

// GeneratePKCE generates a new PKCE code verifier and challenge.
// The code verifier is 32 random bytes (256 bits), base64url-encoded.
// The code challenge is the S256 (SHA256) hash of the verifier.
//
// Returns a PKCEChallenge ready for use in an authorization request.
func GeneratePKCE() (*PKCEChallenge, error) {
	verifier, challenge, err := GeneratePKCERaw()
	if err != nil {
		return nil, err
	}

	return &PKCEChallenge{
		CodeVerifier:        verifier,
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}, nil
}

// GeneratePKCERaw generates a PKCE code verifier and challenge as raw strings.
// This is useful when you don't need the full PKCEChallenge struct.
//
// Returns the verifier and S256 challenge.
func GeneratePKCERaw() (verifier, challenge string, err error) {
	// Generate 32 random bytes for the code verifier
	verifierBytes := make([]byte, pkceVerifierBytes)
	if _, err := rand.Read(verifierBytes); err != nil {
		return "", "", fmt.Errorf("failed to generate random bytes for PKCE: %w", err)
	}

	// Base64url-encode the verifier (no padding, URL-safe)
	verifier = base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Generate the S256 challenge: SHA256(verifier), base64url-encoded
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])

	return verifier, challenge, nil
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
