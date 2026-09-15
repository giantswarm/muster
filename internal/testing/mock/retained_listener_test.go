package mock

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestRetainedListenerKeepsThePortBoundWhileClosed is the property Restart
// relies on: between Close and reopen nobody else can bind the port, Accept
// reports net.ErrClosed so http.Server.Serve returns, and after reopen the
// listener hands out connections again.
func TestRetainedListenerKeepsThePortBoundWhileClosed(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	l := retainListener(raw)
	defer func() { _ = l.release() }()
	addr := l.Addr().String()

	accepted := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		accepted <- err
	}()

	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case err := <-accepted:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("pending Accept after Close: got %v, want net.ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending Accept did not return after Close")
	}
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept while closed: got %v, want net.ErrClosed", err)
	}
	if other, err := net.Listen("tcp", addr); err == nil {
		_ = other.Close()
		t.Fatal("the port was free while the listener was closed; it must stay bound")
	}

	if err := l.reopen(); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial after reopen: %v", err)
	}
	defer func() { _ = conn.Close() }()
	served, err := l.Accept()
	if err != nil {
		t.Fatalf("Accept after reopen: %v", err)
	}
	_ = served.Close()

	if err := l.release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := l.TCPListener.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after release: got %v, want net.ErrClosed", err)
	}
}

// TestOAuthServer_RestartKeepsThePort: the mock serves on the same port after
// Restart, and while it is running nobody else can bind that port -- a
// restart never releases it, so the port cannot be taken in between.
func TestOAuthServer_RestartKeepsThePort(t *testing.T) {
	ctx := context.Background()
	server := NewOAuthServer(OAuthServerConfig{})
	port, err := server.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = server.Stop(ctx) }()

	metadataURL := fmt.Sprintf("http://localhost:%d/.well-known/oauth-authorization-server", port)
	for _, phase := range []string{"before", "after"} {
		resp, err := http.Get(metadataURL) //nolint:gosec
		if err != nil {
			t.Fatalf("metadata %s the restart: %v", phase, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("metadata %s the restart: status %d", phase, resp.StatusCode)
		}
		if other, err := net.Listen("tcp", fmt.Sprintf(":%d", port)); err == nil {
			_ = other.Close()
			t.Fatalf("port %d was free %s the restart while the server owns it", port, phase)
		}
		if phase == "after" {
			break
		}
		if _, err := server.Restart(ctx); err != nil {
			t.Fatalf("restart: %v", err)
		}
		if server.Port() != port {
			t.Fatalf("restart moved the server from port %d to %d", port, server.Port())
		}
	}
}
