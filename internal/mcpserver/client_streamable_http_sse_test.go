package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/ssetransport/ssetest"
)

// TestStreamableHTTPClientCallReturnsAfterAHeldStream proves a backend tool
// call returns at once when its answer's stream ends inside the transport's
// post-close drain, the frame that holds an unwrapped call for the idle
// connection's 90s.
func TestStreamableHTTPClientCallReturnsAfterAHeldStream(t *testing.T) {
	url := ssetest.NewHoldingServer(t)
	c := NewStreamableHTTPClientWithHeaders(url, nil)
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	assertCallAnswers(t, func() error {
		_, err := c.CallTool(ctx, "held", nil)
		return err
	})
}

// TestDynamicAuthClientCallReturnsAfterAHeldStream is the same proof for the
// client of a backend whose bearer is minted per request.
func TestDynamicAuthClientCallReturnsAfterAHeldStream(t *testing.T) {
	url := ssetest.NewHoldingServer(t)
	c := NewDynamicAuthClient(url, nil, "openid", "client-id", "")
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	assertCallAnswers(t, func() error {
		_, err := c.CallTool(ctx, "held", nil)
		return err
	})
}

// assertCallAnswers fails unless call returns without error well before the
// idle-connection timeout.
func assertCallAnswers(t *testing.T, call func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the call failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the call is held after its answer's stream ended")
	}
}
