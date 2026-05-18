package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestEndToEnd_PostRoundTrip wires a real Bridge to a real Server (no Run)
// against an in-process fake child, and verifies POST /mcp round-trips.
func TestEndToEnd_PostRoundTrip(t *testing.T) {
	t.Parallel()

	cmd, args, env := runChildArgs("echo")
	bridge := NewBridge(BridgeOptions{
		Command: cmd,
		Args:    args,
		Env:     parseEnv(env),
		Logger:  testLogger(t),
	})
	require.NoError(t, bridge.Start(t.Context()))
	t.Cleanup(func() { _ = bridge.Stop(time.Second) })

	srv, err := NewServer(Config{
		Bridge:     bridge,
		ListenAddr: loopbackEphemeral,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{"x":1}}`)
	resp, raw := httpPostJSON(t, t.Context(), "http://"+srv.Addr().String()+"/mcp", body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(raw, &parsed))
	require.EqualValues(t, 7, parsed["id"])
}

// TestSigterm_GracefulShutdown launches the compiled shim binary as a real
// subprocess, fires a SIGTERM, and asserts a clean exit code. The binary is
// invoked with --listen-port 0 and its bound address is recovered from
// stderr.
func TestSigterm_GracefulShutdown(t *testing.T) {
	t.Parallel()

	bin := buildShimBinary(t)
	cmd, args, env := runChildArgs("echo")
	shimArgs := []string{
		flagChildCommand, cmd,
		flagListenPort, "0",
	}
	for _, a := range args {
		shimArgs = append(shimArgs, flagChildArg, a)
	}
	for _, e := range env {
		shimArgs = append(shimArgs, flagChildEnv, e)
	}

	cmdProc := exec.Command(bin, shimArgs...)
	cmdProc.Env = append(os.Environ(), env...)
	stderrPipe, err := cmdProc.StderrPipe()
	require.NoError(t, err)
	require.NoError(t, cmdProc.Start())
	t.Cleanup(func() { _ = cmdProc.Process.Kill() })

	addr := readListenAddr(t, stderrPipe, 5*time.Second)
	waitForHealth(t, "http://"+addr+"/healthz", 3*time.Second)

	require.NoError(t, cmdProc.Process.Signal(syscall.SIGTERM))
	done := make(chan error, 1)
	go func() { done <- cmdProc.Wait() }()
	select {
	case err := <-done:
		require.NoError(t, err, "shim must exit 0 on SIGTERM")
	case <-time.After(10 * time.Second):
		t.Fatal("shim did not exit after SIGTERM")
	}
}

// TestChildCrash_NonZeroExit verifies that a child crashing at startup makes
// the shim exit non-zero with a diagnostic message.
func TestChildCrash_NonZeroExit(t *testing.T) {
	t.Parallel()

	bin := buildShimBinary(t)
	cmd, args, env := runChildArgs("crash")
	shimArgs := []string{
		flagChildCommand, cmd,
		flagListenPort, "0",
	}
	for _, a := range args {
		shimArgs = append(shimArgs, flagChildArg, a)
	}
	for _, e := range env {
		shimArgs = append(shimArgs, flagChildEnv, e)
	}

	cmdProc := exec.Command(bin, shimArgs...)
	cmdProc.Env = append(os.Environ(), env...)
	stderr := &captured{}
	cmdProc.Stderr = stderr
	require.NoError(t, cmdProc.Start())

	done := make(chan error, 1)
	go func() { done <- cmdProc.Wait() }()
	select {
	case err := <-done:
		require.Error(t, err, "shim must exit non-zero when child crashes")
		require.Contains(t, strings.ToLower(stderr.String()), "child")
	case <-time.After(10 * time.Second):
		_ = cmdProc.Process.Kill()
		t.Fatal("shim did not exit after child crash; stderr=" + stderr.String())
	}
}

// TestInvalidFlags_ReturnsUsageError verifies that missing --child-command
// produces a clear error and a non-zero exit, not a panic.
func TestInvalidFlags_ReturnsUsageError(t *testing.T) {
	t.Parallel()

	stderr := &captured{}
	err := Run(t.Context(), []string{"--listen-port", "0"}, stderr, slog.New(slog.NewTextHandler(stderr, nil)))
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()+stderr.String()), "child-command")
}

// TestParseFlags_RejectsPositionalArgs covers the "trailing args" branch.
func TestParseFlags_RejectsPositionalArgs(t *testing.T) {
	t.Parallel()

	stderr := &captured{}
	_, err := parseFlags([]string{flagChildCommand, "echo", "extra"}, stderr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected positional arguments")
}

// TestParseFlags_RejectsInvalidPort covers the port-range branch.
func TestParseFlags_RejectsInvalidPort(t *testing.T) {
	t.Parallel()

	stderr := &captured{}
	_, err := parseFlags([]string{flagListenPort, "70000"}, stderr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --listen-port")

	_, err = parseFlags([]string{"--health-port", "-1"}, stderr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --health-port")
}

// TestStderrLogger_Forwards covers the child stderr forwarder.
func TestStderrLogger_Forwards(t *testing.T) {
	t.Parallel()

	buf := &captured{}
	log := slog.New(slog.NewTextHandler(buf, nil))
	n, err := stderrLogger{logger: log}.Write([]byte("hello world"))
	require.NoError(t, err)
	require.Equal(t, len("hello world"), n)
	require.Contains(t, buf.String(), "hello world")
}

// TestRun_PrintVersion exercises the --version short-circuit.
func TestRun_PrintVersion(t *testing.T) {
	t.Parallel()

	stderr := &captured{}
	err := Run(t.Context(), []string{"--version"}, stderr, slog.New(slog.NewTextHandler(stderr, nil)))
	require.NoError(t, err)
	require.Contains(t, stderr.String(), "musterstdio")
}

// TestRun_ContextCancel_GracefulReturn launches Run in-process and asserts
// that canceling the parent context drives a clean shutdown.
func TestRun_ContextCancel_GracefulReturn(t *testing.T) {
	t.Parallel()

	stderrBuf := &captured{}
	runCtx, cancel := context.WithCancel(t.Context())

	flags := childFlagsFor(t, "echo", 0)
	runErr := make(chan error, 1)
	go func() {
		runErr <- Run(runCtx, flags, stderrBuf, slog.New(slog.NewTextHandler(stderrBuf, nil)))
	}()

	require.Eventually(t, func() bool {
		return strings.Contains(stderrBuf.String(), "listen_addr=")
	}, 5*time.Second, 10*time.Millisecond, "Run never logged readiness; stderr=%s", stderrBuf.String())

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancel; stderr=" + stderrBuf.String())
	}
}

// TestNoGoroutineLeaks runs Bridge+Server in-process, exercises POST /mcp,
// shuts down, and asserts no extra goroutines remain.
func TestNoGoroutineLeaks(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"),
		goleak.IgnoreAnyFunction("net/http.(*Server).Serve"),
		goleak.IgnoreCurrent(),
	)

	cmd, args, env := runChildArgs("echo")
	bridge := NewBridge(BridgeOptions{
		Command: cmd,
		Args:    args,
		Env:     parseEnv(env),
		Logger:  testLogger(t),
	})
	require.NoError(t, bridge.Start(t.Context()))

	srv, err := NewServer(Config{
		Bridge:     bridge,
		ListenAddr: loopbackEphemeral,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start(t.Context()))

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	resp, _ := httpPostJSON(t, t.Context(), "http://"+srv.Addr().String()+"/mcp", body)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, srv.Shutdown(context.Background()))
	require.NoError(t, bridge.Stop(2*time.Second))
}

// readListenAddr scans a stderr stream for the slog-formatted listen_addr
// attribute and returns the host:port. Used by subprocess tests to find the
// shim's ephemeral port without a fixed-port race.
func readListenAddr(t *testing.T, r io.Reader, deadline time.Duration) string {
	t.Helper()
	addrRe := regexp.MustCompile(`listen_addr=([^\s]+)`)
	found := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			t.Logf("shim stderr: %s", line)
			if m := addrRe.FindStringSubmatch(line); m != nil {
				found <- m[1]
				continue
			}
		}
	}()
	select {
	case addr := <-found:
		return addr
	case <-time.After(deadline):
		t.Fatal("shim never printed listen_addr")
		return ""
	}
}
