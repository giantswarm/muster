package oauth

import (
	"errors"
	"testing"
)

func TestIsTokenExpiredError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "401 error",
			err:      errors.New("request failed with status 401: something"),
			expected: true,
		},
		{
			name:     "invalid_token error",
			err:      errors.New(`{"error":"invalid_token","error_description":"Token validation failed"}`),
			expected: true,
		},
		{
			name:     "Token validation failed",
			err:      errors.New("Token validation failed"),
			expected: true,
		},
		{
			name:     "token expired error",
			err:      errors.New("the access token has expired"),
			expected: true,
		},
		{
			name:     "unauthorized error",
			err:      errors.New("unauthorized access"),
			expected: true,
		},
		{
			name:     "transport error with 401",
			err:      errors.New(`transport error: request failed with status 401: {"error":"invalid_token","error_description":"Token validation failed"}`),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      errors.New("connection refused"),
			expected: false,
		},
		{
			name:     "timeout error",
			err:      errors.New("request timeout"),
			expected: false,
		},
		{
			name:     "400 error (not 401)",
			err:      errors.New("request failed with status 400: bad request"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTokenExpiredError(tt.err)
			if result != tt.expected {
				t.Errorf("IsTokenExpiredError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestTokenExpiredError(t *testing.T) {
	t.Run("error with auth URL", func(t *testing.T) {
		originalErr := errors.New("token expired")
		authURL := "https://auth.example.com/authorize?..."
		tokenErr := NewTokenExpiredError(originalErr, authURL)

		// Check Error() method
		errMsg := tokenErr.Error()
		if errMsg == "" {
			t.Error("Expected non-empty error message")
		}
		if !containsString(errMsg, authURL) {
			t.Errorf("Error message should contain auth URL, got: %s", errMsg)
		}

		// Check Unwrap
		if unwrapped := tokenErr.Unwrap(); unwrapped != originalErr {
			t.Errorf("Unwrap() = %v, want %v", unwrapped, originalErr)
		}
	})

	t.Run("error without auth URL", func(t *testing.T) {
		originalErr := errors.New("token expired")
		tokenErr := NewTokenExpiredError(originalErr, "")

		errMsg := tokenErr.Error()
		if errMsg == "" {
			t.Error("Expected non-empty error message")
		}
	})
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
