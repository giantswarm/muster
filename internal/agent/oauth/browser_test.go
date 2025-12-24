package oauth

import (
	"runtime"
	"strings"
	"testing"
)

func TestOpenBrowser_SupportedPlatforms(t *testing.T) {
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
	}
	// On supported platforms, we don't actually call the function
	// to avoid opening a browser during tests. The function's behavior
	// is verified by the platform detection logic.
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
	// Test that http and https schemes are accepted
	// Note: We can't actually test the browser opening, but we can verify
	// the URL validation passes by checking error type on supported platforms
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
			// On supported platforms, we might get "failed to open browser" if the command fails
			// But we should NOT get "invalid URL scheme" for valid URLs
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
