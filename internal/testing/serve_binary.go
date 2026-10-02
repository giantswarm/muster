package testing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	gotesting "testing"
	"time"
)

// serveBinaryVersionTimeout bounds `<binary> --version`.
const serveBinaryVersionTimeout = 10 * time.Second

// ServeBinarySource says how the serve binary was found.
type ServeBinarySource string

const (
	// ServeBinarySelf is the running executable: `muster test` serves its
	// scenarios with the build it is part of, whatever its file name.
	ServeBinarySelf ServeBinarySource = "this executable"
	// ServeBinaryPath is the first muster on PATH.
	ServeBinaryPath ServeBinarySource = "PATH"
	// ServeBinaryBuildOutput is a muster build output near the working
	// directory.
	ServeBinaryBuildOutput ServeBinarySource = "build output"
	// ServeBinaryBuilt is a muster built from the source in the working
	// directory.
	ServeBinaryBuilt ServeBinarySource = "built from source"
)

// ServeBinary is the muster binary a run's instances serve with.
type ServeBinary struct {
	// Path is the binary's absolute path.
	Path string `yaml:"path"`
	// Version is the binary's `--version`, or why it is unknown.
	Version string `yaml:"version"`
	// Source says how the binary was found.
	Source ServeBinarySource `yaml:"source"`
}

// String renders the serve binary for the run header.
func (b ServeBinary) String() string {
	return fmt.Sprintf("%s (%s, %s)", b.Path, b.Version, b.Source)
}

// ServeBinary returns the muster binary the instances serve with, resolved
// once per manager.
func (m *musterInstanceManager) ServeBinary() (ServeBinary, error) {
	m.serveBinaryOnce.Do(func() {
		m.serveBinary, m.serveBinaryErr = m.resolveServeBinary()
	})
	return m.serveBinary, m.serveBinaryErr
}

// getMusterBinaryPath returns the path of the muster binary the instances run.
func (m *musterInstanceManager) getMusterBinaryPath() (string, error) {
	binary, err := m.ServeBinary()
	return binary.Path, err
}

// resolveServeBinary finds the muster binary the instances run.
//
// The running executable comes first when it is the muster program, whatever
// its file name: `muster test` then tests the build it is part of, never a
// stale `go install` on PATH. A `go test` binary is not muster; it falls back
// to the PATH lookup, the checkout's build outputs and a build from source.
func (m *musterInstanceManager) resolveServeBinary() (ServeBinary, error) {
	path, source, err := m.findServeBinary()
	if err != nil {
		return ServeBinary{}, err
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return ServeBinary{Path: path, Version: serveBinaryVersion(path), Source: source}, nil
}

func (m *musterInstanceManager) findServeBinary() (string, ServeBinarySource, error) {
	if executable, err := os.Executable(); err == nil {
		if path, ok := selfServeBinary(executable, gotesting.Testing()); ok {
			return path, ServeBinarySelf, nil
		}
	}

	if path, err := exec.LookPath("muster"); err == nil {
		return path, ServeBinaryPath, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("failed to get current directory: %w", err)
	}

	for _, path := range []string{
		filepath.Join(cwd, "muster"),
		filepath.Join(cwd, "bin", "muster"),
		filepath.Join(cwd, "..", "muster"),
		filepath.Join(cwd, "..", "bin", "muster"),
	} {
		if fileExists(path) {
			return path, ServeBinaryBuildOutput, nil
		}
	}

	if isInMusterSource(cwd) {
		if m.debug {
			m.logger.Debug("🔨 Building muster binary from source\n")
		}
		buildCmd := exec.Command("go", "build", "-o", "muster", ".")
		buildCmd.Dir = cwd
		if err := buildCmd.Run(); err != nil {
			return "", "", fmt.Errorf("failed to build muster: %w", err)
		}
		if builtPath := filepath.Join(cwd, "muster"); fileExists(builtPath) {
			return builtPath, ServeBinaryBuilt, nil
		}
	}

	return "", "", fmt.Errorf("muster binary not found")
}

// selfServeBinary returns the running executable when it is the muster
// program: this package is linked into no other program, so every caller but
// a `go test` binary is muster, under any file name.
func selfServeBinary(executable string, goTestBinary bool) (string, bool) {
	if goTestBinary {
		return "", false
	}
	return executable, true
}

// serveBinaryVersion returns the version `<path> --version` reports.
func serveBinaryVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), serveBinaryVersionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output() //nolint:gosec
	if err != nil {
		return fmt.Sprintf("version unknown: %v", err)
	}
	return parseMusterVersion(string(out))
}

// parseMusterVersion extracts the version from `muster --version` output
// ("muster version <v>").
func parseMusterVersion(out string) string {
	out = strings.TrimSpace(out)
	if v, ok := strings.CutPrefix(out, "muster version "); ok {
		return v
	}
	return out
}

// isInMusterSource reports whether dir is the muster source root.
func isInMusterSource(dir string) bool {
	for _, marker := range []string{"main.go", "go.mod", "cmd/serve.go"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
			return false
		}
	}
	return true
}
