package oauth

import (
	pkgoauth "muster/pkg/oauth"
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
	// Delegate to shared implementation
	sharedPKCE, err := pkgoauth.GeneratePKCE()
	if err != nil {
		return nil, err
	}

	return &PKCEChallenge{
		CodeVerifier:        sharedPKCE.CodeVerifier,
		CodeChallenge:       sharedPKCE.CodeChallenge,
		CodeChallengeMethod: sharedPKCE.CodeChallengeMethod,
	}, nil
}

// GenerateState generates a random state parameter for OAuth.
// The state is used to prevent CSRF attacks and link the authorization
// response back to the original request.
func GenerateState() (string, error) {
	return pkgoauth.GenerateState()
}

// ToSharedPKCE converts to the shared PKCEChallenge type.
func (p *PKCEChallenge) ToSharedPKCE() *pkgoauth.PKCEChallenge {
	if p == nil {
		return nil
	}
	return &pkgoauth.PKCEChallenge{
		CodeVerifier:        p.CodeVerifier,
		CodeChallenge:       p.CodeChallenge,
		CodeChallengeMethod: p.CodeChallengeMethod,
	}
}
