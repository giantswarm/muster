package cli

import (
	"errors"
	"fmt"
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
