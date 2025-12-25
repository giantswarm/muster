package orchestrator

import (
	"errors"
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

func TestFormatOAuthAuthenticationError_WithStringContainingAuthRequired(t *testing.T) {
	err := errors.New("connection failed: authentication required for server")

	result := formatOAuthAuthenticationError("my-server", err)
	if result == nil {
		t.Fatal("Expected non-nil result for error containing 'authentication required'")
	}

	if !result.IsError {
		t.Error("Expected IsError to be true")
	}

	content, ok := result.Content[0].(string)
	if !ok {
		t.Fatal("Expected string content")
	}

	if !contains(content, "my-server") {
		t.Error("Expected content to contain service name")
	}
	if !contains(content, "x_my-server_authenticate") {
		t.Error("Expected content to contain authenticate tool name")
	}
}

func TestFormatOAuthAuthenticationError_WithUnrelatedError(t *testing.T) {
	err := errors.New("connection timeout")

	result := formatOAuthAuthenticationError("test-service", err)
	if result != nil {
		t.Error("Expected nil result for unrelated error")
	}
}

func TestFormatOAuthAuthenticationError_WithWrappedAuthError(t *testing.T) {
	authErr := &mcpserver.AuthRequiredError{
		URL: "https://mcp.example.com",
		Err: errors.New("401 Unauthorized"),
	}
	wrappedErr := errors.New("service start failed: " + authErr.Error())

	// This won't match errors.As because it's not properly wrapped
	// but it should match the string check
	result := formatOAuthAuthenticationError("wrapped-service", wrappedErr)
	if result == nil {
		t.Fatal("Expected non-nil result for error containing 'authentication required'")
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
