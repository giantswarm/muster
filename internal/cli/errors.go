package cli

import (
	"fmt"
)

// AuthRequiredError indicates authentication is needed.
// Implements error with actionable guidance.
type AuthRequiredError struct {
	// Endpoint is the URL that requires authentication.
	Endpoint string
}

// Error returns a user-friendly error message with actionable guidance.
func (e *AuthRequiredError) Error() string {
	return fmt.Sprintf(`Authentication required for %s

To authenticate, run:
  muster auth login --endpoint %s

To check current authentication status:
  muster auth status`, e.Endpoint, e.Endpoint)
}

// Is allows errors.Is() to work with wrapped errors.
func (e *AuthRequiredError) Is(target error) bool {
	_, ok := target.(*AuthRequiredError)
	return ok
}

// AuthExpiredError indicates the token has expired.
// Implements error with actionable guidance.
type AuthExpiredError struct {
	// Endpoint is the URL whose token has expired.
	Endpoint string
}

// Error returns a user-friendly error message with actionable guidance.
func (e *AuthExpiredError) Error() string {
	return fmt.Sprintf(`Authentication expired for %s

To re-authenticate, run:
  muster auth login --endpoint %s

Or try to refresh your token:
  muster auth refresh --endpoint %s`, e.Endpoint, e.Endpoint, e.Endpoint)
}

// Is allows errors.Is() to work with wrapped errors.
func (e *AuthExpiredError) Is(target error) bool {
	_, ok := target.(*AuthExpiredError)
	return ok
}

// AuthFailedError indicates authentication failed.
type AuthFailedError struct {
	// Endpoint is the URL where authentication failed.
	Endpoint string
	// Reason is the underlying error.
	Reason error
}

// Error returns a user-friendly error message with actionable guidance.
func (e *AuthFailedError) Error() string {
	return fmt.Sprintf(`Authentication failed for %s: %v

To retry authentication, run:
  muster auth login --endpoint %s`, e.Endpoint, e.Reason, e.Endpoint)
}

// Unwrap returns the underlying error.
func (e *AuthFailedError) Unwrap() error {
	return e.Reason
}

// Is allows errors.Is() to work with wrapped errors.
func (e *AuthFailedError) Is(target error) bool {
	_, ok := target.(*AuthFailedError)
	return ok
}

// ServerStatus represents the connectivity and auth status of a server.
type ServerStatus struct {
	// Endpoint is the URL of the server.
	Endpoint string

	// Reachable indicates whether the server responded to probes.
	Reachable bool

	// AuthRequired indicates whether the server requires authentication.
	AuthRequired bool

	// Authenticated indicates whether we have a valid token for this server.
	Authenticated bool

	// Error holds any error that occurred during the check.
	Error error
}

// IsReady returns true if the server is reachable and either doesn't require
// auth or we're authenticated.
func (s *ServerStatus) IsReady() bool {
	return s.Reachable && (!s.AuthRequired || s.Authenticated)
}
