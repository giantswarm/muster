package cli

import (
	"os"
	"testing"

	musterctx "muster/internal/context"
)

func TestResolveEndpoint_ExplicitEndpoint(t *testing.T) {
	// Explicit endpoint should always win
	endpoint, err := ResolveEndpoint("https://explicit.example.com/mcp", "")
	if err != nil {
		t.Fatalf("ResolveEndpoint failed: %v", err)
	}
	if endpoint != "https://explicit.example.com/mcp" {
		t.Errorf("expected explicit endpoint, got %q", endpoint)
	}
}

func TestResolveEndpoint_ContextFlag(t *testing.T) {
	tmpDir := t.TempDir()
	storage := musterctx.NewStorageWithPath(tmpDir)

	// Add a context
	_ = storage.AddContext("prod", "https://prod.example.com/mcp", nil)

	// Save the original NewStorage function behavior by setting up a temp home
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create the .config/muster directory structure
	_ = os.MkdirAll(tmpDir+"/.config/muster", 0755)

	// Copy the contexts.yaml to the expected location
	config, _ := storage.Load()
	storage2 := musterctx.NewStorageWithPath(tmpDir + "/.config/muster")
	_ = storage2.Save(config)

	// Test resolving with context flag
	endpoint, err := ResolveEndpoint("", "prod")
	if err != nil {
		t.Fatalf("ResolveEndpoint failed: %v", err)
	}
	if endpoint != "https://prod.example.com/mcp" {
		t.Errorf("expected 'https://prod.example.com/mcp', got %q", endpoint)
	}
}

func TestResolveEndpoint_EnvVar(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the .config/muster directory structure
	configDir := tmpDir + "/.config/muster"
	_ = os.MkdirAll(configDir, 0755)

	storage := musterctx.NewStorageWithPath(configDir)
	_ = storage.AddContext("staging", "https://staging.example.com/mcp", nil)

	// Set up HOME to point to our temp directory
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Set the environment variable
	os.Setenv("MUSTER_CONTEXT", "staging")
	defer os.Unsetenv("MUSTER_CONTEXT")

	// Test resolving with env var
	endpoint, err := ResolveEndpoint("", "")
	if err != nil {
		t.Fatalf("ResolveEndpoint failed: %v", err)
	}
	if endpoint != "https://staging.example.com/mcp" {
		t.Errorf("expected 'https://staging.example.com/mcp', got %q", endpoint)
	}
}

func TestResolveEndpoint_CurrentContext(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the .config/muster directory structure
	configDir := tmpDir + "/.config/muster"
	_ = os.MkdirAll(configDir, 0755)

	storage := musterctx.NewStorageWithPath(configDir)
	_ = storage.AddContext("current", "https://current.example.com/mcp", nil)
	_ = storage.SetCurrentContext("current")

	// Set up HOME to point to our temp directory
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Make sure no env var is set
	os.Unsetenv("MUSTER_CONTEXT")

	// Test resolving with current context
	endpoint, err := ResolveEndpoint("", "")
	if err != nil {
		t.Fatalf("ResolveEndpoint failed: %v", err)
	}
	if endpoint != "https://current.example.com/mcp" {
		t.Errorf("expected 'https://current.example.com/mcp', got %q", endpoint)
	}
}

func TestResolveEndpoint_NoContext(t *testing.T) {
	tmpDir := t.TempDir()

	// Set up HOME to point to an empty temp directory
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Make sure no env var is set
	os.Unsetenv("MUSTER_CONTEXT")

	// Test resolving with no context - should return empty string
	endpoint, err := ResolveEndpoint("", "")
	if err != nil {
		t.Fatalf("ResolveEndpoint failed: %v", err)
	}
	if endpoint != "" {
		t.Errorf("expected empty string for fallback, got %q", endpoint)
	}
}

func TestResolveEndpoint_Precedence(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the .config/muster directory structure
	configDir := tmpDir + "/.config/muster"
	_ = os.MkdirAll(configDir, 0755)

	storage := musterctx.NewStorageWithPath(configDir)
	_ = storage.AddContext("current", "https://current.example.com/mcp", nil)
	_ = storage.AddContext("env", "https://env.example.com/mcp", nil)
	_ = storage.AddContext("flag", "https://flag.example.com/mcp", nil)
	_ = storage.SetCurrentContext("current")

	// Set up HOME to point to our temp directory
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Set the environment variable
	os.Setenv("MUSTER_CONTEXT", "env")
	defer os.Unsetenv("MUSTER_CONTEXT")

	// Test 1: Explicit endpoint wins over everything
	endpoint, _ := ResolveEndpoint("https://explicit.example.com/mcp", "flag")
	if endpoint != "https://explicit.example.com/mcp" {
		t.Errorf("expected explicit endpoint to win, got %q", endpoint)
	}

	// Test 2: Context flag wins over env var and current context
	endpoint, _ = ResolveEndpoint("", "flag")
	if endpoint != "https://flag.example.com/mcp" {
		t.Errorf("expected flag context to win, got %q", endpoint)
	}

	// Test 3: Env var wins over current context
	endpoint, _ = ResolveEndpoint("", "")
	if endpoint != "https://env.example.com/mcp" {
		t.Errorf("expected env var context to win, got %q", endpoint)
	}

	// Test 4: Current context is used when nothing else is set
	os.Unsetenv("MUSTER_CONTEXT")
	endpoint, _ = ResolveEndpoint("", "")
	if endpoint != "https://current.example.com/mcp" {
		t.Errorf("expected current context, got %q", endpoint)
	}
}

func TestContextNotFoundError(t *testing.T) {
	tmpDir := t.TempDir()

	// Set up HOME to point to an empty temp directory
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create empty config dir
	_ = os.MkdirAll(tmpDir+"/.config/muster", 0755)

	// Try to resolve a non-existent context
	_, err := ResolveEndpoint("", "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent context")
	}

	// Check it's a ContextNotFoundError
	var ctxErr *musterctx.ContextNotFoundError
	if !isContextNotFoundError(err) {
		t.Errorf("expected ContextNotFoundError, got %T: %v", err, err)
	}
	_ = ctxErr // silence unused variable warning
}

func isContextNotFoundError(err error) bool {
	_, ok := err.(*musterctx.ContextNotFoundError)
	return ok
}

func TestGetContextSettings(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the .config/muster directory structure
	configDir := tmpDir + "/.config/muster"
	_ = os.MkdirAll(configDir, 0755)

	storage := musterctx.NewStorageWithPath(configDir)
	_ = storage.AddContext("with-settings", "https://example.com/mcp", &musterctx.ContextSettings{Output: "json"})
	_ = storage.AddContext("no-settings", "https://example.com/mcp", nil)
	_ = storage.SetCurrentContext("with-settings")

	// Set up HOME to point to our temp directory
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Make sure no env var is set
	os.Unsetenv("MUSTER_CONTEXT")

	// Test getting settings from current context
	settings := GetContextSettings("")
	if settings == nil {
		t.Fatal("expected settings, got nil")
	}
	if settings.Output != "json" {
		t.Errorf("expected output 'json', got %q", settings.Output)
	}

	// Test getting settings from named context
	settings = GetContextSettings("no-settings")
	if settings != nil {
		t.Errorf("expected nil settings, got %+v", settings)
	}
}
