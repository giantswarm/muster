//go:build windows

package testing

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Windows API constants
const (
	PROCESS_TERMINATE = 0x0001
	PROCESS_QUERY_INFORMATION = 0x0400
)

// Windows API functions
var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess = kernel32.NewProc("OpenProcess")
	procTerminateProcess = kernel32.NewProc("TerminateProcess")
	procCloseHandle = kernel32.NewProc("CloseHandle")
)

// configureProcAttr configures the process attributes for Windows
func configureProcAttr(cmd *exec.Cmd) {
	// On Windows, we can't create process groups the same way as Unix
	// We'll use the default behavior and handle process termination differently
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// Windows-specific process creation flags could go here if needed
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// killProcessGroup attempts to terminate a process on Windows
func (m *musterInstanceManager) killProcessGroup(pid int, sig syscall.Signal) error {
	// On Windows, we don't have process groups like Unix
	// We'll just terminate the individual process
	if m.debug {
		m.logger.Debug("🪟 Windows: Terminating process PID %d\n", pid)
	}

	// First try Go's standard process.Kill()
	// This is the most compatible approach for Windows
	handle, _, err := procOpenProcess.Call(
		uintptr(PROCESS_TERMINATE|PROCESS_QUERY_INFORMATION),
		uintptr(0), // bInheritHandle = FALSE
		uintptr(pid),
	)
	
	if handle == 0 {
		return fmt.Errorf("failed to open process %d: %v", pid, err)
	}
	defer procCloseHandle.Call(handle)

	success, _, err := procTerminateProcess.Call(handle, uintptr(1))
	if success == 0 {
		return fmt.Errorf("failed to terminate process %d: %v", pid, err)
	}

	if m.debug {
		m.logger.Debug("✅ Windows: Successfully terminated process PID %d\n", pid)
	}

	return nil
} 