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
			name:     "status 401 error",
			err:      errors.New("request failed with status 401: something"),
			expected: true,
		},
		{
			name:     "401 unauthorized error",
			err:      errors.New("HTTP 401 Unauthorized"),
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
			name:     "transport error with status 401",
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
			name:     "partial 401 in message - no false positive",
			err:      errors.New("error code 4011 not found"),
			expected: false, // Should NOT match - we use specific patterns now
		},
		{
			name:     "port number containing 401",
			err:      errors.New("connection to port 4010 failed"),
			expected: false, // Should NOT match random numbers
		},
		{
			name:     "status code in different format",
			err:      errors.New("HTTP/1.1 401 Unauthorized"),
			expected: true, // Should match "401 " pattern
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
	patternsToTest := []struct {
		pattern  string
		expected bool
	}{
		{"status 401", true},
		{"status: 401", true},
		{"401 unauthorized", true},
		{"error 401: something", true}, // matches " 401:"
		{"returned 401 error", true},   // matches " 401 "
		{"invalid_token", true},
		{"Token validation failed", true},
		{"token expired", true},
		{"token has expired", true},
		{"access token expired", true},
		{"unauthorized", true},
	}

	for _, tc := range patternsToTest {
		t.Run("detects "+tc.pattern, func(t *testing.T) {
			err := errors.New("error: " + tc.pattern + " occurred")
			result := IsTokenExpiredError(err)
			if result != tc.expected {
				t.Errorf("Expected pattern %q detection to be %v, got %v", tc.pattern, tc.expected, result)
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
		"STATUS 401:",
		"Status 401:",
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
		"error code 4011",           // Should not match - not a 401 status
		"port 4010 unavailable",     // Should not match - not a 401 status
		"reference id: ABC401DEF",   // Should not match - 401 in middle of string
		"transaction 401234 failed", // Should not match - 401 is part of larger number
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
		t.Error("Expected complex error message with status 401 to be detected")
	}

	// Verify the error string contains expected patterns
	errStr := err.Error()
	if !strings.Contains(errStr, "status 401") {
		t.Error("Error should contain 'status 401'")
	}
}

func TestIsTokenExpiredError_RealWorldErrors(t *testing.T) {
	// Test patterns from real OAuth/HTTP client errors
	realErrors := []struct {
		name     string
		err      string
		expected bool
	}{
		{
			name:     "Go HTTP client 401",
			err:      "Get \"https://api.example.com\": status 401: Unauthorized",
			expected: true,
		},
		{
			name:     "OAuth2 invalid token",
			err:      `oauth2: token expired and refresh token is not set`,
			expected: true,
		},
		{
			name:     "JSON API error response",
			err:      `{"error":"invalid_token","error_description":"The access token is invalid"}`,
			expected: true,
		},
		{
			name:     "Generic HTTP 401",
			err:      "HTTP 401 Unauthorized: Please authenticate",
			expected: true,
		},
		{
			name:     "Server unavailable (not auth error)",
			err:      "server returned 503: Service Unavailable",
			expected: false,
		},
	}

	for _, tc := range realErrors {
		t.Run(tc.name, func(t *testing.T) {
			err := errors.New(tc.err)
			result := IsTokenExpiredError(err)
			if result != tc.expected {
				t.Errorf("IsTokenExpiredError(%q) = %v, want %v", tc.err, result, tc.expected)
			}
		})
	}
}
