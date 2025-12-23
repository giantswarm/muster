package oauth

import (
	"errors"
	"strings"
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

func TestIsTokenExpiredError_EdgeCases(t *testing.T) {
	// Additional edge case tests
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "mixed case unauthorized",
			err:      errors.New("UNAUTHORIZED access denied"),
			expected: true,
		},
		{
			name:     "partial 401 in message",
			err:      errors.New("error code 4011 not found"),
			expected: true, // Contains "401" substring
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

func TestIsTokenExpiredError_MessagePatterns(t *testing.T) {
	// Verify specific patterns are detected correctly
	patterns := []string{
		"401",
		"invalid_token",
		"Token validation failed",
		"token expired",
		"token has expired",
		"access token expired",
		"unauthorized",
	}

	for _, pattern := range patterns {
		t.Run("detects "+pattern, func(t *testing.T) {
			err := errors.New("error: " + pattern + " occurred")
			if !IsTokenExpiredError(err) {
				t.Errorf("Expected pattern %q to be detected as token expired error", pattern)
			}
		})
	}
}

func TestIsTokenExpiredError_CaseInsensitive(t *testing.T) {
	// Verify case insensitivity
	testCases := []string{
		"UNAUTHORIZED",
		"Unauthorized",
		"TOKEN EXPIRED",
		"Token Expired",
		"INVALID_TOKEN",
		"Invalid_Token",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			err := errors.New(tc)
			if !IsTokenExpiredError(err) {
				t.Errorf("Expected %q to be detected (case-insensitive)", tc)
			}
		})
	}
}

func TestIsTokenExpiredError_DoesNotFalsePositive(t *testing.T) {
	// Ensure we don't have false positives
	nonTokenErrors := []string{
		"connection refused",
		"timeout",
		"DNS lookup failed",
		"certificate error",
		"500 internal server error",
		"403 forbidden",
		"404 not found",
	}

	for _, errMsg := range nonTokenErrors {
		t.Run(errMsg, func(t *testing.T) {
			err := errors.New(errMsg)
			if IsTokenExpiredError(err) {
				t.Errorf("Expected %q to NOT be detected as token expired error", errMsg)
			}
		})
	}
}

func TestErrorPatternContainment(t *testing.T) {
	// Test that we properly check for string containment
	err := errors.New("transport error: request failed with status 401: token validation failed")

	if !IsTokenExpiredError(err) {
		t.Error("Expected complex error message with 401 to be detected")
	}

	// Verify the error string contains expected patterns
	errStr := err.Error()
	if !strings.Contains(errStr, "401") {
		t.Error("Error should contain '401'")
	}
}
