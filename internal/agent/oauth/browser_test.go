package oauth

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// mockBrowserLauncher replaces the real browser launcher for testing.
// It prevents actual browser opening and records what command would be executed.
func mockBrowserLauncher(cmd *exec.Cmd) error {
	// Do nothing - don't actually open a browser
	return nil
}

func TestOpenBrowser_SupportedPlatforms(t *testing.T) {
	// Replace the browser launcher with a mock to prevent actual browser opening
	originalLauncher := browserLauncher
	browserLauncher = mockBrowserLauncher
	defer func() { browserLauncher = originalLauncher }()

	// Verify that the function recognizes supported platforms
	supportedPlatforms := []string{"linux", "darwin", "windows"}

	currentOS := runtime.GOOS
	isSupported := false
	for _, p := range supportedPlatforms {
		if currentOS == p {
			isSupported = true
			break
		}
	}

	if !isSupported {
		// On unsupported platforms, the function should return an error
		err := OpenBrowser("https://example.com")
		if err == nil {
			t.Errorf("Expected error on unsupported platform %s", currentOS)
		}
		if !strings.Contains(err.Error(), "unsupported platform") {
			t.Errorf("Expected 'unsupported platform' in error, got: %s", err.Error())
		}
	} else {
		// On supported platforms, verify the function works with the mock
		err := OpenBrowser("https://example.com")
		if err != nil {
			t.Errorf("Expected no error on supported platform %s, got: %s", currentOS, err.Error())
		}
	}
}

func TestOpenBrowser_FunctionSignature(t *testing.T) {
	// Ensure the function exists with the correct signature
	// This is a compile-time check that the function is properly exported
	var fn func(string) error = OpenBrowser
	if fn == nil {
		t.Error("OpenBrowser function should not be nil")
	}
}

func TestOpenBrowser_EmptyURL(t *testing.T) {
	// Empty URL should be rejected with a clear error
	err := OpenBrowser("")
	if err == nil {
		t.Error("Expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "cannot be empty") {
		t.Errorf("Expected 'cannot be empty' in error, got: %s", err.Error())
	}
}

func TestOpenBrowser_InvalidURLScheme(t *testing.T) {
	// Test that non-http/https schemes are rejected for security
	invalidSchemes := []struct {
		name string
		url  string
	}{
		{"file scheme", "file:///etc/passwd"},
		{"javascript scheme", "javascript:alert(1)"},
		{"data scheme", "data:text/html,<script>alert(1)</script>"},
		{"ftp scheme", "ftp://example.com/file"},
		{"no scheme", "example.com"},
		{"custom scheme", "myapp://callback"},
	}

	for _, tc := range invalidSchemes {
		t.Run(tc.name, func(t *testing.T) {
			err := OpenBrowser(tc.url)
			if err == nil {
				t.Errorf("Expected error for URL with %s: %s", tc.name, tc.url)
			}
			if !strings.Contains(err.Error(), "invalid URL scheme") && !strings.Contains(err.Error(), "invalid URL") {
				t.Errorf("Expected 'invalid URL scheme' or 'invalid URL' in error, got: %s", err.Error())
			}
		})
	}
}

func TestOpenBrowser_ValidURLSchemes(t *testing.T) {
	// Replace the browser launcher with a mock to prevent actual browser opening
	originalLauncher := browserLauncher
	browserLauncher = mockBrowserLauncher
	defer func() { browserLauncher = originalLauncher }()

	// Test that http and https schemes are accepted
	validURLs := []string{
		"https://example.com",
		"https://example.com/path?query=value",
		"http://localhost:8080",
		"https://auth.example.com/oauth/authorize?client_id=123",
	}

	for _, url := range validURLs {
		t.Run(url, func(t *testing.T) {
			err := OpenBrowser(url)
			// On unsupported platforms, we'll get an "unsupported platform" error
			// On supported platforms with the mock, we should get no error
			if err != nil && strings.Contains(err.Error(), "invalid URL scheme") {
				t.Errorf("Valid URL %s should not be rejected for invalid scheme: %s", url, err.Error())
			}
		})
	}
}

func TestOpenBrowser_MalformedURL(t *testing.T) {
	// Test that malformed URLs are rejected
	malformedURLs := []string{
		"://missing-scheme",
		"https://[invalid-ipv6",
	}

	for _, url := range malformedURLs {
		t.Run(url, func(t *testing.T) {
			err := OpenBrowser(url)
			if err == nil {
				t.Errorf("Expected error for malformed URL: %s", url)
			}
		})
	}
}

func TestOpenBrowser_LauncherError(t *testing.T) {
	// Replace the browser launcher with one that returns an error
	originalLauncher := browserLauncher
	browserLauncher = func(cmd *exec.Cmd) error {
		return exec.ErrNotFound
	}
	defer func() { browserLauncher = originalLauncher }()

	err := OpenBrowser("https://example.com")
	if err == nil {
		t.Error("Expected error when browser launcher fails")
	}
	if !strings.Contains(err.Error(), "failed to open browser") {
		t.Errorf("Expected 'failed to open browser' in error, got: %s", err.Error())
	}
}
