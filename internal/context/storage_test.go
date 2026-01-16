package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorage_CRUD(t *testing.T) {
	// Create temp directory for testing
	tmpDir := t.TempDir()
	storage := NewStorageWithPath(tmpDir)

	// Test initial empty state
	t.Run("initial state is empty", func(t *testing.T) {
		contexts, err := storage.ListContexts()
		if err != nil {
			t.Fatalf("ListContexts failed: %v", err)
		}
		if len(contexts) != 0 {
			t.Errorf("expected 0 contexts, got %d", len(contexts))
		}
	})

	// Test AddContext
	t.Run("add context", func(t *testing.T) {
		err := storage.AddContext("prod", "https://prod.example.com/mcp", nil)
		if err != nil {
			t.Fatalf("AddContext failed: %v", err)
		}

		ctx, err := storage.GetContext("prod")
		if err != nil {
			t.Fatalf("GetContext failed: %v", err)
		}
		if ctx == nil {
			t.Fatal("expected context, got nil")
		}
		if ctx.Endpoint != "https://prod.example.com/mcp" {
			t.Errorf("got endpoint %q, want %q", ctx.Endpoint, "https://prod.example.com/mcp")
		}
	})

	// Test duplicate context
	t.Run("add duplicate context fails", func(t *testing.T) {
		err := storage.AddContext("prod", "https://other.example.com/mcp", nil)
		if err == nil {
			t.Error("expected error for duplicate context")
		}
	})

	// Test SetCurrentContext
	t.Run("set current context", func(t *testing.T) {
		err := storage.SetCurrentContext("prod")
		if err != nil {
			t.Fatalf("SetCurrentContext failed: %v", err)
		}

		name, err := storage.GetCurrentContextName()
		if err != nil {
			t.Fatalf("GetCurrentContextName failed: %v", err)
		}
		if name != "prod" {
			t.Errorf("got current context %q, want %q", name, "prod")
		}
	})

	// Test SetCurrentContext with non-existent context
	t.Run("set non-existent current context fails", func(t *testing.T) {
		err := storage.SetCurrentContext("staging")
		if err == nil {
			t.Error("expected error for non-existent context")
		}
	})

	// Test UpdateContext
	t.Run("update context", func(t *testing.T) {
		err := storage.UpdateContext("prod", "https://new-prod.example.com/mcp", &ContextSettings{Output: "json"})
		if err != nil {
			t.Fatalf("UpdateContext failed: %v", err)
		}

		ctx, err := storage.GetContext("prod")
		if err != nil {
			t.Fatalf("GetContext failed: %v", err)
		}
		if ctx.Endpoint != "https://new-prod.example.com/mcp" {
			t.Errorf("got endpoint %q, want %q", ctx.Endpoint, "https://new-prod.example.com/mcp")
		}
		if ctx.Settings == nil || ctx.Settings.Output != "json" {
			t.Error("expected settings to be updated")
		}
	})

	// Test RenameContext
	t.Run("rename context", func(t *testing.T) {
		err := storage.RenameContext("prod", "production")
		if err != nil {
			t.Fatalf("RenameContext failed: %v", err)
		}

		// Old name should not exist
		ctx, _ := storage.GetContext("prod")
		if ctx != nil {
			t.Error("expected old context name to not exist")
		}

		// New name should exist
		ctx, err = storage.GetContext("production")
		if err != nil {
			t.Fatalf("GetContext failed: %v", err)
		}
		if ctx == nil {
			t.Fatal("expected new context to exist")
		}

		// Current context should be updated
		name, _ := storage.GetCurrentContextName()
		if name != "production" {
			t.Errorf("expected current context to be updated to %q, got %q", "production", name)
		}
	})

	// Test DeleteContext
	t.Run("delete context", func(t *testing.T) {
		err := storage.DeleteContext("production")
		if err != nil {
			t.Fatalf("DeleteContext failed: %v", err)
		}

		ctx, _ := storage.GetContext("production")
		if ctx != nil {
			t.Error("expected context to be deleted")
		}

		// Current context should be cleared
		name, _ := storage.GetCurrentContextName()
		if name != "" {
			t.Errorf("expected current context to be cleared, got %q", name)
		}
	})

	// Test delete non-existent context
	t.Run("delete non-existent context fails", func(t *testing.T) {
		err := storage.DeleteContext("does-not-exist")
		if err == nil {
			t.Error("expected error for non-existent context")
		}
	})
}

func TestStorage_Validation(t *testing.T) {
	tmpDir := t.TempDir()
	storage := NewStorageWithPath(tmpDir)

	t.Run("invalid context name", func(t *testing.T) {
		err := storage.AddContext("Invalid_Name", "https://example.com/mcp", nil)
		if err == nil {
			t.Error("expected error for invalid context name")
		}
	})

	t.Run("empty endpoint", func(t *testing.T) {
		err := storage.AddContext("valid", "", nil)
		if err == nil {
			t.Error("expected error for empty endpoint")
		}
	})
}

func TestStorage_GetContextNames(t *testing.T) {
	tmpDir := t.TempDir()
	storage := NewStorageWithPath(tmpDir)

	// Add some contexts
	_ = storage.AddContext("prod", "https://prod.example.com/mcp", nil)
	_ = storage.AddContext("dev", "https://dev.example.com/mcp", nil)
	_ = storage.AddContext("staging", "https://staging.example.com/mcp", nil)

	names, err := storage.GetContextNames()
	if err != nil {
		t.Fatalf("GetContextNames failed: %v", err)
	}

	if len(names) != 3 {
		t.Errorf("expected 3 names, got %d", len(names))
	}

	// Check all names are present (order may vary)
	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	for _, expected := range []string{"prod", "dev", "staging"} {
		if !nameSet[expected] {
			t.Errorf("expected name %q not found", expected)
		}
	}
}

func TestStorage_FileFormat(t *testing.T) {
	tmpDir := t.TempDir()
	storage := NewStorageWithPath(tmpDir)

	// Add a context with settings
	_ = storage.AddContext("prod", "https://prod.example.com/mcp", &ContextSettings{Output: "json"})
	_ = storage.SetCurrentContext("prod")

	// Read the file and verify format
	data, err := os.ReadFile(filepath.Join(tmpDir, "contexts.yaml"))
	if err != nil {
		t.Fatalf("failed to read contexts file: %v", err)
	}

	content := string(data)
	// Check key fields are present
	if !strings.Contains(content, "current-context: prod") {
		t.Error("expected current-context field in output")
	}
	if !strings.Contains(content, "name: prod") {
		t.Error("expected context name in output")
	}
	if !strings.Contains(content, "endpoint: https://prod.example.com/mcp") {
		t.Error("expected endpoint in output")
	}
	if !strings.Contains(content, "output: json") {
		t.Error("expected settings output in output")
	}
}
