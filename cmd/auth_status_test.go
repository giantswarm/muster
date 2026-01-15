package cmd

import (
	"testing"

	"github.com/jedib0t/go-pretty/v6/text"
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
