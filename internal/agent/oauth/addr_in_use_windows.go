package oauth

import (
	"errors"
	"syscall"
)

// wsaeaddrinuse is Winsock's WSAEADDRINUSE; syscall.EADDRINUSE on Windows is
// an invented value no bind returns.
const wsaeaddrinuse = syscall.Errno(10048)

// isAddrInUse reports whether a bind failed because another socket holds the
// address.
func isAddrInUse(err error) bool {
	return errors.Is(err, wsaeaddrinuse) || errors.Is(err, syscall.EADDRINUSE)
}
