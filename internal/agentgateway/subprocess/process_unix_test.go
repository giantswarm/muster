//go:build !windows

package subprocess

import (
	"os/exec"
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

func TestSignalProcessGroup_ESRCHIsSuccess(t *testing.T) {
	cmd := exec.Command("/bin/true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	require.NoError(t, cmd.Wait())

	require.NoError(t, signalProcessGroup(pid, syscall.SIGTERM),
		"signalling a reaped process group must return nil (ESRCH)")
}
