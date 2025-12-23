package oauth

import (
	"strings"
)

// tokenExpirationPatterns are the patterns we look for to detect token expiration errors.
var tokenExpirationPatterns = []string{
	"401",
	"invalid_token",
	"token validation failed",
	"token expired",
	"token has expired",
	"access token expired",
	"unauthorized",
}

// IsTokenExpiredError checks if an error indicates that the OAuth token has expired.
// This is used to detect 401 errors with token validation failures so that
// automatic re-authentication can be triggered.
//
// The function checks for common patterns in error messages that indicate
// authentication failures, including HTTP 401 status codes and OAuth-specific
// error codes like "invalid_token".
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
