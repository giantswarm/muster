package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Without HOME there is no default configuration directory: the binary still
// starts and runs a command that needs no configuration, and a command that
// needs it fails with an error naming --config-path instead of a panic.
func TestRunsWithoutHome(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	binary := buildMuster(t)
	env := []string{"TMPDIR=" + t.TempDir()}

	version := exec.Command(binary, "version")
	version.Env = env
	out, err := version.CombinedOutput()
	if err != nil {
		t.Fatalf("muster version without HOME: %v\n%s", err, out)
	}
	if !strings.HasPrefix(string(out), "muster version ") {
		t.Fatalf("unexpected output:\n%s", out)
	}

	list := exec.Command(binary, "list", "mcpserver")
	list.Env = env
	out, err = list.CombinedOutput()
	if err == nil {
		t.Fatalf("muster list without HOME and --config-path succeeded:\n%s", out)
	}
	if strings.Contains(string(out), "panic") || !strings.Contains(string(out), "--config-path") {
		t.Fatalf("muster list without HOME: want an error naming --config-path, got:\n%s", out)
	}
}
