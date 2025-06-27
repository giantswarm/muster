//go:build !windows

package testing

import (
	"fmt"
	"os/exec"
	"syscall"
)

// configureProcAttr configures the process attributes for creating a new process group
func configureProcAttr(cmd *exec.Cmd) {
	// Configure the process to run in its own process group
	// This allows us to kill the entire process group (parent + children) later
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // Create new process group with this process as leader
	}
}

// killProcessGroup sends a signal to an entire process group to terminate parent and all children
func (m *musterInstanceManager) killProcessGroup(pid int, sig syscall.Signal) error {
	// Kill the process group (negative PID kills the entire process group)
	if err := syscall.Kill(-pid, sig); err != nil {
		// If process group kill fails, try to kill the individual process
		if err2 := syscall.Kill(pid, sig); err2 != nil {
			return fmt.Errorf("failed to kill process group -%d: %v, also failed to kill process %d: %v", pid, err, pid, err2)
		}
		if m.debug {
			m.logger.Debug("⚠️  Process group kill failed, but individual process kill succeeded for PID %d\n", pid)
		}
	}
	return nil
}
