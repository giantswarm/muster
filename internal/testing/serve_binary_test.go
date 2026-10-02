package testing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfServeBinary(t *testing.T) {
	for _, tc := range []struct {
		name         string
		executable   string
		goTestBinary bool
		wantOK       bool
	}{
		{"muster", filepath.Join("usr", "local", "bin", "muster"), false, true},
		{"renamed muster", filepath.Join("bin", "muster-pr123"), false, true},
		{"renamed in a worktree", filepath.Join("wt", "muster-dev"), false, true},
		{"go test caller", filepath.Join("tmp", "testing.test"), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := selfServeBinary(tc.executable, tc.goTestBinary)
			if ok != tc.wantOK {
				t.Fatalf("selfServeBinary(%q, %v) ok = %v, want %v", tc.executable, tc.goTestBinary, ok, tc.wantOK)
			}
			if ok && got != tc.executable {
				t.Errorf("selfServeBinary(%q, %v) = %q, want the executable", tc.executable, tc.goTestBinary, got)
			}
		})
	}
}

// TestServeBinaryGoTestFallsBackToPATH pins the `go test` caller's fallback:
// a test binary is not muster, so the first muster on PATH serves, and the
// header names its absolute path, version and source.
func TestServeBinaryGoTestFallsBackToPATH(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "muster")
	script := "#!/bin/sh\necho 'muster version v9.9.9-fake'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	m := &musterInstanceManager{logger: NewSilentLogger(false, false)}
	got, err := m.ServeBinary()
	if err != nil {
		t.Fatalf("ServeBinary: %v", err)
	}
	want := ServeBinary{Path: fake, Version: "v9.9.9-fake", Source: ServeBinaryPath}
	if got != want {
		t.Errorf("ServeBinary() = %+v, want %+v", got, want)
	}
	if path, err := m.getMusterBinaryPath(); err != nil || path != fake {
		t.Errorf("getMusterBinaryPath() = %q, %v, want %q", path, err, fake)
	}
}

func TestParseMusterVersion(t *testing.T) {
	for out, want := range map[string]string{
		"muster version v5.12.0\n": "v5.12.0",
		"muster version dev":       "dev",
		"something else\n":         "something else",
	} {
		if got := parseMusterVersion(out); got != want {
			t.Errorf("parseMusterVersion(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestServeBinaryString(t *testing.T) {
	b := ServeBinary{Path: "/usr/bin/muster-dev", Version: "v5.12.0", Source: ServeBinarySelf}
	if got, want := b.String(), "/usr/bin/muster-dev (v5.12.0, this executable)"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
