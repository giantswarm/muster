package oauth

import (
	"strings"
)

// tokenExpirationPatterns are the patterns we look for to detect token expiration errors.
// These patterns are designed to be specific enough to avoid false positives while
// catching the common error formats from HTTP clients and OAuth servers.
var tokenExpirationPatterns = []string{
	// HTTP status code patterns - more specific to avoid matching unrelated numbers
	"status 401",
	"status: 401",
	"401 unauthorized",
	" 401:",
	" 401 ",
	// OAuth-specific error codes
	"invalid_token",
	"token validation failed",
	"token expired",
	"token has expired",
	"access token expired",
	// General authentication failure
	"unauthorized",
}

// IsTokenExpiredError checks if an error indicates that the OAuth token has expired.
// This is used to detect 401 errors with token validation failures so that
// automatic re-authentication can be triggered.
//
// The function checks for common patterns in error messages that indicate
// authentication failures, including HTTP 401 status codes and OAuth-specific
// error codes like "invalid_token".
//
// Security note: Patterns are designed to be specific to avoid false positives
// that could trigger unnecessary re-authentication flows.
func IsTokenExpiredError(err error) bool {
	if err == nil {
		return false
	}

	errLower := strings.ToLower(err.Error())

	for _, pattern := range tokenExpirationPatterns {
		if strings.Contains(errLower, pattern) {
			return true
		}
	}

	return false
}
