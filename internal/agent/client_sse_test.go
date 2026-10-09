package agent

import (
	"context"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/ssetest"
)

// TestClientCallReturnsAfterAHeldStream proves a muster agent tool call
// returns at once when its answer's stream ends inside the transport's
// post-close drain, the frame that holds an unwrapped call for the idle
// connection's 90s.
func TestClientCallReturnsAfterAHeldStream(t *testing.T) {
	url := ssetest.NewHoldingServer(t)
	c := NewClient(url, NewLogger(false, false, false), TransportStreamableHTTP)
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := c.callToolDirect(ctx, "held", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the call failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the call is held after its answer's stream ended")
	}
}
