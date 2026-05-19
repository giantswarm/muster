//go:build linux

// Package integration holds slow end-to-end tests that exercise the muster
// binary as a real process. They are skipped unless the runner exposes
// MUSTER_BINARY (the muster build under test) and MUSTER_AGW_BINARY (the
// pinned agentgateway binary that internal/agentgateway/binary.Resolve
// short-circuits to).
package integration

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// envMusterBinary names the muster binary under test. The path must be
// executable and resolve `serve --config-path <dir>`.
const envMusterBinary = "MUSTER_BINARY"

// envAgwBinary names the agentgateway binary internal/agentgateway/binary
// resolves to. The integration test sets it in muster's environment so the
// resolver does not attempt a network download from the CI runner.
const envAgwBinary = "MUSTER_AGW_BINARY"

// readyEndpoint is agentgateway's standard readiness probe target. The
// muster subprocess polls the same URL before reporting startup success.
const readyEndpoint = "http://127.0.0.1:15021/healthz/ready"

func TestAgentgatewaySubprocessTopology(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; skipped in -short mode")
	}
	musterBin := os.Getenv(envMusterBinary)
	if musterBin == "" {
		t.Skipf("%s not set; build muster and export the path to run this test", envMusterBinary)
	}
	agwBin := os.Getenv(envAgwBinary)
	if agwBin == "" {
		t.Skipf("%s not set; download agentgateway-v1.2.1 and export the path to run this test", envAgwBinary)
	}
	requireExecutable(t, musterBin)
	requireExecutable(t, agwBin)

	configDir := writeFilesystemModeConfig(t)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, musterBin, "serve", "--config-path", configDir) //nolint:gosec // path is an env-supplied test fixture, not user input
	cmd.Env = append(os.Environ(), envAgwBinary+"="+agwBin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stderr, err := cmd.StderrPipe()
	require.NoError(t, err)
	t.Cleanup(func() { drainAndLog(t, stdout, "muster-stdout") })
	t.Cleanup(func() { drainAndLog(t, stderr, "muster-stderr") })

	require.NoError(t, cmd.Start())
	musterPID := cmd.Process.Pid
	t.Logf("muster pid=%d", musterPID)

	t.Cleanup(func() {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return
		}
		_ = syscall.Kill(-musterPID, syscall.SIGKILL)
	})

	require.Eventually(t, func() bool { return probeReady(ctx) },
		30*time.Second, 250*time.Millisecond,
		"agentgateway readiness endpoint never came up; muster pid=%d", musterPID)

	agwPID, err := findAgentgatewayChild(musterPID, agwBin)
	require.NoError(t, err, "agentgateway must be a direct child of muster")
	t.Logf("agentgateway pid=%d (ppid=%d)", agwPID, musterPID)
	require.NotEqual(t, musterPID, agwPID, "agentgateway pid must be distinct from muster pid")

	// SIGTERM to muster's process group so Application.Run's signal handler
	// drives the shutdown ordering under test (reconciler → agw → orch).
	require.NoError(t, syscall.Kill(-musterPID, syscall.SIGTERM))

	require.Eventually(t, func() bool { return !pidAlive(musterPID) },
		20*time.Second, 100*time.Millisecond,
		"muster must exit after SIGTERM")
	require.Eventually(t, func() bool { return !pidAlive(agwPID) },
		10*time.Second, 100*time.Millisecond,
		"agentgateway must exit when muster's shutdown sequence stops the subprocess manager")

	waitErr := cmd.Wait()
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !asExitError(waitErr, &exitErr) {
			t.Fatalf("muster Wait failed: %v", waitErr)
		}
	}
}

func writeFilesystemModeConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "mcpservers"), 0o750))
	configYAML := []byte(`aggregator:
  host: 127.0.0.1
  port: 0
  transport: streamable-http
kubernetes: false
`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), configYAML, 0o600))
	return dir
}

func requireExecutable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path) //nolint:gosec // path is an env-supplied test fixture, not user input
	require.NoError(t, err, "%s must exist", path)
	require.Falsef(t, info.IsDir(), "%s must be a file, not a directory", path)
	require.NotZero(t, info.Mode()&0o111, "%s must be executable", path)
}

func probeReady(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, readyEndpoint, nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 250 * time.Millisecond}).Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func findAgentgatewayChild(parentPID int, agwBinary string) (int, error) {
	want, err := filepath.Abs(agwBinary)
	if err != nil {
		return 0, fmt.Errorf("abspath: %w", err)
	}
	procEntries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("read /proc: %w", err)
	}
	for _, entry := range procEntries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		ppid, err := readProcPPID(pid)
		if err != nil || ppid != parentPID {
			continue
		}
		exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err != nil {
			continue
		}
		if exe == want || filepath.Base(exe) == filepath.Base(want) {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("no child of pid %d matched %s", parentPID, want)
}

func readProcPPID(pid int) (int, error) {
	statusBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(statusBytes), "\n") {
		if !strings.HasPrefix(line, "PPid:") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return 0, fmt.Errorf("malformed PPid line: %q", line)
		}
		return strconv.Atoi(parts[1])
	}
	return 0, fmt.Errorf("no PPid line for pid %d", pid)
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

func drainAndLog(t *testing.T, r io.Reader, tag string) {
	t.Helper()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		t.Logf("[%s] %s", tag, scanner.Text())
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	if err == nil {
		return false
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	*target = exitErr
	return true
}
