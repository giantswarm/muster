package orchestrator

import (
	"errors"
	"fmt"
	"testing"

	"muster/internal/mcpserver"
)

func TestFormatOAuthAuthenticationError_WithAuthRequiredError(t *testing.T) {
	authErr := &mcpserver.AuthRequiredError{
		URL: "https://mcp.example.com",
		AuthInfo: mcpserver.AuthInfo{
			Issuer: "https://auth.example.com",
			Scope:  "openid",
		},
		Err: errors.New("server returned 401"),
	}

	result := formatOAuthAuthenticationError("test-service", authErr)
	if result == nil {
		t.Fatal("Expected non-nil result for AuthRequiredError")
	}

	if !result.IsError {
		t.Error("Expected IsError to be true")
	}

	content, ok := result.Content[0].(string)
	if !ok {
		t.Fatal("Expected string content")
	}

	// Check that the error message contains expected parts
	if !contains(content, "test-service") {
		t.Error("Expected content to contain service name")
	}
	if !contains(content, "x_test-service_authenticate") {
		t.Error("Expected content to contain authenticate tool name")
	}
	if !contains(content, "OAuth authentication") {
		t.Error("Expected content to mention OAuth authentication")
	}
}

func TestFormatOAuthAuthenticationError_WithPlainStringError(t *testing.T) {
	// ADR-008: We no longer use string matching for auth detection.
	// Only structured AuthRequiredError should be recognized.
	err := errors.New("connection failed: authentication required for server")

	result := formatOAuthAuthenticationError("my-server", err)
	if result != nil {
		t.Error("Expected nil result for plain string error - we only detect structured AuthRequiredError")
	}
}

func TestFormatOAuthAuthenticationError_WithUnrelatedError(t *testing.T) {
	err := errors.New("connection timeout")

	result := formatOAuthAuthenticationError("test-service", err)
	if result != nil {
		t.Error("Expected nil result for unrelated error")
	}
}

func TestFormatOAuthAuthenticationError_WithProperlyWrappedAuthError(t *testing.T) {
	// ADR-008: Proper error wrapping with fmt.Errorf %w should work
	authErr := &mcpserver.AuthRequiredError{
		URL: "https://mcp.example.com",
		Err: errors.New("401 Unauthorized"),
	}
	wrappedErr := fmt.Errorf("service start failed: %w", authErr)

	// This should match because errors.As can unwrap the error chain
	result := formatOAuthAuthenticationError("wrapped-service", wrappedErr)
	if result == nil {
		t.Fatal("Expected non-nil result for properly wrapped AuthRequiredError")
	}

	if !result.IsError {
		t.Error("Expected IsError to be true")
	}
}

func TestFormatOAuthAuthenticationError_WithStringConcatError(t *testing.T) {
	// ADR-008: String concatenation does not preserve error chain
	authErr := &mcpserver.AuthRequiredError{
		URL: "https://mcp.example.com",
		Err: errors.New("401 Unauthorized"),
	}
	// This uses string concat, not proper wrapping
	stringErr := errors.New("service start failed: " + authErr.Error())

	// This should NOT match because errors.As cannot unwrap string-concatenated errors
	result := formatOAuthAuthenticationError("wrapped-service", stringErr)
	if result != nil {
		t.Error("Expected nil result for string-concatenated error - not properly wrapped")
	}
}

// contains is a helper to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
