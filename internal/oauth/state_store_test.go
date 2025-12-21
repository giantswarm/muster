package oauth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestStateStore_GenerateAndValidate(t *testing.T) {
	ss := NewStateStore()
	defer ss.Stop()

	sessionID := "session-123"
	serverName := "mcp-kubernetes"
	issuer := "https://auth.example.com"
	codeVerifier := "test-code-verifier-abc123"

	// Generate state
	encodedState, err := ss.GenerateState(sessionID, serverName, issuer, codeVerifier)
	if err != nil {
		t.Fatalf("Failed to generate state: %v", err)
	}

	if encodedState == "" {
		t.Error("Expected non-empty encoded state")
	}

	// Validate state
	state := ss.ValidateState(encodedState)
	if state == nil {
		t.Fatal("Expected valid state, got nil")
	}

	// Verify state contents
	if state.SessionID != sessionID {
		t.Errorf("Expected session ID %q, got %q", sessionID, state.SessionID)
	}

	if state.ServerName != serverName {
		t.Errorf("Expected server name %q, got %q", serverName, state.ServerName)
	}

	if state.Issuer != issuer {
		t.Errorf("Expected issuer %q, got %q", issuer, state.Issuer)
	}

	if state.CodeVerifier != codeVerifier {
		t.Errorf("Expected code verifier %q, got %q", codeVerifier, state.CodeVerifier)
	}

	// Verify nonce is non-empty
	if state.Nonce == "" {
		t.Error("Expected non-empty nonce in validated state")
	}
}

func TestStateStore_ValidateRemovesState(t *testing.T) {
	ss := NewStateStore()
	defer ss.Stop()

	encodedState, err := ss.GenerateState("session", "server", "issuer", "verifier")
	if err != nil {
		t.Fatalf("Failed to generate state: %v", err)
	}

	// First validation should succeed
	state := ss.ValidateState(encodedState)
	if state == nil {
		t.Fatal("First validation should succeed")
	}

	// Second validation should fail (state was consumed)
	state = ss.ValidateState(encodedState)
	if state != nil {
		t.Error("Second validation should fail (state already consumed)")
	}
}

func TestStateStore_ValidateInvalidState(t *testing.T) {
	ss := NewStateStore()
	defer ss.Stop()

	// Test with empty string
	if ss.ValidateState("") != nil {
		t.Error("Empty state should return nil")
	}

	// Test with invalid base64
	if ss.ValidateState("not-valid-base64!!!") != nil {
		t.Error("Invalid base64 should return nil")
	}

	// Test with valid base64 but invalid JSON
	invalidJSON := base64.URLEncoding.EncodeToString([]byte("not json"))
	if ss.ValidateState(invalidJSON) != nil {
		t.Error("Invalid JSON should return nil")
	}

	// Test with valid JSON but non-existent nonce
	fakeState := OAuthState{
		SessionID:  "session",
		ServerName: "server",
		Nonce:      "non-existent-nonce",
		CreatedAt:  time.Now(),
		Issuer:     "issuer",
	}
	fakeJSON, _ := json.Marshal(fakeState)
	fakeEncoded := base64.URLEncoding.EncodeToString(fakeJSON)
	if ss.ValidateState(fakeEncoded) != nil {
		t.Error("Non-existent nonce should return nil")
	}
}

func TestStateStore_CodeVerifierNotInEncodedState(t *testing.T) {
	ss := NewStateStore()
	defer ss.Stop()

	codeVerifier := "super-secret-verifier"

	encodedState, err := ss.GenerateState("session", "server", "issuer", codeVerifier)
	if err != nil {
		t.Fatalf("Failed to generate state: %v", err)
	}

	// Decode the state to verify CodeVerifier is not included
	stateJSON, err := base64.URLEncoding.DecodeString(encodedState)
	if err != nil {
		t.Fatalf("Failed to decode state: %v", err)
	}

	// The encoded state should NOT contain the code verifier
	// because it uses json:"-" tag
	var decoded map[string]interface{}
	if err := json.Unmarshal(stateJSON, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal state JSON: %v", err)
	}

	if _, exists := decoded["code_verifier"]; exists {
		t.Error("Code verifier should NOT be included in encoded state (it's sensitive)")
	}

	// But when we validate, the code verifier should be retrieved from storage
	state := ss.ValidateState(encodedState)
	if state == nil {
		t.Fatal("Validation should succeed")
	}

	if state.CodeVerifier != codeVerifier {
		t.Errorf("Code verifier should be retrieved from storage, expected %q, got %q",
			codeVerifier, state.CodeVerifier)
	}
}

func TestStateStore_Delete(t *testing.T) {
	ss := NewStateStore()
	defer ss.Stop()

	encodedState, err := ss.GenerateState("session", "server", "issuer", "verifier")
	if err != nil {
		t.Fatalf("Failed to generate state: %v", err)
	}

	// Validate state to get the nonce
	state := ss.ValidateState(encodedState)
	if state == nil {
		t.Fatal("Expected valid state")
	}

	// Regenerate a new state for the delete test
	encodedState2, err := ss.GenerateState("session", "server", "issuer", "verifier")
	if err != nil {
		t.Fatalf("Failed to generate state: %v", err)
	}

	// Decode to get the nonce
	stateJSON, _ := base64.URLEncoding.DecodeString(encodedState2)
	var decodedState OAuthState
	json.Unmarshal(stateJSON, &decodedState)
	nonce := decodedState.Nonce

	// Delete the state by nonce
	ss.Delete(nonce)

	// Validation should now fail
	if ss.ValidateState(encodedState2) != nil {
		t.Error("Deleted state should not be retrievable")
	}
}

func TestStateStore_UniqueNonces(t *testing.T) {
	ss := NewStateStore()
	defer ss.Stop()

	nonces := make(map[string]bool)

	// Generate multiple states and verify nonces are unique
	for i := 0; i < 100; i++ {
		encodedState, err := ss.GenerateState("session", "server", "issuer", "verifier")
		if err != nil {
			t.Fatalf("Failed to generate state: %v", err)
		}

		// Decode to get the nonce
		stateJSON, _ := base64.URLEncoding.DecodeString(encodedState)
		var state OAuthState
		json.Unmarshal(stateJSON, &state)
		nonce := state.Nonce

		if nonces[nonce] {
			t.Errorf("Duplicate nonce generated: %s", nonce)
		}
		nonces[nonce] = true
	}
}
