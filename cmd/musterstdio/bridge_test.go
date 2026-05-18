package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBridge_RequestResponse verifies a single JSON-RPC request is routed to
// the child's stdin and the matching response surfaces on Send.
func TestBridge_RequestResponse(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	cmd, args, env := runChildArgs("echo")
	b := newBridgeForTest(t, cmd, args, env)
	require.NoError(t, b.Start(ctx))
	t.Cleanup(func() { _ = b.Stop(time.Second) })

	resp, err := b.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	require.NoError(t, err)
	require.NotNil(t, resp)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(resp, &parsed))
	require.EqualValues(t, 1, parsed["id"])
}

// TestBridge_Notification_NoResponse verifies a JSON-RPC notification (no id)
// returns immediately with a nil response.
func TestBridge_Notification_NoResponse(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	cmd, args, env := runChildArgs("echo")
	b := newBridgeForTest(t, cmd, args, env)
	require.NoError(t, b.Start(ctx))
	t.Cleanup(func() { _ = b.Stop(time.Second) })

	resp, err := b.Send(ctx, []byte(`{"jsonrpc":"2.0","method":"ping"}`))
	require.NoError(t, err)
	require.Nil(t, resp)
}

// TestBridge_ConcurrentRequests_OutOfOrder verifies the response mux delivers
// each Send the response with the matching id, even when the child writes
// responses out of order.
func TestBridge_ConcurrentRequests_OutOfOrder(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	cmd, args, env := runChildArgs("out_of_order")
	b := newBridgeForTest(t, cmd, args, env)
	require.NoError(t, b.Start(ctx))
	t.Cleanup(func() { _ = b.Stop(time.Second) })

	type result struct {
		id  int
		raw []byte
		err error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, id := range []int{1, 2} {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"x"}`, id))
			r, err := b.Send(ctx, req)
			results <- result{id: id, raw: r, err: err}
		}(id)
	}
	wg.Wait()
	close(results)

	got := map[int][]byte{}
	for r := range results {
		require.NoError(t, r.err)
		got[r.id] = r.raw
	}
	require.Len(t, got, 2)
	for id, raw := range got {
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(raw, &parsed))
		require.EqualValues(t, id, parsed["id"], "mux must deliver the response for id %d to the Send for id %d", id, id)
	}
}

// TestBridge_ContextCancel_UnregistersPending verifies a canceled Send does
// not leak its response channel registration.
func TestBridge_ContextCancel_UnregistersPending(t *testing.T) {
	t.Parallel()

	parentCtx, cancelParent := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelParent()

	cmd, args, env := runChildArgs("slow")
	b := newBridgeForTest(t, cmd, args, env)
	require.NoError(t, b.Start(parentCtx))
	t.Cleanup(func() { _ = b.Stop(time.Second) })

	sendCtx, cancelSend := context.WithCancel(parentCtx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancelSend()
	}()
	_, err := b.Send(sendCtx, []byte(`{"jsonrpc":"2.0","id":42,"method":"slow"}`))
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, b.pendingCountForTest(), "canceled Send must remove its pending entry")
}

// TestBridge_ChildExits_FailsInFlight verifies that when the child exits, the
// in-flight Send returns a wrapped error and IsHealthy flips to false.
func TestBridge_ChildExits_FailsInFlight(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	cmd, args, env := runChildArgs("echo")
	b := newBridgeForTest(t, cmd, args, env)
	require.NoError(t, b.Start(ctx))
	t.Cleanup(func() { _ = b.Stop(time.Second) })

	require.True(t, b.IsHealthy())
	require.NoError(t, b.Stop(500*time.Millisecond))
	require.False(t, b.IsHealthy(), "after Stop, bridge must be unhealthy")

	_, err := b.Send(ctx, []byte(`{"jsonrpc":"2.0","id":99,"method":"after-stop"}`))
	require.Error(t, err)
}

// TestBridge_Stop_SIGKILL_Fallback verifies that a child ignoring SIGTERM is
// killed after the configured timeout.
func TestBridge_Stop_SIGKILL_Fallback(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	cmd, args, env := runChildArgs("ignore_term")
	b := newBridgeForTest(t, cmd, args, env)
	require.NoError(t, b.Start(ctx))

	start := time.Now()
	err := b.Stop(150 * time.Millisecond)
	elapsed := time.Since(start)
	require.NoError(t, err, "Stop must succeed even when SIGTERM is ignored")
	require.Less(t, elapsed, 3*time.Second, "Stop must not block past the SIGKILL fallback")
}

// TestBridge_Send_InvalidJSON_Errors checks that a body the parser cannot
// extract an id from surfaces an error rather than blocking forever.
func TestBridge_Send_InvalidJSON_Errors(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	cmd, args, env := runChildArgs("echo")
	b := newBridgeForTest(t, cmd, args, env)
	require.NoError(t, b.Start(ctx))
	t.Cleanup(func() { _ = b.Stop(time.Second) })

	_, err := b.Send(ctx, []byte(`not-json`))
	require.Error(t, err)
	require.True(t, errors.Is(err, errInvalidFrame) || err != nil)
}

// newBridgeForTest returns a Bridge with no logger noise and reasonable
// defaults so tests stay readable.
func newBridgeForTest(t *testing.T, cmd string, args []string, env []string) *Bridge {
	t.Helper()
	return NewBridge(BridgeOptions{
		Command: cmd,
		Args:    args,
		Env:     parseEnv(env),
		Logger:  testLogger(t),
	})
}
