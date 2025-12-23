package oauth

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser opens the specified URL in the default web browser.
// It supports Linux, macOS, and Windows.
// Returns an error if the browser could not be opened.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	// Start the command but don't wait for it to complete
	// The browser will open in the background
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}

	return nil
}
