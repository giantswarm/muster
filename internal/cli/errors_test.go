package cli

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestAuthRequiredError(t *testing.T) {
	t.Run("error message includes endpoint and guidance", func(t *testing.T) {
		err := &AuthRequiredError{Endpoint: "https://muster.example.com/mcp"}
		msg := err.Error()

		if !strings.Contains(msg, "https://muster.example.com/mcp") {
			t.Error("expected error message to contain endpoint")
		}
		if !strings.Contains(msg, "muster auth login") {
			t.Error("expected error message to contain login command")
		}
		if !strings.Contains(msg, "muster auth status") {
			t.Error("expected error message to contain status command")
		}
	})

	t.Run("Is returns true for same type", func(t *testing.T) {
		err1 := &AuthRequiredError{Endpoint: "https://example.com"}
		err2 := &AuthRequiredError{Endpoint: "https://other.com"}

		if !err1.Is(err2) {
			t.Error("expected Is to return true for same type")
		}
	})

	t.Run("Is returns false for different type", func(t *testing.T) {
		err1 := &AuthRequiredError{Endpoint: "https://example.com"}
		err2 := errors.New("some error")

		if err1.Is(err2) {
			t.Error("expected Is to return false for different type")
		}
	})

	t.Run("errors.Is works with wrapped error", func(t *testing.T) {
		authErr := &AuthRequiredError{Endpoint: "https://example.com"}
		wrappedErr := fmt.Errorf("wrapped: %w", authErr)

		if !errors.Is(wrappedErr, &AuthRequiredError{}) {
			t.Error("expected errors.Is to find wrapped AuthRequiredError")
		}
	})
}

func TestAuthExpiredError(t *testing.T) {
	t.Run("error message includes endpoint and guidance", func(t *testing.T) {
		err := &AuthExpiredError{Endpoint: "https://muster.example.com/mcp"}
		msg := err.Error()

		if !strings.Contains(msg, "https://muster.example.com/mcp") {
			t.Error("expected error message to contain endpoint")
		}
		if !strings.Contains(msg, "muster auth login") {
			t.Error("expected error message to contain login command")
		}
		if !strings.Contains(msg, "muster auth refresh") {
			t.Error("expected error message to contain refresh command")
		}
		if !strings.Contains(msg, "expired") {
			t.Error("expected error message to mention 'expired'")
		}
	})

	t.Run("Is returns true for same type", func(t *testing.T) {
		err1 := &AuthExpiredError{Endpoint: "https://example.com"}
		err2 := &AuthExpiredError{Endpoint: "https://other.com"}

		if !err1.Is(err2) {
			t.Error("expected Is to return true for same type")
		}
	})

	t.Run("Is returns false for different type", func(t *testing.T) {
		err1 := &AuthExpiredError{Endpoint: "https://example.com"}
		err2 := errors.New("some error")

		if err1.Is(err2) {
			t.Error("expected Is to return false for different type")
		}
	})

	t.Run("Is returns false for AuthRequiredError", func(t *testing.T) {
		err1 := &AuthExpiredError{Endpoint: "https://example.com"}
		err2 := &AuthRequiredError{Endpoint: "https://example.com"}

		if err1.Is(err2) {
			t.Error("expected Is to return false for AuthRequiredError")
		}
	})
}

func TestAuthFailedError(t *testing.T) {
	t.Run("error message includes endpoint and reason", func(t *testing.T) {
		reason := errors.New("connection timeout")
		err := &AuthFailedError{
			Endpoint: "https://muster.example.com/mcp",
			Reason:   reason,
		}
		msg := err.Error()

		if !strings.Contains(msg, "https://muster.example.com/mcp") {
			t.Error("expected error message to contain endpoint")
		}
		if !strings.Contains(msg, "connection timeout") {
			t.Error("expected error message to contain reason")
		}
		if !strings.Contains(msg, "muster auth login") {
			t.Error("expected error message to contain login command")
		}
	})

	t.Run("Unwrap returns underlying error", func(t *testing.T) {
		reason := errors.New("connection timeout")
		err := &AuthFailedError{
			Endpoint: "https://example.com",
			Reason:   reason,
		}

		unwrapped := err.Unwrap()
		if unwrapped != reason {
			t.Errorf("expected unwrapped error to be %v, got %v", reason, unwrapped)
		}
	})

	t.Run("errors.Unwrap works", func(t *testing.T) {
		reason := errors.New("connection timeout")
		err := &AuthFailedError{
			Endpoint: "https://example.com",
			Reason:   reason,
		}

		unwrapped := errors.Unwrap(err)
		if unwrapped != reason {
			t.Errorf("expected errors.Unwrap to return %v, got %v", reason, unwrapped)
		}
	})

	t.Run("Is returns true for same type", func(t *testing.T) {
		err1 := &AuthFailedError{Endpoint: "https://example.com", Reason: errors.New("err1")}
		err2 := &AuthFailedError{Endpoint: "https://other.com", Reason: errors.New("err2")}

		if !err1.Is(err2) {
			t.Error("expected Is to return true for same type")
		}
	})

	t.Run("Is returns false for different type", func(t *testing.T) {
		err1 := &AuthFailedError{Endpoint: "https://example.com", Reason: errors.New("err1")}
		err2 := errors.New("some error")

		if err1.Is(err2) {
			t.Error("expected Is to return false for different type")
		}
	})
}

func TestServerStatus(t *testing.T) {
	t.Run("IsReady returns true when reachable and no auth", func(t *testing.T) {
		status := &ServerStatus{
			Endpoint:      "https://example.com",
			Reachable:     true,
			AuthRequired:  false,
			Authenticated: false,
		}

		if !status.IsReady() {
			t.Error("expected IsReady to return true when reachable and no auth required")
		}
	})

	t.Run("IsReady returns true when reachable with auth", func(t *testing.T) {
		status := &ServerStatus{
			Endpoint:      "https://example.com",
			Reachable:     true,
			AuthRequired:  true,
			Authenticated: true,
		}

		if !status.IsReady() {
			t.Error("expected IsReady to return true when authenticated")
		}
	})

	t.Run("IsReady returns false when not reachable", func(t *testing.T) {
		status := &ServerStatus{
			Endpoint:      "https://example.com",
			Reachable:     false,
			AuthRequired:  false,
			Authenticated: false,
		}

		if status.IsReady() {
			t.Error("expected IsReady to return false when not reachable")
		}
	})

	t.Run("IsReady returns false when auth required but not authenticated", func(t *testing.T) {
		status := &ServerStatus{
			Endpoint:      "https://example.com",
			Reachable:     true,
			AuthRequired:  true,
			Authenticated: false,
		}

		if status.IsReady() {
			t.Error("expected IsReady to return false when auth required but not authenticated")
		}
	})

	t.Run("ServerStatus stores error", func(t *testing.T) {
		expectedErr := errors.New("connection refused")
		status := &ServerStatus{
			Endpoint: "https://example.com",
			Error:    expectedErr,
		}

		if status.Error != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, status.Error)
		}
	})
}

func TestConnectionErrorType(t *testing.T) {
	tests := []struct {
		name     string
		errType  ConnectionErrorType
		expected string
	}{
		{"unknown type", ConnectionErrorUnknown, "Connection error"},
		{"TLS type", ConnectionErrorTLS, "TLS certificate error"},
		{"network type", ConnectionErrorNetwork, "Network error"},
		{"timeout type", ConnectionErrorTimeout, "Connection timeout"},
		{"DNS type", ConnectionErrorDNS, "DNS resolution error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.errType.String()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestConnectionError(t *testing.T) {
	t.Run("TLS error message includes certificate guidance", func(t *testing.T) {
		err := &ConnectionError{
			Endpoint: "https://muster.example.com",
			Type:     ConnectionErrorTLS,
			Reason:   errors.New("x509: certificate is not valid for hostname"),
		}
		msg := err.Error()

		if !strings.Contains(msg, "TLS certificate verification failed") {
			t.Error("expected error message to mention TLS verification")
		}
		if !strings.Contains(msg, "muster.example.com") {
			t.Error("expected error message to contain endpoint")
		}
		if !strings.Contains(msg, "Self-signed") {
			t.Error("expected error message to mention self-signed certificates")
		}
	})

	t.Run("Network error message includes connectivity guidance", func(t *testing.T) {
		err := &ConnectionError{
			Endpoint: "https://muster.example.com",
			Type:     ConnectionErrorNetwork,
			Reason:   errors.New("connection refused"),
		}
		msg := err.Error()

		if !strings.Contains(msg, "Connection failed") {
			t.Error("expected error message to mention connection failed")
		}
		if !strings.Contains(msg, "muster.example.com") {
			t.Error("expected error message to contain endpoint")
		}
		if !strings.Contains(msg, "Server is not running") {
			t.Error("expected error message to mention server not running")
		}
	})

	t.Run("Timeout error message includes timeout guidance", func(t *testing.T) {
		err := &ConnectionError{
			Endpoint: "https://muster.example.com",
			Type:     ConnectionErrorTimeout,
			Reason:   errors.New("context deadline exceeded"),
		}
		msg := err.Error()

		if !strings.Contains(msg, "timed out") {
			t.Error("expected error message to mention timeout")
		}
		if !strings.Contains(msg, "muster.example.com") {
			t.Error("expected error message to contain endpoint")
		}
	})

	t.Run("DNS error message includes resolution guidance", func(t *testing.T) {
		err := &ConnectionError{
			Endpoint: "https://muster.example.com",
			Type:     ConnectionErrorDNS,
			Reason:   errors.New("no such host"),
		}
		msg := err.Error()

		if !strings.Contains(msg, "DNS resolution failed") {
			t.Error("expected error message to mention DNS resolution")
		}
		if !strings.Contains(msg, "muster.example.com") {
			t.Error("expected error message to contain endpoint")
		}
	})

	t.Run("Unknown error message shows basic info", func(t *testing.T) {
		err := &ConnectionError{
			Endpoint: "https://muster.example.com",
			Type:     ConnectionErrorUnknown,
			Reason:   errors.New("some unknown error"),
		}
		msg := err.Error()

		if !strings.Contains(msg, "Connection failed") {
			t.Error("expected error message to mention connection failed")
		}
		if !strings.Contains(msg, "some unknown error") {
			t.Error("expected error message to contain reason")
		}
	})

	t.Run("Unwrap returns underlying error", func(t *testing.T) {
		reason := errors.New("connection refused")
		err := &ConnectionError{
			Endpoint: "https://example.com",
			Type:     ConnectionErrorNetwork,
			Reason:   reason,
		}

		unwrapped := err.Unwrap()
		if unwrapped != reason {
			t.Errorf("expected unwrapped error to be %v, got %v", reason, unwrapped)
		}
	})

	t.Run("Is returns true for same type", func(t *testing.T) {
		err1 := &ConnectionError{Endpoint: "https://example.com", Type: ConnectionErrorTLS}
		err2 := &ConnectionError{Endpoint: "https://other.com", Type: ConnectionErrorNetwork}

		if !err1.Is(err2) {
			t.Error("expected Is to return true for same type")
		}
	})

	t.Run("Is returns false for different type", func(t *testing.T) {
		err1 := &ConnectionError{Endpoint: "https://example.com", Type: ConnectionErrorTLS}
		err2 := errors.New("some error")

		if err1.Is(err2) {
			t.Error("expected Is to return false for different type")
		}
	})

	t.Run("errors.Is works with wrapped error", func(t *testing.T) {
		connErr := &ConnectionError{Endpoint: "https://example.com", Type: ConnectionErrorTLS}
		wrappedErr := fmt.Errorf("wrapped: %w", connErr)

		if !errors.Is(wrappedErr, &ConnectionError{}) {
			t.Error("expected errors.Is to find wrapped ConnectionError")
		}
	})
}

func TestClassifyConnectionError(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		result := ClassifyConnectionError(nil, "https://example.com")
		if result != nil {
			t.Error("expected nil for nil error")
		}
	})

	t.Run("x509 certificate error is classified as TLS", func(t *testing.T) {
		// Simulate an x509 error message
		err := errors.New("Get https://example.com: x509: certificate is not valid for hostname")
		result := ClassifyConnectionError(err, "https://example.com")

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != ConnectionErrorTLS {
			t.Errorf("expected TLS error type, got %v", result.Type)
		}
	})

	t.Run("x509 HostnameError is classified as TLS", func(t *testing.T) {
		// Create a real x509.HostnameError
		cert := &x509.Certificate{}
		hostErr := x509.HostnameError{Certificate: cert, Host: "example.com"}
		err := fmt.Errorf("connection failed: %w", &hostErr)
		result := ClassifyConnectionError(err, "https://example.com")

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != ConnectionErrorTLS {
			t.Errorf("expected TLS error type, got %v", result.Type)
		}
	})

	t.Run("connection refused is classified as network", func(t *testing.T) {
		err := errors.New("dial tcp 127.0.0.1:443: connect: connection refused")
		result := ClassifyConnectionError(err, "https://localhost")

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != ConnectionErrorNetwork {
			t.Errorf("expected Network error type, got %v", result.Type)
		}
	})

	t.Run("timeout error is classified as timeout", func(t *testing.T) {
		err := errors.New("context deadline exceeded")
		result := ClassifyConnectionError(err, "https://example.com")

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != ConnectionErrorTimeout {
			t.Errorf("expected Timeout error type, got %v", result.Type)
		}
	})

	t.Run("DNS error is classified as DNS", func(t *testing.T) {
		dnsErr := &net.DNSError{
			Err:  "no such host",
			Name: "nonexistent.example.com",
		}
		err := fmt.Errorf("lookup failed: %w", dnsErr)
		result := ClassifyConnectionError(err, "https://nonexistent.example.com")

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != ConnectionErrorDNS {
			t.Errorf("expected DNS error type, got %v", result.Type)
		}
	})

	t.Run("unknown error is classified as unknown", func(t *testing.T) {
		err := errors.New("some random error")
		result := ClassifyConnectionError(err, "https://example.com")

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != ConnectionErrorUnknown {
			t.Errorf("expected Unknown error type, got %v", result.Type)
		}
	})

	t.Run("TLS handshake error is classified as TLS", func(t *testing.T) {
		err := errors.New("TLS handshake error: remote error: tls: bad certificate")
		result := ClassifyConnectionError(err, "https://example.com")

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != ConnectionErrorTLS {
			t.Errorf("expected TLS error type, got %v", result.Type)
		}
	})

	t.Run("certificate signed by unknown authority is TLS", func(t *testing.T) {
		err := errors.New("x509: certificate signed by unknown authority")
		result := ClassifyConnectionError(err, "https://example.com")

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != ConnectionErrorTLS {
			t.Errorf("expected TLS error type, got %v", result.Type)
		}
	})
}

func TestIsTLSError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"x509 certificate invalid", errors.New("x509: certificate is invalid"), true},
		{"certificate signed by unknown authority", errors.New("certificate signed by unknown authority"), true},
		{"TLS handshake error", errors.New("TLS handshake failed"), true},
		{"certificate has expired", errors.New("certificate has expired"), true},
		{"connection refused", errors.New("connection refused"), false},
		{"timeout", errors.New("context deadline exceeded"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTLSError(tt.err)
			if result != tt.expected {
				t.Errorf("isTLSError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}
