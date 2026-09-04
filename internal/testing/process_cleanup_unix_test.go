//go:build !windows

package testing

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// helperRoleEnv selects what this test binary does when re-executed by
// TestCleanupStaleMusterTestProcesses_SparesLiveHarness instead of running
// the tests: stand in for a `muster test` harness or for the `muster serve`
// instance it started. The re-exec gives the stand-ins the exact command
// lines the sweep inspects, so the test proves the real listing (`ps`),
// parsing and SIGTERM path on real processes -- but only on the two it
// started, never on anything else running on the machine.
const (
	helperRoleEnv       = "MUSTER_TEST_PROCESS_CLEANUP_HELPER"
	helperConfigPathEnv = "MUSTER_TEST_PROCESS_CLEANUP_CONFIG_PATH"
	helperRoleHarness   = "harness"
	helperRoleInstance  = "instance"
)

// TestMain diverts the helper roles before any test runs; the ordinary
// invocation runs the package's tests unchanged.
func TestMain(m *testing.M) {
	switch os.Getenv(helperRoleEnv) {
	case helperRoleHarness:
		runHelperHarness()
	case helperRoleInstance:
		runHelperInstance()
	default:
		os.Exit(m.Run())
	}
}

// runHelperHarness plays a live `muster test`: it starts the instance stand-in
// exactly as the harness would (a direct child running `muster serve
// --config-path <tmp>/muster-test-<digits>/...`) and then waits to be killed.
// It never tears its child down, so SIGKILLing it leaves the instance orphaned,
// as a crashed harness does. The instance announces itself; the harness stays
// quiet.
func runHelperHarness() {
	// os.Executable, not os.Args[0]: the harness runs as argv[0] "muster", and
	// exec.Command("muster") would resolve to a real muster binary in PATH.
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper harness: locate test binary: %v\n", err)
		os.Exit(1)
	}
	child := exec.Command(self) //nolint:gosec // re-exec of this test binary
	child.Args = []string{"muster", "serve", "--config-path", os.Getenv(helperConfigPathEnv), "--debug"}
	child.Env = append(os.Environ(), helperRoleEnv+"="+helperRoleInstance)
	// The instance inherits the test's pipes: stdin so it lives until the test
	// closes the write end (a nil Stdin would be /dev/null, an immediate EOF),
	// stdout so the test can read its "terminated" line after this harness died.
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "helper harness: start instance: %v\n", err)
		os.Exit(1)
	}
	blockUntilTerminated(notifyTerm())
	os.Exit(0)
}

// runHelperInstance plays a `muster serve` test instance. It announces
// "instance <pid> ready" on stdout only after its SIGTERM handler is in place
// -- a SIGTERM that lands before signal.Notify kills a Go process silently,
// and on a slow CI container the test would otherwise reach phase 2 before
// the runtime got that far -- then reports "terminated" once SIGTERM arrives
// and exits 0, the way muster's graceful shutdown does.
func runHelperInstance() {
	sigs := notifyTerm()
	fmt.Printf("instance %d ready\n", os.Getpid())
	blockUntilTerminated(sigs)
	fmt.Printf("instance %d terminated\n", os.Getpid())
	os.Exit(0)
}

func notifyTerm() <-chan os.Signal {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	return sigs
}

// blockUntilTerminated returns on SIGTERM, or when stdin reaches EOF -- the
// test holds the write end of stdin, so a helper outliving the test still exits.
func blockUntilTerminated(sigs <-chan os.Signal) {
	stdinClosed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(stdinClosed)
	}()
	select {
	case <-sigs:
	case <-stdinClosed:
	}
}

// TestCleanupStaleMusterTestProcesses_SparesLiveHarness proves on real
// processes what a concurrent `muster test` needs: while a harness is alive,
// the sweep leaves its instance alone; once the harness is gone (SIGKILLed,
// as by a crash), the same sweep terminates the instance it left behind.
//
// The sweep runs against the real `ps` listing, narrowed to the two processes
// this test started, and the real SIGTERM sender.
func TestCleanupStaleMusterTestProcesses_SparesLiveHarness(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("ps not available")
	}

	// A config path shaped like the instance manager's, under this test's own
	// temp directory rather than the shared one.
	configPath := filepath.Join(t.TempDir(), "muster-test-4242", "test-cleanup-spares-live-harness-1", "muster")

	self, err := os.Executable()
	require.NoError(t, err)
	harness := exec.Command(self) //nolint:gosec // re-exec of this test binary
	harness.Args = []string{"muster", "test", "--scenario", "cleanup-spares-live-harness"}
	harness.Env = append(os.Environ(), helperRoleEnv+"="+helperRoleHarness, helperConfigPathEnv+"="+configPath)
	harness.Stderr = os.Stderr
	// Plain pipes rather than StdinPipe/StdoutPipe: Wait closes those once the
	// harness exits, but the instance inherits both ends and the test keeps
	// reading its stdout and holding its stdin open after the harness is gone.
	stdinR, stdinW, err := os.Pipe()
	require.NoError(t, err)
	stdoutR, stdoutW, err := os.Pipe()
	require.NoError(t, err)
	harness.Stdin = stdinR
	harness.Stdout = stdoutW
	require.NoError(t, harness.Start())
	_ = stdinR.Close()
	_ = stdoutW.Close()
	output := bufio.NewReader(stdoutR)

	// The instance announces itself once its SIGTERM handler is installed; by
	// then it has long been exec'd, so its command line is in the process table.
	line, err := output.ReadString('\n')
	require.NoError(t, err)
	instancePID, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "instance "), " ready\n")))
	require.NoError(t, err, "instance announced %q", line)
	t.Cleanup(func() {
		_ = syscall.Kill(instancePID, syscall.SIGKILL)
		_ = harness.Process.Kill()
		_ = harness.Wait()
		_ = stdinW.Close()
		_ = stdoutR.Close()
	})

	// The real table, narrowed to the harness and the instance.
	terminated := []int{}
	table := processTable{
		list: func() ([]processInfo, error) {
			procs, err := listProcesses()
			if err != nil {
				return nil, err
			}
			var ours []processInfo
			for _, p := range procs {
				if p.PID == harness.Process.Pid || p.PID == instancePID {
					ours = append(ours, p)
				}
			}
			return ours, nil
		},
		terminate: func(pid int) error {
			terminated = append(terminated, pid)
			return terminateProcess(pid)
		},
	}
	logger := &mockTestLogger{debugEnabled: true}

	// Phase 1: the harness is alive -> its instance is a live run's and survives.
	procs, err := table.list()
	require.NoError(t, err)
	require.Len(t, procs, 2, "ps lists the harness and its instance: %+v", procs)
	require.True(t, isTestHarness(byPID(procs, harness.Process.Pid)), "the stand-in harness reads as muster test: %+v", procs)
	require.True(t, isTestInstance(byPID(procs, instancePID)), "the stand-in instance reads as a test instance: %+v", procs)
	require.Equal(t, harness.Process.Pid, byPID(procs, instancePID).PPID)

	cleanupStaleMusterTestProcesses(logger, true, table)

	require.Empty(t, terminated, "an instance of a live harness is not signalled")
	require.NoError(t, syscall.Kill(instancePID, 0), "the instance is still running")
	require.Equal(t, "No stale muster test processes found\n", logger.debugBuf.String())

	// Phase 2: the harness dies without cleaning up -> the instance is orphaned
	// (re-parented by the kernel before the harness's exit is reported) and the
	// same sweep terminates it.
	require.NoError(t, harness.Process.Kill())
	_ = harness.Wait()
	procs, err = table.list()
	require.NoError(t, err)
	require.Len(t, procs, 1, "only the instance is left: %+v", procs)
	require.NotEqual(t, harness.Process.Pid, procs[0].PPID, "the instance was re-parented")

	logger.debugBuf.Reset()
	cleanupStaleMusterTestProcesses(logger, true, table)

	require.Equal(t, []int{instancePID}, terminated)
	require.Contains(t, logger.debugBuf.String(), fmt.Sprintf("Killed stale muster test process PID %d", instancePID))
	// The instance's stdout is the harness's pipe, and the harness is gone, so
	// this read completes only when the instance acts on the SIGTERM.
	line, err = output.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("instance %d terminated\n", instancePID), line)
}

func byPID(procs []processInfo, pid int) processInfo {
	for _, p := range procs {
		if p.PID == pid {
			return p
		}
	}
	return processInfo{}
}
