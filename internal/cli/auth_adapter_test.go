package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/giantswarm/muster/internal/agent/oauth"
)

func TestNewAuthAdapter(t *testing.T) {
	t.Run("creates adapter with default configuration", func(t *testing.T) {
		adapter, err := NewAuthAdapter()
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		if adapter == nil {
			t.Fatal("expected non-nil adapter")
		}
		if adapter.managers == nil {
			t.Error("expected managers map to be initialized")
		}
		if adapter.tokenStorageDir == "" {
			t.Error("expected tokenStorageDir to be set")
		}
	})
}

func TestAuthAdapter_HasValidToken(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer adapter.Close()

	// Without any tokens, should return false
	if adapter.HasValidToken("https://example.com") {
		t.Error("expected HasValidToken to return false when no token exists")
	}
}

func TestAuthAdapter_GetBearerToken_NotAuthenticated(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer adapter.Close()

	_, err = adapter.GetBearerToken("https://example.com")
	if err == nil {
		t.Error("expected error when getting token without authentication")
	}

	// Should return AuthRequiredError
	var authErr *AuthRequiredError
	if ok := isAuthRequiredError(err); !ok {
		t.Errorf("expected AuthRequiredError, got %T: %v", err, authErr)
	}
}

func TestAuthAdapter_Logout(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer adapter.Close()

	// Logout should work even when no token exists
	err = adapter.Logout("https://example.com")
	if err != nil {
		t.Errorf("unexpected error on logout: %v", err)
	}
}

func TestAuthAdapter_LogoutAll(t *testing.T) {
	// Create a temp directory for tokens
	tmpDir := t.TempDir()

	adapter := &AuthAdapter{
		managers:        make(map[string]*oauth.AuthManager),
		tokenStorageDir: tmpDir,
	}
	defer adapter.Close()

	// LogoutAll should work even when no managers exist
	err := adapter.LogoutAll()
	if err != nil {
		t.Errorf("unexpected error on logoutAll: %v", err)
	}
}

func TestAuthAdapter_GetStatus_Empty(t *testing.T) {
	// Create a temp directory for tokens
	tmpDir := t.TempDir()

	adapter := &AuthAdapter{
		managers:        make(map[string]*oauth.AuthManager),
		tokenStorageDir: tmpDir,
	}
	defer adapter.Close()

	statuses := adapter.GetStatus()
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses, got %d", len(statuses))
	}
}

func TestAuthAdapter_GetStatusForEndpoint_Unknown(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer adapter.Close()

	status := adapter.GetStatusForEndpoint("https://unknown.example.com")
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if status.Endpoint != "https://unknown.example.com" {
		t.Errorf("expected endpoint to match, got %s", status.Endpoint)
	}
}

func TestAuthAdapter_Close(t *testing.T) {
	adapter, err := NewAuthAdapter()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	err = adapter.Close()
	if err != nil {
		t.Errorf("unexpected error on close: %v", err)
	}

	// Managers should be cleared
	if len(adapter.managers) != 0 {
		t.Error("expected managers to be cleared after close")
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://example.com/mcp", "https://example.com"},
		{"https://example.com/sse", "https://example.com"},
		{"https://example.com/", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"https://example.com/mcp/", "https://example.com"},
		{"http://localhost:8080/mcp", "http://localhost:8080"},
		{"http://localhost:8080/sse", "http://localhost:8080"},
		{"http://localhost:8090", "http://localhost:8090"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeEndpoint(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeEndpoint(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestListTokenFiles(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		adapter := &AuthAdapter{
			tokenStorageDir: tmpDir,
		}

		tokens, err := adapter.listTokenFiles()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(tokens) != 0 {
			t.Errorf("expected 0 tokens, got %d", len(tokens))
		}
	})

	t.Run("non-existent directory", func(t *testing.T) {
		adapter := &AuthAdapter{
			tokenStorageDir: "/nonexistent/directory/path",
		}

		tokens, err := adapter.listTokenFiles()
		if err != nil {
			t.Errorf("unexpected error for non-existent dir: %v", err)
		}
		if len(tokens) != 0 {
			t.Errorf("expected 0 tokens, got %d", len(tokens))
		}
	})

	t.Run("directory with non-json files", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create a non-json file
		err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("test"), 0644)
		if err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		adapter := &AuthAdapter{
			tokenStorageDir: tmpDir,
		}

		tokens, err := adapter.listTokenFiles()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(tokens) != 0 {
			t.Errorf("expected 0 tokens (non-json files ignored), got %d", len(tokens))
		}
	})
}

func TestReadTokenFile(t *testing.T) {
	t.Run("non-existent file", func(t *testing.T) {
		_, err := readTokenFile("/nonexistent/path.json")
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "invalid.json")
		err := os.WriteFile(tmpFile, []byte("not valid json"), 0644)
		if err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		_, err = readTokenFile(tmpFile)
		if err == nil {
			t.Error("expected error for invalid json")
		}
	})
}

// isAuthRequiredError checks if an error is an AuthRequiredError using errors.As.
func isAuthRequiredError(err error) bool {
	var authErr *AuthRequiredError
	return errors.As(err, &authErr)
}

func TestAuthAdapterConfig_NoSilentRefresh(t *testing.T) {
	t.Run("creates adapter with NoSilentRefresh enabled", func(t *testing.T) {
		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
			NoSilentRefresh: true,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		if !adapter.noSilentRefresh {
			t.Error("expected noSilentRefresh to be true")
		}
	})

	t.Run("creates adapter with NoSilentRefresh disabled by default", func(t *testing.T) {
		tmpDir := t.TempDir()
		adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
			TokenStorageDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("failed to create adapter: %v", err)
		}
		defer adapter.Close()

		if adapter.noSilentRefresh {
			t.Error("expected noSilentRefresh to be false by default")
		}
	})
}

func TestAuthAdapter_SetNoSilentRefresh(t *testing.T) {
	tmpDir := t.TempDir()
	adapter, err := NewAuthAdapterWithConfig(AuthAdapterConfig{
		TokenStorageDir: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer adapter.Close()

	// Initially false
	if adapter.noSilentRefresh {
		t.Error("expected noSilentRefresh to be false initially")
	}

	// Set to true
	adapter.SetNoSilentRefresh(true)
	if !adapter.noSilentRefresh {
		t.Error("expected noSilentRefresh to be true after setting")
	}

	// Set back to false
	adapter.SetNoSilentRefresh(false)
	if adapter.noSilentRefresh {
		t.Error("expected noSilentRefresh to be false after unsetting")
	}
}
