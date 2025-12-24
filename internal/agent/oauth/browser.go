package oauth

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
)

// OpenBrowser opens the specified URL in the default web browser.
// It supports Linux, macOS, and Windows.
//
// Security: Only HTTP and HTTPS URLs are allowed to prevent command injection
// attacks through malicious URL schemes.
//
// Returns an error if:
//   - The URL is invalid or empty
//   - The URL scheme is not http or https
//   - The browser could not be opened
//   - The platform is not supported
func OpenBrowser(urlStr string) error {
	// Validate URL scheme to prevent command injection
	if urlStr == "" {
		return fmt.Errorf("URL cannot be empty")
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("invalid URL scheme %q: only http and https are allowed", parsedURL.Scheme)
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", urlStr)
	case "darwin":
		cmd = exec.Command("open", urlStr)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", urlStr)
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
