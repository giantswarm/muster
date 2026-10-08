package oauth

import (
	"bytes"
	"errors"
	"os"
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
	var fn = OpenBrowser
	if fn == nil { //nolint:staticcheck
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

// recordLaunches replaces the launcher with one that records every command
// and fails the ones named in fail, restoring the original at test end.
func recordLaunches(t *testing.T, fail ...string) *[][]string {
	t.Helper()
	var launched [][]string
	original := browserLauncher
	browserLauncher = func(cmd *exec.Cmd) error {
		launched = append(launched, cmd.Args)
		for _, f := range fail {
			if cmd.Args[0] == f {
				return exec.ErrNotFound
			}
		}
		return nil
	}
	t.Cleanup(func() { browserLauncher = original })
	return &launched
}

func TestOpenBrowser_NoBrowserLaunchesNothing(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(NoBrowserEnvVar, v)
			t.Setenv(BrowserEnvVar, "mybrowser")
			launched := recordLaunches(t)

			err := OpenBrowser("https://example.com/auth")
			if !errors.Is(err, ErrBrowserDisabled) {
				t.Fatalf("expected ErrBrowserDisabled, got %v", err)
			}
			if len(*launched) != 0 {
				t.Fatalf("expected no launch, got %v", *launched)
			}
		})
	}
}

func TestBrowserDisabled_FalseValues(t *testing.T) {
	for _, v := range []string{"", "0", "false", "no", "off"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(NoBrowserEnvVar, v)
			if BrowserDisabled() {
				t.Errorf("%s=%q should not disable the browser", NoBrowserEnvVar, v)
			}
		})
	}
}

func TestOpenOrPrint_NoBrowserPrintsURL(t *testing.T) {
	t.Setenv(NoBrowserEnvVar, "1")
	launched := recordLaunches(t)
	var out bytes.Buffer

	if OpenOrPrint(&out, "https://example.com/auth?state=x") {
		t.Fatal("expected OpenOrPrint to report no launch")
	}
	if !strings.Contains(out.String(), "https://example.com/auth?state=x") {
		t.Errorf("expected the URL on the writer, got %q", out.String())
	}
	if len(*launched) != 0 {
		t.Fatalf("expected no launch, got %v", *launched)
	}
}

func TestOpenOrPrint_LaunchPrintsNothing(t *testing.T) {
	t.Setenv(NoBrowserEnvVar, "")
	t.Setenv(BrowserEnvVar, "mybrowser")
	recordLaunches(t)
	var out bytes.Buffer

	if !OpenOrPrint(&out, "https://example.com/auth") {
		t.Fatal("expected OpenOrPrint to report a launch")
	}
	if out.Len() != 0 {
		t.Errorf("expected nothing printed, got %q", out.String())
	}
}

func TestOpenBrowser_BrowserEnvWins(t *testing.T) {
	const u = "https://example.com/auth"
	sep := string(os.PathListSeparator)
	tests := []struct {
		name    string
		browser string
		fail    []string
		want    []string
	}{
		{"appends the URL", "mybrowser --new-window", nil, []string{"mybrowser", "--new-window", u}},
		{"substitutes %s", "mybrowser --url=%s --x", nil, []string{"mybrowser", "--url=" + u, "--x"}},
		{"next entry when the first fails", "missing" + sep + "mybrowser", []string{"missing"}, []string{"mybrowser", u}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(NoBrowserEnvVar, "")
			t.Setenv(BrowserEnvVar, tc.browser)
			launched := recordLaunches(t, tc.fail...)

			if err := OpenBrowser(u); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			last := (*launched)[len(*launched)-1]
			if strings.Join(last, " ") != strings.Join(tc.want, " ") {
				t.Errorf("launched %v, want %v", last, tc.want)
			}
		})
	}
}

func TestOpenBrowser_DefaultWithoutBrowserEnv(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("unsupported platform")
	}
	t.Setenv(NoBrowserEnvVar, "")
	t.Setenv(BrowserEnvVar, "")
	launched := recordLaunches(t)

	if err := OpenBrowser("https://example.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"linux": "xdg-open", "darwin": "open", "windows": "cmd"}[runtime.GOOS]
	if len(*launched) != 1 || (*launched)[0][0] != want {
		t.Errorf("launched %v, want one %s", *launched, want)
	}
}

func TestOpenBrowser_FallsBackWhenBrowserEnvFails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("checks the linux default")
	}
	t.Setenv(NoBrowserEnvVar, "")
	t.Setenv(BrowserEnvVar, "missing")
	launched := recordLaunches(t, "missing")

	if err := OpenBrowser("https://example.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := (*launched)[len(*launched)-1][0]; got != "xdg-open" {
		t.Errorf("expected xdg-open after $BROWSER failed, got %s", got)
	}
}
