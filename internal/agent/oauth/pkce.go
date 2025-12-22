package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCEChallenge represents a PKCE (Proof Key for Code Exchange) challenge.
// PKCE is required for OAuth 2.1 public clients to prevent authorization code interception.
type PKCEChallenge struct {
	// CodeVerifier is the cryptographically random string (32-96 bytes, base64url-encoded).
	// This is kept secret and never transmitted to the browser.
	CodeVerifier string

	// CodeChallenge is the SHA256 hash of the verifier (base64url-encoded).
	// This is sent in the authorization request.
	CodeChallenge string

	// CodeChallengeMethod is always "S256" for security (plain is not allowed in OAuth 2.1).
	CodeChallengeMethod string
}

// GeneratePKCE generates a new PKCE code verifier and challenge.
// The code verifier is 32 random bytes (256 bits), base64url-encoded.
// The code challenge is the S256 (SHA256) hash of the verifier.
//
// Returns a PKCEChallenge ready for use in an authorization request.
func GeneratePKCE() (*PKCEChallenge, error) {
	// Generate 32 random bytes for the code verifier
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("failed to generate random bytes for PKCE: %w", err)
	}

	// Base64url-encode the verifier (no padding, URL-safe)
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Generate the S256 challenge: SHA256(verifier), base64url-encoded
	hash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEChallenge{
		CodeVerifier:        codeVerifier,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: "S256",
	}, nil
}

// GenerateState generates a random state parameter for OAuth.
// The state is used to prevent CSRF attacks and link the authorization
// response back to the original request.
func GenerateState() (string, error) {
	stateBytes := make([]byte, 16) // 128 bits
	if _, err := rand.Read(stateBytes); err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(stateBytes), nil
}
