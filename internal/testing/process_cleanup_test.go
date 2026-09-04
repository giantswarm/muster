package testing

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// mockTestLogger is a simple mock logger for testing.
type mockTestLogger struct {
	debugBuf       bytes.Buffer
	debugEnabled   bool
	verboseEnabled bool
}

func (m *mockTestLogger) Info(format string, args ...interface{}) {
	fmt.Fprintf(&m.debugBuf, format, args...)
}

func (m *mockTestLogger) Debug(format string, args ...interface{}) {
	fmt.Fprintf(&m.debugBuf, format, args...)
}

func (m *mockTestLogger) Error(format string, args ...interface{}) {
	fmt.Fprintf(&m.debugBuf, format, args...)
}

func (m *mockTestLogger) IsDebugEnabled() bool {
	return m.debugEnabled
}

func (m *mockTestLogger) IsVerboseEnabled() bool {
	return m.verboseEnabled
}

// fakeProcessTable is a process table the sweep can run against without
// touching a real process: a fixed listing (or a listing error) and a recorder
// of every pid the sweep asks to terminate.
type fakeProcessTable struct {
	procs      []processInfo
	listErr    error
	termErr    map[int]error
	terminated []int
}

func (f *fakeProcessTable) table() processTable {
	return processTable{
		list: func() ([]processInfo, error) {
			if f.listErr != nil {
				return nil, f.listErr
			}
			return f.procs, nil
		},
		terminate: func(pid int) error {
			f.terminated = append(f.terminated, pid)
			return f.termErr[pid]
		},
	}
}

func args(cmdline string) []string { return strings.Fields(cmdline) }

// The process shapes the sweep meets: a harness, its instance, the mock MCP
// server an instance starts, muster serving a real configuration, and bystanders.
var (
	liveHarness   = processInfo{PID: 100, PPID: 50, Args: args("muster test --parallel 50 --base-port 30000")}
	liveInstance  = processInfo{PID: 101, PPID: 100, Args: args("muster serve --config-path /tmp/muster-test-2604742823/test-mcpserver-check-available-1757000000000/muster --debug")}
	liveMockMCP   = processInfo{PID: 102, PPID: 101, Args: args("muster test --mock-mcp-server --mock-config /tmp/muster-test-2604742823/test-mcpserver-check-available-1757000000000/mock-a.yaml")}
	goRunHarness  = processInfo{PID: 200, PPID: 50, Args: args("/tmp/go-build123/b001/exe/muster test --scenario oauth-login")}
	goRunInstance = processInfo{PID: 201, PPID: 200, Args: args("/home/u/go/bin/muster serve --config-path=/var/folders/x1/T/muster-test-77/test-oauth-login-1/muster --debug")}
	orphanToInit  = processInfo{PID: 300, PPID: 1, Args: args("./muster serve --config-path /tmp/muster-test-1111/test-old-1/muster --debug")}
	orphanToShell = processInfo{PID: 301, PPID: 400, Args: args("muster serve --config-path /tmp/muster-test-2222/test-old-2/muster --debug")}
	shell         = processInfo{PID: 400, PPID: 1, Args: args("/usr/bin/zsh -l")}
	parentGone    = processInfo{PID: 302, PPID: 9999, Args: args("muster serve --config-path /tmp/muster-test-3333/test-old-3/muster --debug")}
	realServe     = processInfo{PID: 500, PPID: 1, Args: args("muster serve --config-path /home/u/.config/muster --debug")}
	userDirServe  = processInfo{PID: 501, PPID: 1, Args: args("muster serve --config-path /home/u/muster-test-config/muster")}
	otherBinary   = processInfo{PID: 502, PPID: 1, Args: args("/usr/bin/sleep serve --config-path /tmp/muster-test-4444/x/muster")}
	kernelThread  = processInfo{PID: 2, PPID: 0, Args: args("[kthreadd]")}
)

func pids(procs []processInfo) []int {
	out := make([]int, 0, len(procs))
	for _, p := range procs {
		out = append(out, p.PID)
	}
	return out
}

// TestStaleTestInstances_KeepsInstancesOfLiveHarnesses is the rule the sweep
// lives by: an instance whose parent is a running `muster test` belongs to a
// run in progress -- ours or a concurrent one -- and is never touched; one
// whose harness is gone is stale whatever it was re-parented to.
func TestStaleTestInstances_KeepsInstancesOfLiveHarnesses(t *testing.T) {
	table := []processInfo{
		kernelThread, shell,
		liveHarness, liveInstance, liveMockMCP,
		goRunHarness, goRunInstance,
		orphanToInit, orphanToShell, parentGone,
		realServe, userDirServe, otherBinary,
	}
	stale := staleTestInstances(table, 1)
	require.ElementsMatch(t, []int{orphanToInit.PID, orphanToShell.PID, parentGone.PID}, pids(stale),
		"only the instances without a live muster test parent are stale")
}

// TestStaleTestInstances_NeverReturnsSelf: the sweep runs inside a muster
// process and must not be able to name itself, whatever its command line.
func TestStaleTestInstances_NeverReturnsSelf(t *testing.T) {
	stale := staleTestInstances([]processInfo{orphanToInit}, orphanToInit.PID)
	require.Empty(t, stale)
}

// TestIsTestInstance pins what counts as a harness-started instance.
func TestIsTestInstance(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    processInfo
		want bool
	}{
		{"harness instance", liveInstance, true},
		{"--config-path=value form under TMPDIR", goRunInstance, true},
		{"relative binary path", orphanToInit, true},
		{"windows path separators", processInfo{Args: args(`C:\Users\u\muster.exe serve --config-path C:\Temp\muster-test-42\test-a\muster`)}, true},
		{"harness itself", liveHarness, false},
		{"mock MCP server started by an instance", liveMockMCP, false},
		{"muster serving a real config", realServe, false},
		{"user directory that merely starts with muster-test-", userDirServe, false},
		{"muster-test- without digits", processInfo{Args: args("muster serve --config-path /tmp/muster-test-/x/muster")}, false},
		{"not a muster binary", otherBinary, false},
		{"serve without config path", processInfo{Args: args("muster serve --debug")}, false},
		{"config path as the last argument without a value", processInfo{Args: args("muster serve --config-path")}, false},
		{"bare argv", processInfo{Args: args("muster")}, false},
		{"kernel thread", kernelThread, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isTestInstance(tc.p))
		})
	}
}

// TestIsTestHarness pins what counts as a harness, the parent that protects an
// instance.
func TestIsTestHarness(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    processInfo
		want bool
	}{
		{"muster test", liveHarness, true},
		{"go run temp binary", goRunHarness, true},
		{"mcp-server mode", processInfo{Args: args("muster test --mcp-server --config-path /home/u/.config/muster")}, true},
		{"global flag before the subcommand", processInfo{Args: args("muster --debug test")}, true},
		{"instance", liveInstance, false},
		{"shell", shell, false},
		{"init", processInfo{PID: 1, Args: args("/usr/lib/systemd/systemd --system")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isTestHarness(tc.p))
		})
	}
}

// TestParseProcessTable pins the `ps -o pid,ppid,args` shape the listing
// depends on, including the header line ps prints when headers are not
// suppressed, procps 3.3's "{comm}" marker in front of a command line whose
// argv[0] is not the executable's name, and the odd rows that are not processes.
func TestParseProcessTable(t *testing.T) {
	out := "    PID    PPID COMMAND\n" +
		"      1       0 /usr/lib/systemd/systemd --switched-root --system\n" +
		"      2       0 [kthreadd]\n" +
		"\n" +
		"  12345    4321 muster serve --config-path /tmp/muster-test-99/test-a-1/muster --debug\n" +
		"   5011    4990 {testing.test} muster test --scenario cleanup-spares-live-harness\n" +
		"   5012    4990 {} --not-a-marker\n" +
		"garbage line\n" +
		"  12346 notanumber muster serve\n" +
		"  12347\n"
	procs, err := parseProcessTable(strings.NewReader(out))
	require.NoError(t, err)
	require.Equal(t, []processInfo{
		{PID: 1, PPID: 0, Args: args("/usr/lib/systemd/systemd --switched-root --system")},
		{PID: 2, PPID: 0, Args: args("[kthreadd]")},
		{PID: 12345, PPID: 4321, Args: args("muster serve --config-path /tmp/muster-test-99/test-a-1/muster --debug")},
		{PID: 5011, PPID: 4990, Args: args("muster test --scenario cleanup-spares-live-harness")},
		{PID: 5012, PPID: 4990, Args: args("--not-a-marker")},
	}, procs)
}

// TestCleanupStaleMusterTestProcesses_TerminatesOnlyOrphans runs the sweep
// against a fabricated table holding a live run and two orphans, and checks
// that exactly the orphans are signalled -- and that the sweep itself, being
// a unit test, never reaches a real process.
func TestCleanupStaleMusterTestProcesses_TerminatesOnlyOrphans(t *testing.T) {
	fake := &fakeProcessTable{
		procs:   []processInfo{shell, liveHarness, liveInstance, liveMockMCP, orphanToInit, orphanToShell, realServe},
		termErr: map[int]error{orphanToShell.PID: errors.New("no such process")},
	}
	logger := &mockTestLogger{debugEnabled: true}

	cleanupStaleMusterTestProcesses(logger, true, fake.table())

	require.ElementsMatch(t, []int{orphanToInit.PID, orphanToShell.PID}, fake.terminated)
	out := logger.debugBuf.String()
	require.Contains(t, out, fmt.Sprintf("Killed stale muster test process PID %d (its muster test harness is gone, parent is now 1, config /tmp/muster-test-1111/test-old-1/muster)", orphanToInit.PID))
	require.Contains(t, out, fmt.Sprintf("Could not send SIGTERM to PID %d: no such process", orphanToShell.PID))
	require.NotContains(t, out, fmt.Sprintf("PID %d", liveInstance.PID), "the live run's instance is not mentioned, let alone signalled")
}

// TestCleanupStaleMusterTestProcesses_NoProcesses covers the common case: a
// table with nothing to sweep logs that in debug mode and stays silent otherwise.
func TestCleanupStaleMusterTestProcesses_NoProcesses(t *testing.T) {
	for _, debug := range []bool{true, false} {
		t.Run(fmt.Sprintf("debug=%t", debug), func(t *testing.T) {
			fake := &fakeProcessTable{procs: []processInfo{shell, liveHarness, liveInstance, realServe}}
			logger := &mockTestLogger{debugEnabled: debug}

			cleanupStaleMusterTestProcesses(logger, debug, fake.table())

			require.Empty(t, fake.terminated)
			if debug {
				require.Equal(t, "No stale muster test processes found\n", logger.debugBuf.String())
			} else {
				require.Empty(t, logger.debugBuf.String())
			}
		})
	}
}

// TestCleanupStaleMusterTestProcesses_ListingFailure: when the process table
// cannot be read (no ps on this platform) the sweep reports that in debug mode
// and terminates nothing.
func TestCleanupStaleMusterTestProcesses_ListingFailure(t *testing.T) {
	fake := &fakeProcessTable{listErr: errors.New("ps: executable file not found in $PATH")}
	logger := &mockTestLogger{debugEnabled: true}

	cleanupStaleMusterTestProcesses(logger, true, fake.table())

	require.Empty(t, fake.terminated)
	require.Equal(t, "Could not check for stale processes: ps: executable file not found in $PATH\n", logger.debugBuf.String())
}

// TestCleanupStaleMusterTestProcesses_SkipsSelf: a table that lists this very
// process as a stale-looking instance must not have it signalled.
func TestCleanupStaleMusterTestProcesses_SkipsSelf(t *testing.T) {
	self := processInfo{PID: os.Getpid(), PPID: 1, Args: args("muster serve --config-path /tmp/muster-test-5/test-self-1/muster")}
	fake := &fakeProcessTable{procs: []processInfo{self}}

	cleanupStaleMusterTestProcesses(&mockTestLogger{}, false, fake.table())

	require.Empty(t, fake.terminated)
}
