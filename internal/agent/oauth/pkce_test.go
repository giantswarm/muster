package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestGeneratePKCE(t *testing.T) {
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE() failed: %v", err)
	}

	// Verify code verifier is non-empty
	if pkce.CodeVerifier == "" {
		t.Error("CodeVerifier is empty")
	}

	// Verify code challenge is non-empty
	if pkce.CodeChallenge == "" {
		t.Error("CodeChallenge is empty")
	}

	// Verify challenge method is S256
	if pkce.CodeChallengeMethod != "S256" {
		t.Errorf("CodeChallengeMethod = %q, want S256", pkce.CodeChallengeMethod)
	}

	// Verify the challenge is the SHA256 hash of the verifier
	hash := sha256.Sum256([]byte(pkce.CodeVerifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(hash[:])

	if pkce.CodeChallenge != expectedChallenge {
		t.Errorf("CodeChallenge verification failed.\nGot:  %q\nWant: %q", pkce.CodeChallenge, expectedChallenge)
	}
}

func TestGeneratePKCE_Uniqueness(t *testing.T) {
	// Generate multiple PKCE challenges and verify they're unique
	seen := make(map[string]bool)

	for i := 0; i < 100; i++ {
		pkce, err := GeneratePKCE()
		if err != nil {
			t.Fatalf("GeneratePKCE() failed on iteration %d: %v", i, err)
		}

		if seen[pkce.CodeVerifier] {
			t.Errorf("Duplicate code verifier generated on iteration %d", i)
		}
		seen[pkce.CodeVerifier] = true
	}
}

func TestGenerateState(t *testing.T) {
	state, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState() failed: %v", err)
	}

	// Verify state is non-empty
	if state == "" {
		t.Error("State is empty")
	}

	// Verify state length (32 bytes base64url encoded = 43 chars, must be >= 32 for OAuth servers)
	if len(state) < 32 {
		t.Errorf("State too short: %d chars (must be >= 32)", len(state))
	}
}

func TestGenerateState_Uniqueness(t *testing.T) {
	// Generate multiple states and verify they're unique
	seen := make(map[string]bool)

	for i := 0; i < 100; i++ {
		state, err := GenerateState()
		if err != nil {
			t.Fatalf("GenerateState() failed on iteration %d: %v", i, err)
		}

		if seen[state] {
			t.Errorf("Duplicate state generated on iteration %d", i)
		}
		seen[state] = true
	}
}

func TestPKCE_VerifierLength(t *testing.T) {
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE() failed: %v", err)
	}

	// OAuth 2.1 requires code_verifier to be between 43 and 128 chars
	// Our implementation uses 32 bytes = 43 base64url chars
	if len(pkce.CodeVerifier) < 43 {
		t.Errorf("CodeVerifier too short: %d chars (min 43)", len(pkce.CodeVerifier))
	}

	if len(pkce.CodeVerifier) > 128 {
		t.Errorf("CodeVerifier too long: %d chars (max 128)", len(pkce.CodeVerifier))
	}
}
