package oauth

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// NoBrowserEnvVar disables every browser launch when set to a true value
// (1, true, yes, ...): OpenBrowser starts no process and returns
// ErrBrowserDisabled, and callers print the URL instead.
const NoBrowserEnvVar = "MUSTER_NO_BROWSER"

// BrowserEnvVar names the browser command tried before the platform default,
// following the $BROWSER convention: a list of commands separated by the OS
// path list separator, where %s stands for the URL (appended when absent).
const BrowserEnvVar = "BROWSER"

// ErrBrowserDisabled is returned by OpenBrowser when NoBrowserEnvVar is set.
var ErrBrowserDisabled = errors.New("browser launch disabled by " + NoBrowserEnvVar)

// browserLauncher is the function used to launch the browser command.
// It can be replaced in tests to prevent actual browser opening.
var browserLauncher = func(cmd *exec.Cmd) error {
	return cmd.Start()
}

// BrowserDisabled reports whether NoBrowserEnvVar disables the browser launch.
// Any value other than empty or a false one (0, false, no, off) disables it.
func BrowserDisabled() bool {
	v := strings.TrimSpace(os.Getenv(NoBrowserEnvVar))
	if v == "" {
		return false
	}
	switch strings.ToLower(v) {
	case "no", "off":
		return false
	}
	disabled, err := strconv.ParseBool(v)
	return err != nil || disabled
}

// OpenBrowser opens the specified URL in the web browser: the commands in
// $BROWSER first, then the platform default (xdg-open, open, start).
//
// Security: Only HTTP and HTTPS URLs are allowed to prevent command injection
// attacks through malicious URL schemes.
//
// Returns an error if:
//   - The URL is invalid or empty
//   - The URL scheme is not http or https
//   - The browser launch is disabled (ErrBrowserDisabled)
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

	if BrowserDisabled() {
		return ErrBrowserDisabled
	}

	for _, cmd := range browserEnvCommands(urlStr) {
		if err := browserLauncher(cmd); err == nil {
			return nil
		}
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", urlStr) //nolint:gosec
	case "darwin":
		cmd = exec.Command("open", urlStr) //nolint:gosec
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", urlStr) //nolint:gosec
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	// Start the command but don't wait for it to complete
	// The browser will open in the background
	if err := browserLauncher(cmd); err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}

	return nil
}

// browserEnvCommands builds one command per entry of $BROWSER, the URL
// replacing %s or appended as the last argument.
func browserEnvCommands(urlStr string) []*exec.Cmd {
	var cmds []*exec.Cmd
	for _, entry := range strings.Split(os.Getenv(BrowserEnvVar), string(os.PathListSeparator)) {
		fields := strings.Fields(entry)
		if len(fields) == 0 {
			continue
		}
		substituted := false
		for i, f := range fields {
			if strings.Contains(f, "%s") {
				fields[i] = strings.ReplaceAll(f, "%s", urlStr)
				substituted = true
			}
		}
		if !substituted {
			fields = append(fields, urlStr)
		}
		cmds = append(cmds, exec.Command(fields[0], fields[1:]...)) //nolint:gosec
	}
	return cmds
}

// OpenOrPrint opens authURL in the browser. When the launch is disabled or
// fails, it writes the URL to w, so the person opens it elsewhere while the
// caller keeps waiting for the callback. It reports whether a browser was
// launched.
func OpenOrPrint(w io.Writer, authURL string) bool {
	err := OpenBrowser(authURL)
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrBrowserDisabled):
		_, _ = fmt.Fprintf(w, "Open this URL in your browser to sign in:\n  %s\n\n", authURL)
	default:
		_, _ = fmt.Fprintf(w, "Could not open the browser (%v).\nOpen this URL in your browser to sign in:\n  %s\n\n", err, authURL)
	}
	return false
}
