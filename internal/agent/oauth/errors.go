package oauth

import (
	"strings"
)

// IsTokenExpiredError checks if an error indicates that the OAuth token has expired.
// This is used to detect 401 errors with token validation failures so that
// automatic re-authentication can be triggered.
func IsTokenExpiredError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Check for common token expiration patterns
	patterns := []string{
		"401",
		"invalid_token",
		"Token validation failed",
		"token expired",
		"token has expired",
		"access token expired",
		"unauthorized",
	}

	errLower := strings.ToLower(errStr)
	for _, pattern := range patterns {
		if strings.Contains(errLower, strings.ToLower(pattern)) {
			return true
		}
	}

	return false
}

// TokenExpiredError wraps an original error to indicate token expiration.
// It includes an auth URL for re-authentication.
type TokenExpiredError struct {
	OriginalError error
	AuthURL       string
	Message       string
}

// Error implements the error interface.
func (e *TokenExpiredError) Error() string {
	if e.AuthURL != "" {
		return e.Message + "\nPlease authenticate at: " + e.AuthURL
	}
	return e.Message
}

// Unwrap returns the original error for error chain inspection.
func (e *TokenExpiredError) Unwrap() error {
	return e.OriginalError
}

// NewTokenExpiredError creates a new TokenExpiredError.
func NewTokenExpiredError(originalErr error, authURL string) *TokenExpiredError {
	return &TokenExpiredError{
		OriginalError: originalErr,
		AuthURL:       authURL,
		Message:       "Authentication token expired. Please re-authenticate.",
	}
}
