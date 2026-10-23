//go:build windows

package oauth

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on f, blocking until it is free. Windows
// releases it when the handle is closed or the process ends.
func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &windows.Overlapped{})
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
