//go:build !windows

package oauth

import (
	"errors"
	"syscall"
)

// isAddrInUse reports whether a bind failed because another socket holds the
// address.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
