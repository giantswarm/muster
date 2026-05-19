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

// signalProcessGroup delivers sig to the process group identified by
// pgid. ESRCH (group already reaped) is treated as success; any other
// errno is wrapped and returned.
func signalProcessGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 0 {
		return fmt.Errorf("invalid pgid %d", pgid)
	}
	err := syscall.Kill(-pgid, sig)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return fmt.Errorf("kill -%d (%s): %w", pgid, sig, err)
}
