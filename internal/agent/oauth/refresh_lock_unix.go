//go:build !windows

package oauth

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on f, blocking until it is free.
// The kernel releases it when the file is closed or the process ends.
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) //nolint:gosec // a file descriptor fits an int
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // a file descriptor fits an int
}
