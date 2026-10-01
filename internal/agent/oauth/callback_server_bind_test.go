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
