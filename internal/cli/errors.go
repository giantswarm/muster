package cli

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ConnectionErrorType categorizes the type of connection error.
type ConnectionErrorType int

const (
	// ConnectionErrorUnknown indicates an unclassified connection error.
	ConnectionErrorUnknown ConnectionErrorType = iota
	// ConnectionErrorTLS indicates a TLS/certificate verification error.
	ConnectionErrorTLS
	// ConnectionErrorNetwork indicates a network connectivity error (e.g., refused, unreachable).
	ConnectionErrorNetwork
	// ConnectionErrorTimeout indicates a connection timeout.
	ConnectionErrorTimeout
	// ConnectionErrorDNS indicates a DNS resolution failure.
	ConnectionErrorDNS
)

// String returns a human-readable name for the connection error type.
func (t ConnectionErrorType) String() string {
	switch t {
	case ConnectionErrorTLS:
		return "TLS certificate error"
	case ConnectionErrorNetwork:
		return "Network error"
	case ConnectionErrorTimeout:
		return "Connection timeout"
	case ConnectionErrorDNS:
		return "DNS resolution error"
	default:
		return "Connection error"
	}
}

// ConnectionError indicates a connection failure to an endpoint.
// It wraps the underlying error and provides categorization for better user feedback.
type ConnectionError struct {
	// Endpoint is the URL that could not be reached.
	Endpoint string
	// Type categorizes the connection error.
	Type ConnectionErrorType
	// Reason is the underlying error.
	Reason error
}

// ClassifyConnectionError analyzes an error and returns a ConnectionError with the appropriate type.
// If the error is nil, returns nil.
func ClassifyConnectionError(err error, endpoint string) *ConnectionError {
	if err == nil {
		return nil
	}

	// Check for TLS/certificate errors
	if isTLSError(err) {
		return &ConnectionError{
			Endpoint: endpoint,
			Type:     ConnectionErrorTLS,
			Reason:   err,
		}
	}

	// Check for DNS errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &ConnectionError{
			Endpoint: endpoint,
			Type:     ConnectionErrorDNS,
			Reason:   err,
		}
	}

	// Check for timeout errors
	if isTimeoutError(err) {
		return &ConnectionError{
			Endpoint: endpoint,
			Type:     ConnectionErrorTimeout,
			Reason:   err,
		}
	}

	// Check for network errors (connection refused, unreachable, etc.)
	if isNetworkError(err.Error()) {
		return &ConnectionError{
			Endpoint: endpoint,
			Type:     ConnectionErrorNetwork,
			Reason:   err,
		}
	}

	// Default to unknown connection error
	return &ConnectionError{
		Endpoint: endpoint,
		Type:     ConnectionErrorUnknown,
		Reason:   err,
	}
}

// isTLSError checks if the error is related to TLS/certificate issues.
func isTLSError(err error) bool {
	if err == nil {
		return false
	}

	// Check for x509 certificate errors using errors.As
	var certErr *x509.CertificateInvalidError
	var hostErr *x509.HostnameError
	var unknownAuthErr *x509.UnknownAuthorityError
	var systemRootsErr *x509.SystemRootsError

	if errors.As(err, &certErr) || errors.As(err, &hostErr) ||
		errors.As(err, &unknownAuthErr) || errors.As(err, &systemRootsErr) {
		return true
	}

	// Also check error message for TLS-related keywords
	// Note: "certificate" is checked broadly as it covers most TLS-related error messages
	errStr := err.Error()
	tlsKeywords := []string{
		"x509:",
		"certificate",
		"tls:",
		"TLS handshake",
	}

	for _, keyword := range tlsKeywords {
		if strings.Contains(errStr, keyword) {
			return true
		}
	}

	return false
}

// isTimeoutError checks if the error is a timeout.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}

	// Check for net.Error timeout (interface, needs manual unwrapping)
	for e := err; e != nil; {
		if ne, ok := e.(net.Error); ok && ne.Timeout() {
			return true
		}
		if u, ok := e.(interface{ Unwrap() error }); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}

	// Check for url.Error timeout
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}

	// Check error message
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded")
}

// isNetworkError checks if the error string indicates a network connectivity issue.
func isNetworkError(errStr string) bool {
	networkKeywords := []string{
		"connection refused",
		"connection reset",
		"network is unreachable",
		"no route to host",
		"dial tcp",
		"connect:",
	}

	for _, keyword := range networkKeywords {
		if strings.Contains(errStr, keyword) {
			return true
		}
	}
	return false
}

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
