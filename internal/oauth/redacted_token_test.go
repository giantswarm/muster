package oauth

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestRedactedToken_String(t *testing.T) {
	token := NewRedactedToken("super-secret-token-12345")

	// String() should return [REDACTED]
	if token.String() != "[REDACTED]" {
		t.Errorf("Expected [REDACTED], got %s", token.String())
	}

	// Value() should return the actual token
	if token.Value() != "super-secret-token-12345" {
		t.Errorf("Expected actual token, got %s", token.Value())
	}
}

func TestRedactedToken_GoString(t *testing.T) {
	token := NewRedactedToken("secret")

	// GoString should also redact
	expected := "oauth.RedactedToken{[REDACTED]}"
	if token.GoString() != expected {
		t.Errorf("Expected %s, got %s", expected, token.GoString())
	}
}

func TestRedactedToken_Printf(t *testing.T) {
	token := NewRedactedToken("my-secret-token")

	// %s format should use String()
	result := fmt.Sprintf("Token: %s", token)
	if result != "Token: [REDACTED]" {
		t.Errorf("Expected 'Token: [REDACTED]', got %s", result)
	}

	// %v format should also use String()
	result = fmt.Sprintf("Token: %v", token)
	if result != "Token: [REDACTED]" {
		t.Errorf("Expected 'Token: [REDACTED]', got %s", result)
	}

	// %#v format should use GoString()
	result = fmt.Sprintf("Token: %#v", token)
	if result != "Token: oauth.RedactedToken{[REDACTED]}" {
		t.Errorf("Expected 'Token: oauth.RedactedToken{[REDACTED]}', got %s", result)
	}
}

func TestRedactedToken_IsEmpty(t *testing.T) {
	emptyToken := NewRedactedToken("")
	if !emptyToken.IsEmpty() {
		t.Error("Expected empty token to return true for IsEmpty()")
	}

	nonEmptyToken := NewRedactedToken("value")
	if nonEmptyToken.IsEmpty() {
		t.Error("Expected non-empty token to return false for IsEmpty()")
	}
}

func TestRedactedToken_MarshalJSON(t *testing.T) {
	token := NewRedactedToken("secret-value")

	data, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if string(data) != `"[REDACTED]"` {
		t.Errorf("Expected \"[REDACTED]\", got %s", string(data))
	}
}

func TestRedactedToken_MarshalText(t *testing.T) {
	token := NewRedactedToken("secret-value")

	data, err := token.MarshalText()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if string(data) != "[REDACTED]" {
		t.Errorf("Expected [REDACTED], got %s", string(data))
	}
}

func TestRedactedToken_InStruct(t *testing.T) {
	type Request struct {
		Token RedactedToken `json:"token"`
		Name  string        `json:"name"`
	}

	req := Request{
		Token: NewRedactedToken("secret-token"),
		Name:  "test",
	}

	// JSON marshaling should redact the token
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	expected := `{"token":"[REDACTED]","name":"test"}`
	if string(data) != expected {
		t.Errorf("Expected %s, got %s", expected, string(data))
	}

	// fmt.Sprintf should also redact
	result := fmt.Sprintf("%+v", req)
	if result != "{Token:[REDACTED] Name:test}" {
		t.Errorf("Expected redacted output, got %s", result)
	}
}

func TestRedactedToken_InError(t *testing.T) {
	token := NewRedactedToken("secret-value")

	// Creating an error with the token should show [REDACTED]
	err := fmt.Errorf("failed with token: %s", token)
	if err.Error() != "failed with token: [REDACTED]" {
		t.Errorf("Expected redacted error, got %s", err.Error())
	}
}

