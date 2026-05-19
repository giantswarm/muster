//go:build !windows

package subprocess

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

// configureProcAttr makes the child the leader of its own process group
// so signals can be delivered to the whole group (parent + children).
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalProcessGroup delivers sig to the process group rooted at pid.
// If the group signal fails (e.g. the process already reaped) it falls
// back to signalling pid directly.
func signalProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err2 := syscall.Kill(pid, sig); err2 != nil {
			if errors.Is(err2, syscall.ESRCH) {
				return nil
			}
			return fmt.Errorf("kill -%d (%s): %w; kill %d: %v", pid, sig, err, pid, err2)
		}
	}
	return nil
}
