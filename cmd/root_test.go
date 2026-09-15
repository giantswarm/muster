package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/giantswarm/muster/v5/pkg/project"
)

func TestVersionIsTheProjectVersionLine(t *testing.T) {
	if got, want := rootCmd.Version, project.VersionLine(); got != want {
		t.Errorf("rootCmd.Version = %q, want %q", got, want)
	}
}

// The hint that a newer release exists is for the commands a person runs at a
// terminal, not for the processes and the plumbing.
func TestRemindsOfNewerRelease(t *testing.T) {
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()
	byPath := map[string]*cobra.Command{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			byPath[sub.CommandPath()] = sub
			walk(sub)
		}
	}
	walk(rootCmd)
	for path, want := range map[string]bool{
		"muster list":            true,
		"muster call":            true,
		"muster auth login":      true,
		"muster serve":           false,
		"muster standalone":      false,
		"muster agent":           false,
		"muster test":            false,
		"muster version":         false,
		"muster self-update":     false,
		"muster help":            false,
		"muster completion":      false,
		"muster completion bash": false,
	} {
		c, ok := byPath[path]
		if !ok {
			t.Errorf("no command %q registered", path)
			continue
		}
		if got := remindsOfNewerRelease(c); got != want {
			t.Errorf("remindsOfNewerRelease(%s) = %v, want %v", path, got, want)
		}
	}
}

func TestRootCommand(t *testing.T) {
	// Test root command properties
	if rootCmd.Use != "muster" {
		t.Errorf("Expected Use to be 'muster', got %s", rootCmd.Use)
	}

	if rootCmd.Short == "" {
		t.Error("Expected Short description to be set")
	}

	if rootCmd.Long == "" {
		t.Error("Expected Long description to be set")
	}

	if !rootCmd.SilenceUsage {
		t.Error("Expected SilenceUsage to be true")
	}
}

func TestVersionTemplate(t *testing.T) {
	// Create a new command to test version template
	testCmd := &cobra.Command{
		Use:     "test",
		Version: "1.0.0",
	}

	// Set the same version template as in Execute()
	testCmd.SetVersionTemplate(`{{printf "muster version %s\n" .Version}}`)

	// Capture output
	var buf bytes.Buffer
	testCmd.SetOut(&buf)

	// Execute version command
	testCmd.SetArgs([]string{"--version"})
	err := testCmd.Execute()
	if err != nil {
		t.Fatalf("Error executing version command: %v", err)
	}

	output := buf.String()
	expected := "muster version 1.0.0\n"
	if output != expected {
		t.Errorf("Expected version output %q, got %q", expected, output)
	}
}

func TestSubcommands(t *testing.T) {
	// Test that subcommands are added
	commands := rootCmd.Commands()

	expectedCommands := []string{"version", "self-update", "serve"}
	foundCommands := make(map[string]bool)

	for _, cmd := range commands {
		foundCommands[cmd.Name()] = true
	}

	for _, expected := range expectedCommands {
		if !foundCommands[expected] {
			t.Errorf("Expected subcommand %s to be registered", expected)
		}
	}
}

func TestRootCommandHelp(t *testing.T) {
	// The root help is the first documentation a user reads and the source of
	// docs/reference/cli/README.md, so it must render and describe what muster
	// is today rather than a historical purpose.
	var buf bytes.Buffer
	root := RootCommand()
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	defer root.SetArgs(nil)

	if err := root.Execute(); err != nil {
		t.Fatalf("Error executing help command: %v", err)
	}

	output := buf.String()
	for _, want := range []string{
		"aggregates the tools of many MCP servers",
		"muster serve",
		"https://giantswarm.github.io/muster/",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Help output should contain %q. Got: %q", want, output)
		}
	}
	for _, stale := range []string{"Giant Swarm clusters", "port-forwarding"} {
		if strings.Contains(output, stale) {
			t.Errorf("Help output still carries the stale phrase %q", stale)
		}
	}
}
