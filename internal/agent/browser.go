package agent

import (
	"muster/internal/agent/oauth"
)

// OpenBrowserForAuth opens the specified URL in the default web browser.
// This is a convenience wrapper around the oauth package's OpenBrowser function,
// used by CLI commands that need to trigger OAuth authentication flows.
//
// Args:
//   - url: The URL to open in the browser
//
// Returns:
//   - error: nil if successful, or an error if the browser couldn't be opened
func OpenBrowserForAuth(url string) error {
	return oauth.OpenBrowser(url)
}
