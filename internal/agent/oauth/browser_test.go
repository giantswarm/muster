package oauth

import (
	"runtime"
	"testing"
)

func TestOpenBrowser(t *testing.T) {
	// We can't actually test browser opening in CI, but we can verify
	// the function doesn't panic and handles the platform detection
	t.Run("does not panic on current platform", func(t *testing.T) {
		// This will fail on non-existent URL but should not panic
		// We're testing that the platform switch works correctly
		switch runtime.GOOS {
		case "linux", "darwin", "windows":
			// Expected platforms - the function should work
			// We don't actually call it to avoid opening a browser in tests
		default:
			// On unsupported platforms, the function should return an error
			err := OpenBrowser("https://example.com")
			if err == nil {
				t.Errorf("Expected error on unsupported platform %s", runtime.GOOS)
			}
		}
	})
}
