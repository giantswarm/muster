//go:build !windows

package listenfds

import (
	"net"
	"os"
	"syscall"
)

const supported = true

// fileListener turns the inherited descriptor fd into a listener. The
// listener holds its own duplicate; fd itself is closed.
func fileListener(fd int, name string) (net.Listener, error) {
	syscall.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), name)
	defer func() { _ = f.Close() }()
	return net.FileListener(f)
}
