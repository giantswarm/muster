package testing

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// CleanupStaleMusterTestProcesses kills any muster processes that were left behind
// from previous test runs. It identifies test processes by looking for muster serve
// commands with --config-path pointing to /tmp/muster-test-* directories.
//
// This should be called at the start of each test suite to ensure a clean slate.
func CleanupStaleMusterTestProcesses(logger TestLogger, debug bool) error {
	// Get current process ID to avoid killing ourselves
	currentPID := os.Getpid()

	// Find all muster processes with test config paths
	cmd := exec.Command("pgrep", "-f", "muster.*serve.*--config-path.*/tmp/muster-test-")
	output, err := cmd.Output()
	if err != nil {
		// pgrep returns exit code 1 when no processes found, which is fine
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			if debug {
				logger.Debug("🧹 No stale muster test processes found\n")
			}
			return nil
		}
		// Other errors are unexpected but not fatal
		if debug {
			logger.Debug("⚠️  Could not check for stale processes: %v\n", err)
		}
		return nil
	}

	// Parse PIDs from output
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil
	}

	killedCount := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}

		// Don't kill ourselves
		if pid == currentPID {
			continue
		}

		// Attempt to kill the process
		process, err := os.FindProcess(pid)
		if err != nil {
			continue
		}

		// Send SIGTERM first for graceful shutdown
		if err := process.Signal(syscall.SIGTERM); err != nil {
			// Process might already be gone, that's fine
			if debug {
				logger.Debug("⚠️  Could not send SIGTERM to PID %d: %v\n", pid, err)
			}
			continue
		}

		killedCount++
		if debug {
			logger.Debug("🔪 Killed stale muster test process PID %d\n", pid)
		}
	}

	if killedCount > 0 {
		if debug {
			logger.Debug("🧹 Cleaned up %d stale muster test process(es)\n", killedCount)
		} else {
			fmt.Printf("🧹 Cleaned up %d stale muster test process(es)\n", killedCount)
		}
	}

	return nil
}
