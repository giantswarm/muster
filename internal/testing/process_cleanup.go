package testing

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// CleanupStaleMusterTestProcesses terminates the muster serve instances an
// earlier `muster test` run left behind. A run that ends normally destroys its
// instances itself; one that crashed, was SIGKILLed, or lost its terminal
// leaves them running with their ports bound, and the next run would collide
// with them.
//
// An instance is stale when the harness that started it is gone: every
// instance is a direct child of its `muster test` process, so an instance
// whose parent is not a muster test harness (it was re-parented to init or a
// subreaper) has no run left to belong to. Instances whose harness is alive
// are left alone, so several `muster test` runs -- other terminals, another
// developer session on the same machine, CI jobs sharing a pid namespace --
// coexist without terminating each other's instances mid-scenario.
//
// Best-effort: failures are logged in debug mode rather than returned, since a
// failed sweep must not block test execution.
func CleanupStaleMusterTestProcesses(logger TestLogger, debug bool) {
	cleanupStaleMusterTestProcesses(logger, debug, systemProcessTable())
}

// processInfo is one row of the process table: enough to tell a test instance
// from any other process and to find the harness that owns it.
type processInfo struct {
	PID  int
	PPID int
	// Args is the command line with argv[0] first. It comes from `ps -o args`,
	// so an argument containing whitespace arrives split; the paths the sweep
	// inspects are created by os.MkdirTemp and contain none.
	Args []string
}

// processTable is the sweep's view of the operating system: a listing of every
// visible process and a way to ask one to terminate. Tests substitute both so
// the sweep runs against a fabricated table without touching real processes.
type processTable struct {
	list      func() ([]processInfo, error)
	terminate func(pid int) error
}

// systemProcessTable is the real process table: `ps` for the listing, SIGTERM
// for termination.
func systemProcessTable() processTable {
	return processTable{list: listProcesses, terminate: terminateProcess}
}

// cleanupStaleMusterTestProcesses is CleanupStaleMusterTestProcesses over an
// explicit process table.
func cleanupStaleMusterTestProcesses(logger TestLogger, debug bool, table processTable) {
	procs, err := table.list()
	if err != nil {
		if debug {
			logger.Debug("Could not check for stale processes: %v\n", err)
		}
		return
	}

	stale := staleTestInstances(procs, os.Getpid())
	if len(stale) == 0 {
		if debug {
			logger.Debug("No stale muster test processes found\n")
		}
		return
	}

	killedCount := 0
	for _, p := range stale {
		if err := table.terminate(p.PID); err != nil {
			// The process may have exited between listing and signalling.
			if debug {
				logger.Debug("Could not send SIGTERM to PID %d: %v\n", p.PID, err)
			}
			continue
		}
		killedCount++
		if debug {
			configPath, _ := flagValue(p.Args, "--config-path")
			logger.Debug("Killed stale muster test process PID %d (its muster test harness is gone, parent is now %d, config %s)\n", p.PID, p.PPID, configPath)
		}
	}

	if killedCount > 0 {
		fmt.Printf("Cleaned up %d stale muster test process(es)\n", killedCount)
	}
}

// staleTestInstances returns the test instances in procs whose harness is gone.
//
// A test instance is a `muster serve` whose --config-path lies under a
// muster-test-<digits> directory, the layout NewMusterInstanceManagerWithConfig
// creates with os.MkdirTemp. Its harness is its parent process, the `muster
// test` that spawned it. While that parent is alive the instance belongs to a
// run in progress -- this one or a concurrent one -- and is kept. An instance
// whose parent is missing from the table or is not a muster test harness was
// re-parented after its harness died, and is stale. selfPID is never returned.
func staleTestInstances(procs []processInfo, selfPID int) []processInfo {
	byPID := make(map[int]processInfo, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}

	var stale []processInfo
	for _, p := range procs {
		if p.PID == selfPID || !isTestInstance(p) {
			continue
		}
		if parent, ok := byPID[p.PPID]; ok && isTestHarness(parent) {
			continue
		}
		stale = append(stale, p)
	}
	return stale
}

// isTestInstance reports whether p is a muster serve started by the test
// harness: a muster binary running the serve subcommand with a --config-path
// under a muster-test-<digits> directory.
func isTestInstance(p processInfo) bool {
	if !isMusterBinary(p.Args) || subcommand(p.Args) != "serve" {
		return false
	}
	configPath, ok := flagValue(p.Args, "--config-path")
	return ok && underTestTempDir(configPath)
}

// isTestHarness reports whether p is a `muster test` process, the parent every
// live test instance has.
func isTestHarness(p processInfo) bool {
	return isMusterBinary(p.Args) && subcommand(p.Args) == "test"
}

// isMusterBinary reports whether argv[0] names a muster binary, wherever it is
// installed and however it was built (`muster`, `./muster`, a `go run` temp
// binary, `muster.exe`).
func isMusterBinary(args []string) bool {
	return len(args) > 0 && strings.Contains(filepath.Base(args[0]), "muster")
}

// subcommand returns the first argument after argv[0] that is not a flag: the
// cobra subcommand.
func subcommand(args []string) string {
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// flagValue returns the value of the named flag in args, given either as
// `--flag value` or `--flag=value`.
func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// testTempDirPrefix is the os.MkdirTemp pattern the instance manager uses,
// without the random part. MkdirTemp fills the `*` with decimal digits.
const testTempDirPrefix = "muster-test-"

// underTestTempDir reports whether path has a `muster-test-<digits>` component,
// i.e. lies under a temp directory the instance manager created. Matching the
// component rather than a fixed /tmp prefix follows TMPDIR, and requiring
// digits keeps a user's own `muster-test-config` directory out of the sweep.
func underTestTempDir(path string) bool {
	for _, component := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		suffix, ok := strings.CutPrefix(component, testTempDirPrefix)
		if !ok || suffix == "" {
			continue
		}
		if _, err := strconv.ParseUint(suffix, 10, 64); err == nil {
			return true
		}
	}
	return false
}

// listProcesses lists every process visible in this pid namespace via
// `ps -eo pid=,ppid=,args=`, which procps and the BSD ps of macOS both accept;
// with every header empty neither prints a header line.
func listProcesses() ([]processInfo, error) {
	out, err := exec.Command("ps", "-eo", "pid=,ppid=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return parseProcessTable(bytes.NewReader(out))
}

// parseProcessTable parses `ps -o pid,ppid,args` output: one process per line,
// pid and ppid first, the command line as the rest. Lines that do not start
// with two integers (a header, a truncated row) are skipped, and procps 3.3's
// "{comm}" marker in front of a command line is dropped.
func parseProcessTable(r io.Reader) ([]processInfo, error) {
	var procs []processInfo
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		args := fields[2:]
		// procps 3.3 (Ubuntu 22.04, CircleCI's cimg images) prefixes the command
		// line with "{comm}" when the executable's name differs from argv[0];
		// procps 4 and BSD ps do not. The brace word is not part of argv.
		if len(args) > 1 && strings.HasPrefix(args[0], "{") && strings.HasSuffix(args[0], "}") {
			args = args[1:]
		}
		procs = append(procs, processInfo{PID: pid, PPID: ppid, Args: args})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading process table: %w", err)
	}
	return procs, nil
}

// terminateProcess asks pid to shut down gracefully with SIGTERM. muster serve
// stops its services, and with them any stdio MCP servers it started, on the
// way out.
func terminateProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(syscall.SIGTERM)
}
