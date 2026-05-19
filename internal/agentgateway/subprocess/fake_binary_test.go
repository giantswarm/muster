//go:build !windows

package subprocess

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// The test binary doubles as a fake "agentgateway": when invoked with
// SUBPROCESS_FAKE_MODE set, TestMain dispatches into runFakeBinary
// instead of running the test suite. Production code never touches
// these envs.
const (
	envFakeMode         = "SUBPROCESS_FAKE_MODE"
	envFakeReadyFile    = "SUBPROCESS_FAKE_READY_FILE"
	envFakeStartFile    = "SUBPROCESS_FAKE_START_FILE"
	envFakeSignalLog    = "SUBPROCESS_FAKE_SIGNAL_LOG"
	envFakeReadyWait    = "SUBPROCESS_FAKE_READY_WAIT_MS"
	envFakeRunFor       = "SUBPROCESS_FAKE_RUN_FOR_MS"
	envFakeExitCode     = "SUBPROCESS_FAKE_EXIT_CODE"
	envFakeStdout       = "SUBPROCESS_FAKE_STDOUT"
	envFakeStderr       = "SUBPROCESS_FAKE_STDERR"
	envFakeChildPIDFile = "SUBPROCESS_FAKE_CHILD_PID_FILE"
)

const (
	fakeModeNormal = "normal"
	fakeModeCrash  = "crash"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(envFakeMode); mode != "" {
		runFakeBinary(mode)
		return
	}
	goleak.VerifyTestMain(m,
		// errgroup-style goroutines from httptest.Server inside ready.go
		// tests can linger briefly after parent return; filter by symbol.
		goleak.IgnoreTopFunction("net/http.(*Server).Shutdown"),
	)
}

// runFakeBinary mimics a long-running daemon. Behavior is selected by
// a comma-separated mode string plus env-var knobs.
//
// Modes:
//   - "normal"      — touch ready file, wait for SIGTERM/SIGINT, exit 0.
//   - "crash"       — touch ready file (if env set), then exit with
//     SUBPROCESS_FAKE_EXIT_CODE (default 1) after
//     SUBPROCESS_FAKE_RUN_FOR_MS.
//   - "no_ready"    — never touch ready file; exit after RUN_FOR_MS.
//   - "ignore_term" — install no-op handler for SIGTERM; exit only on
//     SIGKILL.
//   - "record_signals" — append every received signal to SIGNAL_LOG file.
//   - "spawn_child" — fork /bin/sleep 300, write its pid to
//     SUBPROCESS_FAKE_CHILD_PID_FILE, then behave like "normal".
func runFakeBinary(mode string) {
	flags := map[string]bool{}
	for m := range strings.SplitSeq(mode, ",") {
		flags[strings.TrimSpace(m)] = true
	}

	if startFile := os.Getenv(envFakeStartFile); startFile != "" {
		appendLine(startFile, strconv.FormatInt(time.Now().UnixNano(), 10))
	}

	if s := os.Getenv(envFakeStdout); s != "" {
		_, _ = fmt.Fprintln(os.Stdout, s)
	}
	if s := os.Getenv(envFakeStderr); s != "" {
		_, _ = fmt.Fprintln(os.Stderr, s)
	}

	signals := make(chan os.Signal, 8)
	notified := []os.Signal{syscall.SIGHUP, syscall.SIGINT}
	if !flags["ignore_term"] {
		notified = append(notified, syscall.SIGTERM)
	}
	signal.Notify(signals, notified...)
	defer signal.Stop(signals)

	if flags["ignore_term"] {
		// Explicitly mask SIGTERM so default handler doesn't terminate
		// us; we exit only on SIGKILL (which can't be caught).
		signal.Ignore(syscall.SIGTERM)
	}

	if flags["spawn_child"] {
		pidFile := os.Getenv(envFakeChildPIDFile)
		if pidFile == "" {
			fmt.Fprintln(os.Stderr, "fake: spawn_child requires SUBPROCESS_FAKE_CHILD_PID_FILE")
			os.Exit(2)
		}
		child, err := spawnLongSleeper()
		if err != nil {
			fmt.Fprintf(os.Stderr, "fake: spawn child: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(child)), 0o600); err != nil { //nolint:gosec // test fake; path from test-controlled env
			fmt.Fprintf(os.Stderr, "fake: write child pid: %v\n", err)
		}
	}

	readyWait := durationFromEnv(envFakeReadyWait)
	if readyWait > 0 {
		select {
		case sig := <-signals:
			handleSignal(sig, flags)
			os.Exit(0)
		case <-time.After(readyWait):
		}
	}

	if readyFile := os.Getenv(envFakeReadyFile); readyFile != "" && !flags["no_ready"] {
		if err := os.WriteFile(readyFile, []byte("ready"), 0o600); err != nil { //nolint:gosec // test fake; path from test-controlled env
			fmt.Fprintf(os.Stderr, "fake: write ready file: %v\n", err)
		}
	}

	runFor := durationFromEnv(envFakeRunFor)
	exitCode, _ := strconv.Atoi(os.Getenv(envFakeExitCode))

	switch {
	case flags["crash"]:
		// Run for the configured duration (or 0) then exit non-zero.
		if runFor > 0 {
			select {
			case sig := <-signals:
				handleSignal(sig, flags)
				os.Exit(0)
			case <-time.After(runFor):
			}
		}
		if exitCode == 0 {
			exitCode = 1
		}
		os.Exit(exitCode)
	default:
		// Block on signals; exit cleanly on SIGTERM/SIGINT.
		for sig := range signals {
			handleSignal(sig, flags)
			if sig == syscall.SIGTERM || sig == syscall.SIGINT {
				os.Exit(exitCode)
			}
		}
	}
}

func handleSignal(sig os.Signal, flags map[string]bool) {
	if flags["record_signals"] {
		if path := os.Getenv(envFakeSignalLog); path != "" {
			appendLine(path, sig.String())
		}
	}
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // test fake; path from test-controlled env
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake: open %s: %v\n", path, err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		fmt.Fprintf(os.Stderr, "fake: write %s: %v\n", path, err)
	}
}

// spawnLongSleeper forks /bin/sleep 300 inheriting the fake's process
// group so the manager's group-kill reaches it. Returns the child pid.
func spawnLongSleeper() (int, error) {
	cmd := exec.Command("/bin/sleep", "300")
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
}

func durationFromEnv(key string) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Millisecond
}
