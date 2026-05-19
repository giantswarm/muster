//go:build !windows

package subprocess

import (
	"os/exec"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSignalProcessGroup_RejectsNonPositivePgid(t *testing.T) {
	for _, pgid := range []int{0, -1} {
		err := signalProcessGroup(pgid, syscall.SIGTERM)
		require.Error(t, err, "pgid=%d", pgid)
	}
}

// TestSignalProcessGroup_ESRCHIsSuccess pins the contract that signalling
// a group that no longer exists is a no-op rather than an error: the
// terminal state we care about (group gone) is already achieved.
func TestSignalProcessGroup_ESRCHIsSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group signalling differs on windows")
	}
	// Spawn /bin/true in its own process group, wait for exit, then
	// signal — the group has been reaped so kill returns ESRCH.
	cmd := exec.Command("/bin/true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	require.NoError(t, cmd.Wait())

	require.NoError(t, signalProcessGroup(pid, syscall.SIGTERM),
		"signalling a reaped process group must return nil (ESRCH)")
}
