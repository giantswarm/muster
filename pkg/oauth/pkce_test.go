package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestGeneratePKCE(t *testing.T) {
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE() error = %v", err)
	}

	// Verify verifier length (32 bytes = 43 base64url chars)
	if len(pkce.CodeVerifier) != 43 {
		t.Errorf("CodeVerifier length = %d, want 43", len(pkce.CodeVerifier))
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
}

func TestGeneratePKCERaw(t *testing.T) {
	verifier, challenge, err := GeneratePKCERaw()
	if err != nil {
		t.Fatalf("GeneratePKCERaw() error = %v", err)
	}

	// Verify verifier length
	if len(verifier) != 43 {
		t.Errorf("verifier length = %d, want 43", len(verifier))
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
