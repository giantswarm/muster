//go:build windows

package subprocess

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// configureProcAttr is a no-op on Windows. The Manager kills the
// process directly rather than signalling a process group.
func configureProcAttr(_ *exec.Cmd) {}

// signalProcessGroup forwards sig to pid via os.Process.Signal. Windows
// only supports os.Kill cleanly; other signals are best-effort.
func signalProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if sig == syscall.SIGKILL {
		return p.Kill()
	}
	return p.Signal(os.Interrupt)
}
