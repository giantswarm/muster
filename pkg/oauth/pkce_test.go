package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"golang.org/x/oauth2"
)

func TestGeneratePKCE(t *testing.T) {
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE() error = %v", err)
	}

	// Verify verifier is not empty and has reasonable length
	// The stdlib generates RFC 7636 compliant verifiers (43+ chars)
	if len(pkce.CodeVerifier) < 43 {
		t.Errorf("CodeVerifier length = %d, want >= 43", len(pkce.CodeVerifier))
	}

	// Verify challenge method
	if pkce.CodeChallengeMethod != "S256" {
		t.Errorf("CodeChallengeMethod = %q, want %q", pkce.CodeChallengeMethod, "S256")
	}

	// Verify challenge is correct S256 of verifier
	hash := sha256.Sum256([]byte(pkce.CodeVerifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(hash[:])
	if pkce.CodeChallenge != expectedChallenge {
		t.Errorf("CodeChallenge = %q, want %q", pkce.CodeChallenge, expectedChallenge)
	}

	// Verify our implementation matches the stdlib
	stdlibChallenge := oauth2.S256ChallengeFromVerifier(pkce.CodeVerifier)
	if pkce.CodeChallenge != stdlibChallenge {
		t.Errorf("CodeChallenge = %q, want stdlib result %q", pkce.CodeChallenge, stdlibChallenge)
	}
}

func TestGeneratePKCERaw(t *testing.T) {
	verifier, challenge := GeneratePKCERaw()

	// Verify verifier has reasonable length
	if len(verifier) < 43 {
		t.Errorf("verifier length = %d, want >= 43", len(verifier))
	}

	// Verify challenge is correct S256 of verifier
	hash := sha256.Sum256([]byte(verifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(hash[:])
	if challenge != expectedChallenge {
		t.Errorf("challenge = %q, want %q", challenge, expectedChallenge)
	}
}

func TestGeneratePKCE_Uniqueness(t *testing.T) {
	// Generate multiple PKCE challenges and ensure they're unique
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		pkce, err := GeneratePKCE()
		if err != nil {
			t.Fatalf("GeneratePKCE() error = %v", err)
		}

		if seen[pkce.CodeVerifier] {
			t.Error("Generated duplicate CodeVerifier")
		}
		seen[pkce.CodeVerifier] = true
	}
}

func TestGenerateState(t *testing.T) {
	state, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState() error = %v", err)
	}

	// Verify state length (32 bytes = 43 base64url chars)
	if len(state) != 43 {
		t.Errorf("state length = %d, want 43", len(state))
	}
}

func TestGenerateState_Uniqueness(t *testing.T) {
	// Generate multiple states and ensure they're unique
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		state, err := GenerateState()
		if err != nil {
			t.Fatalf("GenerateState() error = %v", err)
		}

		if seen[state] {
			t.Error("Generated duplicate state")
		}
		seen[state] = true
	}
}

// TestGeneratePKCE_MatchesStdlib verifies our wrapper produces the same
// output format as the standard library.
func TestGeneratePKCE_MatchesStdlib(t *testing.T) {
	// Generate using stdlib directly
	stdlibVerifier := oauth2.GenerateVerifier()
	stdlibChallenge := oauth2.S256ChallengeFromVerifier(stdlibVerifier)

	// Verify our function produces compatible output
	verifier, challenge := GeneratePKCERaw()

	// Both should have similar lengths (RFC 7636 compliant)
	if len(verifier) < 43 || len(stdlibVerifier) < 43 {
		t.Errorf("Verifiers should be >= 43 chars: ours=%d, stdlib=%d",
			len(verifier), len(stdlibVerifier))
	}

	// Verify we can compute the challenge from a stdlib verifier
	ourChallenge := oauth2.S256ChallengeFromVerifier(verifier)
	if ourChallenge != challenge {
		t.Errorf("Our challenge doesn't match stdlib computation")
	}

	// Verify stdlib can validate our challenge
	if oauth2.S256ChallengeFromVerifier(verifier) != challenge {
		t.Errorf("Stdlib cannot verify our challenge")
	}

	// Sanity check that stdlib produces valid challenge
	if len(stdlibChallenge) == 0 {
		t.Error("Stdlib challenge should not be empty")
	}
}
