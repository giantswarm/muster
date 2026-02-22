package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jedib0t/go-pretty/v6/text"

	"github.com/giantswarm/muster/internal/api"
)

func TestFormatMCPServerStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected string
	}{
		{
			name:     "connected status",
			status:   "connected",
			expected: text.FgGreen.Sprint("Connected"),
		},
		{
			name:     "auth_required status",
			status:   "auth_required",
			expected: text.FgYellow.Sprint("Not authenticated"),
		},
		{
			name:     "disconnected status",
			status:   "disconnected",
			expected: text.FgRed.Sprint("Disconnected"),
		},
		{
			name:     "error status",
			status:   "error",
			expected: text.FgRed.Sprint("Error"),
		},
		{
			name:     "unknown status",
			status:   "initializing",
			expected: text.FgHiBlack.Sprint("initializing"),
		},
		{
			name:     "empty status",
			status:   "",
			expected: text.FgHiBlack.Sprint(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatMCPServerStatus(tt.status)
			if result != tt.expected {
				t.Errorf("formatMCPServerStatus(%q) = %q, want %q", tt.status, result, tt.expected)
			}
		})
	}
}

func TestAuthStatusCmdProperties(t *testing.T) {
	t.Run("status command Use field", func(t *testing.T) {
		if authStatusCmd.Use != "status" {
			t.Errorf("expected Use 'status', got %q", authStatusCmd.Use)
		}
	})

	t.Run("status command has short description", func(t *testing.T) {
		if authStatusCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
	})

	t.Run("status command has long description", func(t *testing.T) {
		if authStatusCmd.Long == "" {
			t.Error("expected Long description to be set")
		}
	})

	t.Run("status command has RunE", func(t *testing.T) {
		if authStatusCmd.RunE == nil {
			t.Error("expected RunE to be set")
		}
	})
}

func TestFormatConnectionErrorReason(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected string
	}{
		{
			name:     "nil error returns unknown",
			errMsg:   "",
			expected: "unknown error",
		},
		{
			name:     "x509 error extracts certificate message",
			errMsg:   "Get https://example.com: x509: certificate is not valid for hostname",
			expected: "x509: certificate is not valid for hostname",
		},
		{
			name:     "connection refused extracts core message",
			errMsg:   "dial tcp 127.0.0.1:443: connect: connection refused",
			expected: "connection refused",
		},
		{
			name:     "connect error extracts message",
			errMsg:   "dial tcp 10.0.0.1:443: connect: no route to host",
			expected: "no route to host",
		},
		{
			name:     "simple error returns as-is",
			errMsg:   "some error occurred",
			expected: "some error occurred",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = fmt.Errorf("%s", tt.errMsg)
			}
			result := formatConnectionErrorReason(err)
			if !strings.Contains(result, tt.expected) && result != tt.expected {
				t.Errorf("formatConnectionErrorReason(%q) = %q, want to contain %q", tt.errMsg, result, tt.expected)
			}
		})
	}
}

// captureStdout captures stdout output produced by fn.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read pipe: %v", err)
	}
	return string(out)
}

func TestPrintAuthenticatedStatus(t *testing.T) {
	authQuiet = false

	t.Run("shows session expiry when refresh token has expiry", func(t *testing.T) {
		sessionExpiry := time.Now().Add(29 * 24 * time.Hour)
		status := &api.AuthStatus{
			Authenticated:    true,
			ExpiresAt:        time.Now().Add(25 * time.Minute),
			HasRefreshToken:  true,
			RefreshExpiresAt: sessionExpiry,
		}

		output := captureStdout(t, func() {
			printAuthenticatedStatus(status)
		})

		if !strings.Contains(output, "Session:") {
			t.Errorf("expected output to contain 'Session:', got: %s", output)
		}
		if !strings.Contains(output, "(auto-refresh)") {
			t.Errorf("expected output to contain '(auto-refresh)', got: %s", output)
		}
		if strings.Contains(output, "Refresh:") {
			t.Errorf("should not contain 'Refresh:' when session expiry is set, got: %s", output)
		}
	})

	t.Run("shows refresh available when no expiry is set", func(t *testing.T) {
		status := &api.AuthStatus{
			Authenticated:   true,
			ExpiresAt:       time.Now().Add(25 * time.Minute),
			HasRefreshToken: true,
		}

		output := captureStdout(t, func() {
			printAuthenticatedStatus(status)
		})

		if !strings.Contains(output, "Refresh:") {
			t.Errorf("expected output to contain 'Refresh:', got: %s", output)
		}
		if strings.Contains(output, "Session:") {
			t.Errorf("should not contain 'Session:' when RefreshExpiresAt is zero, got: %s", output)
		}
	})

	t.Run("shows no refresh when refresh token is absent", func(t *testing.T) {
		status := &api.AuthStatus{
			Authenticated:   true,
			ExpiresAt:       time.Now().Add(25 * time.Minute),
			HasRefreshToken: false,
		}

		output := captureStdout(t, func() {
			printAuthenticatedStatus(status)
		})

		if !strings.Contains(output, "Not available") {
			t.Errorf("expected output to contain 'Not available', got: %s", output)
		}
	})
}
