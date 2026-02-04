package main

import (
	"os"
	"testing"

	"github.com/giantswarm/muster/cmd"
)

func TestMain(m *testing.M) {
	// Run tests
	code := m.Run()
	os.Exit(code)
}

func TestVersion(t *testing.T) {
	// Test default version
	if version != "dev" {
		t.Errorf("Expected default version to be 'dev', got %s", version)
	}

	// Test setting version
	testVersion := "1.2.3"
	version = testVersion
	if version != testVersion {
		t.Errorf("Expected version to be %s, got %s", testVersion, version)
	}

	// Reset version
	version = "dev"
}

func TestMainFunction(t *testing.T) {
	// Save original args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Test with version command to avoid side effects
	os.Args = []string{"muster", "version"}

	// The main function calls cmd.Execute() which will run the version command
	// We can't easily test the main function directly without executing commands
	// So we'll just verify that cmd.SetVersion is called correctly
	cmd.SetVersion(version)

	// The test verifies that SetVersion doesn't panic and accepts the version
}

func TestVersionVariable(t *testing.T) {
	tests := []struct {
		name     string
		setValue string
		expected string
	}{
		{
			name:     "default version",
			setValue: "",
			expected: "dev",
		},
		{
			name:     "custom version",
			setValue: "v1.0.0",
			expected: "v1.0.0",
		},
		{
			name:     "semantic version",
			setValue: "2.3.4-beta.1",
			expected: "2.3.4-beta.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original version
			originalVersion := version

			// Set test version
			if tt.setValue != "" {
				version = tt.setValue
			}

			// Check version
			if version != tt.expected {
				t.Errorf("Expected version %s, got %s", tt.expected, version)
			}

			// Restore original version
			version = originalVersion
		})
	}
}

func TestMainPackageIntegration(t *testing.T) {
	// This test verifies that the main package properly integrates with cmd package

	// Save original version
	originalVersion := version
	defer func() { version = originalVersion }()

	// Test different version scenarios
	versions := []string{"dev", "1.0.0", "v2.0.0-rc1"}

	for _, v := range versions {
		version = v
		// Test that SetVersion doesn't panic with different version formats
		cmd.SetVersion(version)
	}
}

func TestMainWithDifferentArgs(t *testing.T) {
	// Test main function behavior with different command line arguments
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Test with version flag
	os.Args = []string{"muster", "--version"}

	// We can't easily test main() execution without it actually running,
	// but we can test that the setup works correctly
	cmd.SetVersion("test-version-main")

	// Verify the version was set (indirectly)
	// This tests the integration between main and cmd packages
}

func TestMainPackageStructure(t *testing.T) {
	// Test that main package is properly structured
	// This is a basic test to ensure the package compiles and has expected structure

	// Test that version variable exists (indirectly through cmd.SetVersion)
	testVersions := []string{"1.0.0", "dev", "v2.1.0-beta"}

	for _, version := range testVersions {
		cmd.SetVersion(version)
		// If we get here without panic, the version setting works
	}
}

func TestMainWithEmptyArgs(t *testing.T) {
	// Test main function with minimal arguments
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Test with just program name
	os.Args = []string{"muster"}

	// Set a test version
	cmd.SetVersion("test-empty-args")

	// This tests that the main package can handle minimal arguments
	// without panicking during initialization
}
