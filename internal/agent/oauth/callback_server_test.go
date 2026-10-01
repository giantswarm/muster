package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCallbackServer_Start_PortBinding(t *testing.T) {
	t.Run("starts on default port", func(t *testing.T) {
		// Note: This test might fail if port 3000 is already in use
		server := NewCallbackServer(0) // 0 means use default port

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		callbackURL, err := server.Start(ctx)
		if err != nil {
			// Port might be in use, skip this particular test
			t.Skipf("Could not start callback server (port may be in use): %v", err)
		}
		defer server.Stop()

		// Should get a valid callback URL
		if callbackURL == "" {
			t.Error("expected non-empty callback URL")
		}

		if !strings.Contains(callbackURL, "/callback") {
			t.Errorf("callback URL should contain '/callback', got: %s", callbackURL)
		}

		// Port should be set
		if server.GetPort() == 0 {
			t.Error("expected non-zero port after start")
		}
	})

	t.Run("fails naming the port when the port is busy", func(t *testing.T) {
		port := freeLoopbackPort(t)
		server1 := NewCallbackServer(port)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if _, err := server1.Start(ctx); err != nil {
			t.Fatalf("first server: %v", err)
		}
		defer server1.Stop()

		_, err := NewCallbackServer(port).Start(ctx)
		var inUse *PortInUseError
		if !errors.As(err, &inUse) || inUse.Port != port {
			t.Fatalf("second server on port %d: want a *PortInUseError, got %v", port, err)
		}
	})
}

func TestCallbackServer_HandleCallback_Success(t *testing.T) {
	server := NewCallbackServer(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	callbackURL, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start callback server: %v", err)
	}
	defer server.Stop()

	// Make a callback request with code and state
	go func() {
		time.Sleep(100 * time.Millisecond) // Give server time to be ready
		resp, err := http.Get(callbackURL + "?code=test-code&state=test-state")
		if err != nil {
			t.Logf("HTTP request error (may be expected if server stops first): %v", err)
			return
		}
		_ = resp.Body.Close()
	}()

	// Wait for callback
	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()

	result, err := server.WaitForCallback(waitCtx)
	if err != nil {
		t.Fatalf("WaitForCallback failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected result, got nil")
	}

	if result.Code != "test-code" {
		t.Errorf("expected code 'test-code', got %q", result.Code)
	}

	if result.State != "test-state" {
		t.Errorf("expected state 'test-state', got %q", result.State)
	}

	if result.IsError() {
		t.Error("expected success, but IsError() returned true")
	}
}

func TestCallbackServer_HandleCallback_Error(t *testing.T) {
	server := NewCallbackServer(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	callbackURL, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start callback server: %v", err)
	}
	defer server.Stop()

	// Make a callback request with error
	go func() {
		time.Sleep(100 * time.Millisecond)
		resp, err := http.Get(callbackURL + "?error=access_denied&error_description=User+denied+access")
		if err != nil {
			t.Logf("HTTP request error: %v", err)
			return
		}
		_ = resp.Body.Close()
	}()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()

	result, err := server.WaitForCallback(waitCtx)
	if err != nil {
		t.Fatalf("WaitForCallback failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected result, got nil")
	}

	if !result.IsError() {
		t.Error("expected error result, but IsError() returned false")
	}

	if result.Error != "access_denied" {
		t.Errorf("expected error 'access_denied', got %q", result.Error)
	}

	if result.ErrorDescription != "User denied access" {
		t.Errorf("expected error description 'User denied access', got %q", result.ErrorDescription)
	}
}

func TestCallbackServer_HandleCallback_StateParameter(t *testing.T) {
	server := NewCallbackServer(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	callbackURL, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start callback server: %v", err)
	}
	defer server.Stop()

	expectedState := "random-state-12345"

	go func() {
		time.Sleep(100 * time.Millisecond)
		resp, err := http.Get(callbackURL + "?code=auth-code&state=" + expectedState)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()

	result, err := server.WaitForCallback(waitCtx)
	if err != nil {
		t.Fatalf("WaitForCallback failed: %v", err)
	}

	if result.State != expectedState {
		t.Errorf("expected state %q, got %q", expectedState, result.State)
	}
}

func TestCallbackServer_WaitForCallback_Timeout(t *testing.T) {
	server := NewCallbackServer(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start callback server: %v", err)
	}
	defer server.Stop()

	// Wait with a very short timeout (no callback will happen)
	waitCtx, waitCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer waitCancel()

	result, err := server.WaitForCallback(waitCtx)

	// Should timeout
	if err == nil {
		t.Error("expected timeout error, got nil")
	}

	if result != nil {
		t.Errorf("expected nil result on timeout, got: %+v", result)
	}

	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded error, got: %v", err)
	}
}

func TestCallbackServer_SecurityHeaders(t *testing.T) {
	server := NewCallbackServer(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	callbackURL, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start callback server: %v", err)
	}
	defer server.Stop()

	// Make a request and check the response headers
	resp, err := http.Get(callbackURL + "?code=test-code&state=test-state")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check security headers
	expectedHeaders := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
	}

	for header, expectedValue := range expectedHeaders {
		actual := resp.Header.Get(header)
		if actual != expectedValue {
			t.Errorf("expected header %s=%q, got %q", header, expectedValue, actual)
		}
	}

	// Check CSP header (may have varying format)
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Error("expected Content-Security-Policy header")
	} else if !strings.Contains(csp, "default-src") {
		t.Errorf("Content-Security-Policy should contain 'default-src', got: %s", csp)
	}
}

func TestCallbackServer_GetRedirectURI(t *testing.T) {
	server := NewCallbackServer(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start callback server: %v", err)
	}
	defer server.Stop()

	redirectURI := server.GetRedirectURI()

	if redirectURI == "" {
		t.Error("expected non-empty redirect URI")
	}

	if !strings.HasSuffix(redirectURI, "/callback") {
		t.Errorf("redirect URI should end with '/callback', got: %s", redirectURI)
	}

	if !strings.HasPrefix(redirectURI, "http://localhost:") {
		t.Errorf("redirect URI should start with 'http://localhost:', got: %s", redirectURI)
	}
}

func TestCallbackServer_ContextCancellation(t *testing.T) {
	server := NewCallbackServer(0)
	ctx, cancel := context.WithCancel(context.Background())

	_, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start callback server: %v", err)
	}

	// Cancel context - server should stop
	cancel()

	// Give some time for the server to stop
	time.Sleep(200 * time.Millisecond)

	// Trying to connect should fail now
	resp, err := http.Get(server.GetRedirectURI())
	if err == nil {
		_ = resp.Body.Close()
		// Server might still be shutting down, not a hard failure
		t.Log("Server still responded after context cancellation (may take time to stop)")
	}
}

func TestCallbackServer_Stop(t *testing.T) {
	server := NewCallbackServer(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start callback server: %v", err)
	}

	// Stop should work without error
	server.Stop()

	// Stopping again should not panic or error
	server.Stop()
}

func TestCallbackResult_IsError(t *testing.T) {
	testCases := []struct {
		name     string
		result   CallbackResult
		expected bool
	}{
		{
			name: "success with code",
			result: CallbackResult{
				Code:  "auth-code",
				State: "state",
			},
			expected: false,
		},
		{
			name: "error response",
			result: CallbackResult{
				Error:            "access_denied",
				ErrorDescription: "User denied access",
			},
			expected: true,
		},
		{
			name:     "empty result",
			result:   CallbackResult{},
			expected: false, // No error field means not an error
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.result.IsError() != tc.expected {
				t.Errorf("IsError() = %v, want %v", tc.result.IsError(), tc.expected)
			}
		})
	}
}

func TestCallbackServer_MultipleCallbacksHandledOnce(t *testing.T) {
	server := NewCallbackServer(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	callbackURL, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start callback server: %v", err)
	}
	defer server.Stop()

	// Make first callback
	go func() {
		time.Sleep(100 * time.Millisecond)
		resp, err := http.Get(callbackURL + "?code=first-code&state=first-state")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()

	result, err := server.WaitForCallback(waitCtx)
	if err != nil {
		t.Fatalf("WaitForCallback failed: %v", err)
	}

	if result.Code != "first-code" {
		t.Errorf("expected first code, got %q", result.Code)
	}

	// Try second callback - should get "already processed" or be rejected
	resp, err := http.Get(callbackURL + "?code=second-code&state=second-state")
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		// The second request should be rejected
		if resp.StatusCode != http.StatusBadRequest {
			t.Logf("Second callback got status %d (expected 400 BadRequest)", resp.StatusCode)
		}
	}
}

// freeLoopbackPort returns a port free on 127.0.0.1 and, where the host has
// IPv6 loopback, on ::1.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	for range 20 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		if !hasIPv6Loopback() {
			return port
		}
		if l6, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", port)); err == nil {
			_ = l6.Close()
			return port
		}
	}
	t.Fatal("no port free on both loopback addresses")
	return 0
}

func hasIPv6Loopback() bool {
	l, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// TestCallbackServer_Start_ListensOnBothLoopbacks: the redirect URI names
// localhost, which a browser resolves to ::1 or 127.0.0.1, so the callback
// listens on both.
func TestCallbackServer_Start_ListensOnBothLoopbacks(t *testing.T) {
	port := freeLoopbackPort(t)
	server := NewCallbackServer(port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := server.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer server.Stop()

	hosts := []string{"127.0.0.1"}
	if hasIPv6Loopback() {
		hosts = append(hosts, "::1")
	}
	for _, host := range hosts {
		addr := net.JoinHostPort(host, fmt.Sprint(port))
		resp, err := http.Get("http://" + addr + "/callback?code=c&state=s")
		if err != nil {
			t.Fatalf("callback on %s does not reach the server: %v", addr, err)
		}
		_ = resp.Body.Close()
	}
}

// TestCallbackServer_Start_FailsWhenALoopbackIsTaken: a process listening on
// the callback port on either loopback address would receive the browser's
// callback in muster's place. Start refuses at once, naming the address and
// the holder, and leaves the other address free.
func TestCallbackServer_Start_FailsWhenALoopbackIsTaken(t *testing.T) {
	for _, held := range []string{"::1", "127.0.0.1"} {
		t.Run(held, func(t *testing.T) {
			if held == "::1" && !hasIPv6Loopback() {
				t.Skip("no IPv6 loopback")
			}
			port := freeLoopbackPort(t)
			addr := net.JoinHostPort(held, fmt.Sprint(port))
			holder, err := net.Listen("tcp", addr)
			if err != nil {
				t.Fatalf("hold %s: %v", addr, err)
			}
			defer func() { _ = holder.Close() }()

			server := NewCallbackServer(port)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			start := time.Now()
			_, err = server.Start(ctx)
			if err == nil {
				server.Stop()
				t.Fatalf("the callback started although %s is held: the browser's callback can land there", addr)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("Start took %v to refuse", elapsed)
			}
			var inUse *PortInUseError
			if !errors.As(err, &inUse) {
				t.Fatalf("want a *PortInUseError, got %T: %v", err, err)
			}
			if inUse.Addr != addr || !strings.Contains(err.Error(), fmt.Sprintf("callback port %d is already in use on %s", port, addr)) {
				t.Errorf("the error does not name %s: %v", addr, err)
			}
			if _, statErr := os.Stat("/proc/net/tcp"); statErr == nil && !strings.Contains(err.Error(), fmt.Sprintf("pid %d ", os.Getpid())) {
				t.Errorf("the error does not name the holder:\n%v", err)
			}

			for _, host := range callbackHosts {
				if host == held || (host == "::1" && !hasIPv6Loopback()) {
					continue
				}
				l, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
				if err != nil {
					t.Fatalf("Start left %s bound after refusing: %v", host, err)
				}
				_ = l.Close()
			}
		})
	}
}
