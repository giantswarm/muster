package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewVersionCmd(t *testing.T) {
	// Test version command creation
	versionCmd := newVersionCmd()

	if versionCmd.Use != "version" {
		t.Errorf("Expected Use to be 'version', got %s", versionCmd.Use)
	}

	if versionCmd.Short == "" {
		t.Error("Expected Short description to be set")
	}

	if versionCmd.Long == "" {
		t.Error("Expected Long description to be set")
	}

	if versionCmd.Run == nil {
		t.Error("Expected Run function to be set")
	}
}

func TestVersionCommandExecution(t *testing.T) {
	// Set a test version
	testVersion := "1.2.3-test"
	originalVersion := rootCmd.Version
	defer func() { rootCmd.Version = originalVersion }()
	rootCmd.Version = testVersion

	// Create version command
	versionCmd := newVersionCmd()

	// Capture output - need to capture stdout since the command prints directly
	var buf bytes.Buffer
	versionCmd.SetOut(&buf)

	// Execute the command's Run function directly to capture output
	versionCmd.Run(versionCmd, []string{})

	output := buf.String()

	// Check that CLI version is printed
	if !strings.Contains(output, "muster version "+testVersion) {
		t.Errorf("Expected output to contain CLI version, got %q", output)
	}

	// Check that server status is present (either "Server: <version>" or "Server: (not running)")
	if !strings.Contains(output, "Server:") {
		t.Errorf("Expected output to contain server status, got %q", output)
	}
}

func TestVersionCommandWithEmptyVersion(t *testing.T) {
	// Test with empty version
	originalVersion := rootCmd.Version
	defer func() { rootCmd.Version = originalVersion }()

	rootCmd.Version = ""

	versionCmd := newVersionCmd()
	var buf bytes.Buffer
	versionCmd.SetOut(&buf)

	// Execute the command's Run function directly
	versionCmd.Run(versionCmd, []string{})

	output := buf.String()
	if !strings.Contains(output, "muster version") {
		t.Error("Output should contain 'muster version' even with empty version")
	}
}

func TestVersionCommandHelp(t *testing.T) {
	// Test version command help
	versionCmd := newVersionCmd()
	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	versionCmd.SetErr(&buf) // Also capture stderr for help

	// Use --help flag
	versionCmd.SetArgs([]string{"--help"})

	err := versionCmd.Execute()
	if err != nil {
		t.Fatalf("Error executing version help: %v", err)
	}

	output := buf.String()
	// Updated to match the new Long description
	if !strings.Contains(output, "CLI version") {
		t.Errorf("Help output should contain description about CLI version. Got: %q", output)
	}
}

func TestGetServerVersion_ReturnsInfo(t *testing.T) {
	// This test verifies getServerVersion handles both running and not-running states.
	// Note: This is an environment-dependent test - behavior depends on whether
	// the muster server is actually running. For deterministic testing, consider
	// using integration tests with controlled server lifecycle.
	version, name, err := getServerVersion()

	// If server is running, we should get valid info
	if err == nil {
		if version == "" {
			t.Error("Expected non-empty version when server is running")
		}
		if name == "" {
			t.Error("Expected non-empty name when server is running")
		}
	}
	// If server is not running, error should be returned and values empty
	if err != nil {
		if version != "" {
			t.Errorf("Expected empty version when server error occurs, got %q", version)
		}
		if name != "" {
			t.Errorf("Expected empty name when server error occurs, got %q", name)
		}
	}
}
