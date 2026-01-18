package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestAuthCommandStructure(t *testing.T) {
	t.Run("auth command exists", func(t *testing.T) {
		if authCmd == nil {
			t.Fatal("authCmd should not be nil")
		}
	})

	t.Run("auth command properties", func(t *testing.T) {
		if authCmd.Use != "auth" {
			t.Errorf("expected Use 'auth', got %q", authCmd.Use)
		}
		if authCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
		if authCmd.Long == "" {
			t.Error("expected Long description to be set")
		}
	})

	t.Run("auth has subcommands", func(t *testing.T) {
		subcommands := authCmd.Commands()
		if len(subcommands) == 0 {
			t.Error("expected auth to have subcommands")
		}

		expectedSubcommands := []string{"login", "logout", "status", "refresh", "whoami"}
		foundCommands := make(map[string]bool)
		for _, cmd := range subcommands {
			foundCommands[cmd.Name()] = true
		}

		for _, expected := range expectedSubcommands {
			if !foundCommands[expected] {
				t.Errorf("expected subcommand %q to be registered", expected)
			}
		}
	})
}

func TestAuthLoginCommand(t *testing.T) {
	t.Run("login command exists", func(t *testing.T) {
		if authLoginCmd == nil {
			t.Fatal("authLoginCmd should not be nil")
		}
	})

	t.Run("login command properties", func(t *testing.T) {
		if authLoginCmd.Use != "login" {
			t.Errorf("expected Use 'login', got %q", authLoginCmd.Use)
		}
		if authLoginCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
	})

	t.Run("login command has RunE", func(t *testing.T) {
		if authLoginCmd.RunE == nil {
			t.Error("expected RunE to be set")
		}
	})

	t.Run("login has --all flag", func(t *testing.T) {
		flag := authLoginCmd.Flags().Lookup("all")
		if flag == nil {
			t.Error("expected --all flag on login command")
		}
	})

	t.Run("login has --server flag", func(t *testing.T) {
		flag := authLoginCmd.Flags().Lookup("server")
		if flag == nil {
			t.Error("expected --server flag on login command")
		}
	})
}

func TestAuthLogoutCommand(t *testing.T) {
	t.Run("logout command exists", func(t *testing.T) {
		if authLogoutCmd == nil {
			t.Fatal("authLogoutCmd should not be nil")
		}
	})

	t.Run("logout command properties", func(t *testing.T) {
		if authLogoutCmd.Use != "logout" {
			t.Errorf("expected Use 'logout', got %q", authLogoutCmd.Use)
		}
		if authLogoutCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
	})

	t.Run("logout has --all flag", func(t *testing.T) {
		flag := authLogoutCmd.Flags().Lookup("all")
		if flag == nil {
			t.Error("expected --all flag on logout command")
		}
	})

	t.Run("logout has --yes flag", func(t *testing.T) {
		flag := authLogoutCmd.Flags().Lookup("yes")
		if flag == nil {
			t.Error("expected --yes flag on logout command")
		}
	})

	t.Run("logout has --server flag", func(t *testing.T) {
		flag := authLogoutCmd.Flags().Lookup("server")
		if flag == nil {
			t.Error("expected --server flag on logout command")
		}
	})

	t.Run("logout --server flag has -s shorthand", func(t *testing.T) {
		flag := authLogoutCmd.Flags().ShorthandLookup("s")
		if flag == nil {
			t.Error("expected -s shorthand for --server flag")
		}
		if flag.Name != "server" {
			t.Errorf("expected -s to be shorthand for 'server', got %q", flag.Name)
		}
	})
}

func TestAuthStatusCommand(t *testing.T) {
	t.Run("status command exists", func(t *testing.T) {
		if authStatusCmd == nil {
			t.Fatal("authStatusCmd should not be nil")
		}
	})

	t.Run("status command properties", func(t *testing.T) {
		if authStatusCmd.Use != "status" {
			t.Errorf("expected Use 'status', got %q", authStatusCmd.Use)
		}
		if authStatusCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
	})

	t.Run("status has --server flag", func(t *testing.T) {
		flag := authStatusCmd.Flags().Lookup("server")
		if flag == nil {
			t.Error("expected --server flag on status command")
		}
	})
}

func TestAuthRefreshCommand(t *testing.T) {
	t.Run("refresh command exists", func(t *testing.T) {
		if authRefreshCmd == nil {
			t.Fatal("authRefreshCmd should not be nil")
		}
	})

	t.Run("refresh command properties", func(t *testing.T) {
		if authRefreshCmd.Use != "refresh" {
			t.Errorf("expected Use 'refresh', got %q", authRefreshCmd.Use)
		}
		if authRefreshCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
	})
}

func TestAuthWhoamiCommand(t *testing.T) {
	t.Run("whoami command exists", func(t *testing.T) {
		if authWhoamiCmd == nil {
			t.Fatal("authWhoamiCmd should not be nil")
		}
	})

	t.Run("whoami command properties", func(t *testing.T) {
		if authWhoamiCmd.Use != "whoami" {
			t.Errorf("expected Use 'whoami', got %q", authWhoamiCmd.Use)
		}
		if authWhoamiCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
	})

	t.Run("whoami command has RunE", func(t *testing.T) {
		if authWhoamiCmd.RunE == nil {
			t.Error("expected RunE to be set")
		}
	})
}

func TestAuthPersistentFlags(t *testing.T) {
	t.Run("endpoint flag exists", func(t *testing.T) {
		flag := authCmd.PersistentFlags().Lookup("endpoint")
		if flag == nil {
			t.Error("expected --endpoint flag to exist")
		}
	})

	t.Run("config-path flag exists", func(t *testing.T) {
		flag := authCmd.PersistentFlags().Lookup("config-path")
		if flag == nil {
			t.Error("expected --config-path flag to exist")
		}
	})

	t.Run("quiet flag exists", func(t *testing.T) {
		flag := authCmd.PersistentFlags().Lookup("quiet")
		if flag == nil {
			t.Error("expected --quiet flag to exist")
		}
	})
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "negative duration",
			duration: -30 * time.Second,
			expected: "expired",
		},
		{
			name:     "less than a minute",
			duration: 30 * time.Second,
			expected: "< 1 minute",
		},
		{
			name:     "exactly one minute",
			duration: 1 * time.Minute,
			expected: "1 minute",
		},
		{
			name:     "multiple minutes",
			duration: 45 * time.Minute,
			expected: "45 minutes",
		},
		{
			name:     "exactly one hour",
			duration: 1 * time.Hour,
			expected: "1 hour",
		},
		{
			name:     "multiple hours",
			duration: 5 * time.Hour,
			expected: "5 hours",
		},
		{
			name:     "exactly one day",
			duration: 24 * time.Hour,
			expected: "1 day",
		},
		{
			name:     "multiple days",
			duration: 72 * time.Hour,
			expected: "3 days",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.duration)
			if result != tt.expected {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, result, tt.expected)
			}
		})
	}
}

func TestFormatExpiryWithDirection(t *testing.T) {
	tests := []struct {
		name        string
		expiresAt   time.Time
		shouldMatch string // substring that should be in the result
	}{
		{
			name:        "future expiry shows in",
			expiresAt:   time.Now().Add(2 * time.Hour),
			shouldMatch: "in ",
		},
		{
			name:        "past expiry shows expired",
			expiresAt:   time.Now().Add(-2 * time.Hour),
			shouldMatch: "expired",
		},
		{
			name:        "past expiry shows ago",
			expiresAt:   time.Now().Add(-2 * time.Hour),
			shouldMatch: "ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatExpiryWithDirection(tt.expiresAt)
			if !strings.Contains(result, tt.shouldMatch) {
				t.Errorf("formatExpiryWithDirection() = %q, want to contain %q", result, tt.shouldMatch)
			}
		})
	}
}

func TestAuthCommandHelp(t *testing.T) {
	var buf bytes.Buffer

	// Create a copy of the auth command for testing
	testCmd := &cobra.Command{
		Use:   authCmd.Use,
		Short: authCmd.Short,
		Long:  authCmd.Long,
	}

	testCmd.SetOut(&buf)
	testCmd.SetArgs([]string{"--help"})

	err := testCmd.Execute()
	if err != nil {
		t.Fatalf("Error executing help: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "auth") {
		t.Error("help output should contain 'auth'")
	}
	if !strings.Contains(output, "authentication") {
		t.Error("help output should contain 'authentication'")
	}
}

func TestAuthLoginHelp(t *testing.T) {
	var buf bytes.Buffer

	testCmd := &cobra.Command{
		Use:   authLoginCmd.Use,
		Short: authLoginCmd.Short,
		Long:  authLoginCmd.Long,
	}

	testCmd.SetOut(&buf)
	testCmd.SetArgs([]string{"--help"})

	err := testCmd.Execute()
	if err != nil {
		t.Fatalf("Error executing help: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "login") {
		t.Error("help output should contain 'login'")
	}
}

func TestAuthIsRegistered(t *testing.T) {
	// Test that auth command is registered on root
	commands := rootCmd.Commands()
	found := false
	for _, cmd := range commands {
		if cmd.Name() == "auth" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected 'auth' command to be registered on root")
	}
}
