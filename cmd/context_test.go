package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	musterctx "muster/internal/context"
)

func setupContextTestStorage(t *testing.T) (*musterctx.Storage, func()) {
	t.Helper()

	// Create temp directory for testing
	tmpDir := t.TempDir()

	// Create a storage with the temp directory
	storage := musterctx.NewStorageWithPath(tmpDir)

	return storage, func() {
		// Cleanup is automatic with t.TempDir()
	}
}

func TestContextListEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	storage := musterctx.NewStorageWithPath(tmpDir)

	contexts, err := storage.ListContexts()
	if err != nil {
		t.Fatalf("ListContexts failed: %v", err)
	}

	if len(contexts) != 0 {
		t.Errorf("expected 0 contexts, got %d", len(contexts))
	}
}

func TestContextAddAndList(t *testing.T) {
	tmpDir := t.TempDir()
	storage := musterctx.NewStorageWithPath(tmpDir)

	// Add contexts
	if err := storage.AddContext("prod", "https://prod.example.com/mcp", nil); err != nil {
		t.Fatalf("AddContext failed: %v", err)
	}
	if err := storage.AddContext("dev", "https://dev.example.com/mcp", nil); err != nil {
		t.Fatalf("AddContext failed: %v", err)
	}

	// List contexts
	contexts, err := storage.ListContexts()
	if err != nil {
		t.Fatalf("ListContexts failed: %v", err)
	}

	if len(contexts) != 2 {
		t.Errorf("expected 2 contexts, got %d", len(contexts))
	}
}

func TestContextUse(t *testing.T) {
	tmpDir := t.TempDir()
	storage := musterctx.NewStorageWithPath(tmpDir)

	// Add a context
	if err := storage.AddContext("prod", "https://prod.example.com/mcp", nil); err != nil {
		t.Fatalf("AddContext failed: %v", err)
	}

	// Set as current
	if err := storage.SetCurrentContext("prod"); err != nil {
		t.Fatalf("SetCurrentContext failed: %v", err)
	}

	// Verify
	name, err := storage.GetCurrentContextName()
	if err != nil {
		t.Fatalf("GetCurrentContextName failed: %v", err)
	}
	if name != "prod" {
		t.Errorf("expected current context 'prod', got %q", name)
	}
}

func TestContextDelete(t *testing.T) {
	tmpDir := t.TempDir()
	storage := musterctx.NewStorageWithPath(tmpDir)

	// Add and set context
	if err := storage.AddContext("prod", "https://prod.example.com/mcp", nil); err != nil {
		t.Fatalf("AddContext failed: %v", err)
	}
	if err := storage.SetCurrentContext("prod"); err != nil {
		t.Fatalf("SetCurrentContext failed: %v", err)
	}

	// Delete context
	if err := storage.DeleteContext("prod"); err != nil {
		t.Fatalf("DeleteContext failed: %v", err)
	}

	// Verify context is gone
	ctx, _ := storage.GetContext("prod")
	if ctx != nil {
		t.Error("expected context to be deleted")
	}

	// Verify current context is cleared
	name, _ := storage.GetCurrentContextName()
	if name != "" {
		t.Errorf("expected current context to be cleared, got %q", name)
	}
}

func TestContextRename(t *testing.T) {
	tmpDir := t.TempDir()
	storage := musterctx.NewStorageWithPath(tmpDir)

	// Add and set context
	if err := storage.AddContext("prod", "https://prod.example.com/mcp", nil); err != nil {
		t.Fatalf("AddContext failed: %v", err)
	}
	if err := storage.SetCurrentContext("prod"); err != nil {
		t.Fatalf("SetCurrentContext failed: %v", err)
	}

	// Rename context
	if err := storage.RenameContext("prod", "production"); err != nil {
		t.Fatalf("RenameContext failed: %v", err)
	}

	// Verify old name is gone
	ctx, _ := storage.GetContext("prod")
	if ctx != nil {
		t.Error("expected old context name to be gone")
	}

	// Verify new name exists
	ctx, _ = storage.GetContext("production")
	if ctx == nil {
		t.Error("expected new context to exist")
	}

	// Verify current context is updated
	name, _ := storage.GetCurrentContextName()
	if name != "production" {
		t.Errorf("expected current context 'production', got %q", name)
	}
}

func TestContextUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	storage := musterctx.NewStorageWithPath(tmpDir)

	// Add a context
	if err := storage.AddContext("prod", "https://prod.example.com/mcp", nil); err != nil {
		t.Fatalf("AddContext failed: %v", err)
	}

	// Update endpoint
	newEndpoint := "https://new-prod.example.com/mcp"
	if err := storage.UpdateContext("prod", newEndpoint, nil); err != nil {
		t.Fatalf("UpdateContext failed: %v", err)
	}

	// Verify update
	ctx, err := storage.GetContext("prod")
	if err != nil {
		t.Fatalf("GetContext failed: %v", err)
	}
	if ctx.Endpoint != newEndpoint {
		t.Errorf("expected endpoint %q, got %q", newEndpoint, ctx.Endpoint)
	}
}

func TestContextUpdateNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	storage := musterctx.NewStorageWithPath(tmpDir)

	// Try to update a nonexistent context
	err := storage.UpdateContext("nonexistent", "https://example.com/mcp", nil)
	if err == nil {
		t.Error("expected error when updating nonexistent context")
	}

	// Verify it's a ContextNotFoundError
	var notFoundErr *musterctx.ContextNotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Errorf("expected ContextNotFoundError, got %T", err)
	}
}

func TestContextNameValidation(t *testing.T) {
	tests := []struct {
		name      string
		wantError bool
	}{
		{"valid", false},
		{"with-hyphen", false},
		{"with123numbers", false},
		{"", true},
		{"UPPERCASE", true},
		{"with_underscore", true},
		{"with space", true},
		{"-starts-with-hyphen", true},
		{"ends-with-hyphen-", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := musterctx.ValidateContextName(tt.name)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateContextName(%q) error = %v, wantError %v", tt.name, err, tt.wantError)
			}
		})
	}
}

func TestContextFileFormat(t *testing.T) {
	tmpDir := t.TempDir()
	storage := musterctx.NewStorageWithPath(tmpDir)

	// Add a context with settings
	if err := storage.AddContext("prod", "https://prod.example.com/mcp", &musterctx.ContextSettings{Output: "json"}); err != nil {
		t.Fatalf("AddContext failed: %v", err)
	}
	if err := storage.SetCurrentContext("prod"); err != nil {
		t.Fatalf("SetCurrentContext failed: %v", err)
	}

	// Read the file
	data, err := os.ReadFile(filepath.Join(tmpDir, "contexts.yaml"))
	if err != nil {
		t.Fatalf("failed to read contexts file: %v", err)
	}

	content := string(data)

	// Verify key fields
	if !strings.Contains(content, "current-context: prod") {
		t.Error("expected 'current-context: prod' in file")
	}
	if !strings.Contains(content, "name: prod") {
		t.Error("expected 'name: prod' in file")
	}
	if !strings.Contains(content, "endpoint: https://prod.example.com/mcp") {
		t.Error("expected endpoint in file")
	}
}

func TestContextEnvVarOverride(t *testing.T) {
	tmpDir := t.TempDir()
	storage := musterctx.NewStorageWithPath(tmpDir)

	// Add contexts
	_ = storage.AddContext("prod", "https://prod.example.com/mcp", nil)
	_ = storage.AddContext("dev", "https://dev.example.com/mcp", nil)
	_ = storage.SetCurrentContext("prod")

	// Test that env var is read by the ResolveEndpoint function
	// This tests the integration point
	os.Setenv("MUSTER_CONTEXT", "dev")
	defer os.Unsetenv("MUSTER_CONTEXT")

	// The actual test would need to use the cli package's ResolveEndpoint function
	// which is tested in internal/cli/context_test.go
}
