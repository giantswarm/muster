package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// scenarios_test.go captures end-to-end behaviour in BDD form. Each subtest
// runs the shim Bridge + Server against the in-process fake stdio child and
// asserts a discrete user-facing behaviour. Wider system-level scenarios
// (CRD-driven, exercising the shim through MCPServer) live under
// internal/testing/scenarios once the reconciler wires this binary in
// (PR 6 of the translator series).

// TestScenario_StdioChild_ExposesMCPOverStreamableHTTP documents the core
// behaviour: given an stdio MCP child, the shim translates HTTP requests into
// stdio frames and routes responses back.
func TestScenario_StdioChild_ExposesMCPOverStreamableHTTP(t *testing.T) {
	t.Parallel()

	// Given an stdio MCP child reachable through the shim
	srv, _ := scenarioBootstrap(t, "echo")

	// When a JSON-RPC request is POSTed to /mcp
	body := []byte(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"x"}}`)
	resp, raw := httpPostJSON(t, t.Context(), "http://"+srv.Addr().String()+"/mcp", body)

	// Then the matching response surfaces with the request id preserved
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(raw, &parsed))
	require.EqualValues(t, 42, parsed["id"])
	require.Contains(t, parsed, "result")
}

// TestScenario_Notification_NoResponseBody documents that JSON-RPC
// notifications (no id) do not yield a response body and the shim signals 202.
func TestScenario_Notification_NoResponseBody(t *testing.T) {
	t.Parallel()

	// Given a shim wrapping a stdio MCP child
	srv, _ := scenarioBootstrap(t, "echo")

	// When a notification frame is POSTed
	body := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp, raw := httpPostJSON(t, t.Context(), "http://"+srv.Addr().String()+"/mcp", body)

	// Then the shim accepts it with 202 and an empty body
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.Empty(t, raw)
}

// TestScenario_HealthEndpoint_TracksChild documents the /healthz contract.
func TestScenario_HealthEndpoint_TracksChild(t *testing.T) {
	t.Parallel()

	// Given a running shim wrapping a healthy stdio MCP child
	srv, bridge := scenarioBootstrap(t, "echo")

	// When /healthz is queried
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+srv.Addr().String()+"/healthz", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	// Then the shim reports OK
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// And when the child has been stopped
	require.NoError(t, bridge.Stop(time.Second))

	// Then /healthz reports unavailable
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// TestScenario_ConcurrentRequests_AreMultiplexed documents the multiplexing
// guarantee: each Send waits for the response carrying its own id, even when
// the child writes responses out of order.
func TestScenario_ConcurrentRequests_AreMultiplexed(t *testing.T) {
	t.Parallel()

	// Given a child that returns responses in reverse arrival order
	srv, _ := scenarioBootstrap(t, "out_of_order")

	type result struct {
		id   int
		body []byte
	}
	results := make(chan result, 2)
	for _, id := range []int{11, 22} {
		go func(id int) {
			body := []byte(`{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"x"}`)
			resp, raw := httpPostJSON(t, t.Context(), "http://"+srv.Addr().String()+"/mcp", body)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			results <- result{id: id, body: raw}
		}(id)
	}

	// When both responses arrive
	got := map[int][]byte{}
	for range 2 {
		r := <-results
		got[r.id] = r.body
	}

	// Then each request body carries its own id, never the other request's
	require.Len(t, got, 2)
	for id, raw := range got {
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(raw, &parsed))
		require.EqualValues(t, id, parsed["id"])
	}
}

// scenarioBootstrap stands up a Bridge + Server against the fake child for a
// scenario test.
func scenarioBootstrap(t *testing.T, mode string) (*Server, *Bridge) {
	t.Helper()
	cmd, args, env := runChildArgs(mode)
	bridge := NewBridge(BridgeOptions{
		Command: cmd,
		Args:    args,
		Env:     parseEnv(env),
		Logger:  testLogger(t),
	})
	require.NoError(t, bridge.Start(t.Context()))
	t.Cleanup(func() { _ = bridge.Stop(time.Second) })

	srv, err := NewServer(Config{
		Bridge:     bridge,
		ListenAddr: loopbackEphemeral,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return srv, bridge
}
