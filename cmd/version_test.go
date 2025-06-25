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
	expected := "muster version " + testVersion + "\n"
	if output != expected {
		t.Errorf("Expected output %q, got %q", expected, output)
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
	versionCmd.SetArgs([]string{"--help"})

	err := versionCmd.Execute()
	if err != nil {
		t.Fatalf("Error executing version help: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "All software has versions") {
		t.Errorf("Help output should contain description. Got: %q", output)
	}
}
