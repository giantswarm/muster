package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The release image is scratch: no /tmp, no HOME. A read-only command must not
// depend on either, so a dependency whose package init creates a cache or
// temporary directory (controller-runtime's envtest does) must not be linked
// into the binary; it panicked here before the envtest build tag.
func TestVersionRunsWithoutAWritableTmp(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	dir := t.TempDir()
	binary := buildMuster(t)

	// A regular file as HOME and TMPDIR makes every mkdir below them fail, root included.
	notADir := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "version")
	cmd.Env = []string{"HOME=" + notADir, "TMPDIR=" + notADir, "MUSTER_AGGREGATOR_ENDPOINT=http://127.0.0.1:1/mcp"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("muster version without a writable /tmp: %v\n%s", err, out)
	}
	if !strings.HasPrefix(string(out), "muster version ") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

// buildMuster builds the release binary (CGO disabled, like the image) into a
// temporary directory and returns its path.
func buildMuster(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "muster")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return binary
}
