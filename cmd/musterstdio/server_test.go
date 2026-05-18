package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeBridge is a hand-rolled stand-in for *Bridge that lets server tests
// inject arbitrary responses and health states without spawning a process.
type fakeBridge struct {
	mu        sync.Mutex
	healthy   bool
	send      func(ctx context.Context, frame []byte) ([]byte, error)
	stopCalls int
}

func (f *fakeBridge) Send(ctx context.Context, frame []byte) ([]byte, error) {
	f.mu.Lock()
	send := f.send
	f.mu.Unlock()
	if send == nil {
		return nil, nil
	}
	return send(ctx, frame)
}

func (f *fakeBridge) IsHealthy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.healthy
}

func (f *fakeBridge) Stop(timeout time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	return nil
}

// TestServer_PostMCP_Request_ReturnsBody verifies the happy path: POST /mcp
// returns whatever the bridge produced as the response body.
func TestServer_PostMCP_Request_ReturnsBody(t *testing.T) {
	t.Parallel()

	br := &fakeBridge{healthy: true, send: func(ctx context.Context, frame []byte) ([]byte, error) {
		return []byte(`{"jsonrpc":"2.0","id":1,"result":{"echo":true}}`), nil
	}}

	srv := serverForTest(t, br)
	resp, body := httpPostJSON(t, t.Context(), srv.URL+"/mcp", []byte(`{"jsonrpc":"2.0","id":1,"method":"x"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `{"jsonrpc":"2.0","id":1,"result":{"echo":true}}`, string(body))
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

// TestServer_PostMCP_Notification_Returns202 verifies that a JSON-RPC
// notification (Send returns nil body) translates to a 202 Accepted.
func TestServer_PostMCP_Notification_Returns202(t *testing.T) {
	t.Parallel()

	br := &fakeBridge{healthy: true, send: func(ctx context.Context, frame []byte) ([]byte, error) {
		return nil, nil
	}}
	srv := serverForTest(t, br)
	resp, body := httpPostJSON(t, t.Context(), srv.URL+"/mcp", []byte(`{"jsonrpc":"2.0","method":"ping"}`))
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.Empty(t, body)
}

// TestServer_PostMCP_BridgeError_Returns502 verifies bridge errors surface as
// 502 with a JSON-RPC-style error body.
func TestServer_PostMCP_BridgeError_Returns502(t *testing.T) {
	t.Parallel()

	br := &fakeBridge{healthy: true, send: func(ctx context.Context, frame []byte) ([]byte, error) {
		return nil, errors.New("boom: child returned nonsense")
	}}
	srv := serverForTest(t, br)
	resp, body := httpPostJSON(t, t.Context(), srv.URL+"/mcp", []byte(`{"jsonrpc":"2.0","id":1,"method":"x"}`))
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	require.NotEmpty(t, body)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Contains(t, parsed, "error")
}

// TestServer_PostMCP_Unhealthy_Returns503 verifies that requests are refused
// when the bridge is not healthy.
func TestServer_PostMCP_Unhealthy_Returns503(t *testing.T) {
	t.Parallel()

	br := &fakeBridge{healthy: false}
	srv := serverForTest(t, br)
	resp, _ := httpPostJSON(t, t.Context(), srv.URL+"/mcp", []byte(`{"jsonrpc":"2.0","id":1,"method":"x"}`))
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// TestServer_PostMCP_EmptyBody_Returns400 covers the empty-body branch.
func TestServer_PostMCP_EmptyBody_Returns400(t *testing.T) {
	t.Parallel()

	br := &fakeBridge{healthy: true}
	srv := serverForTest(t, br)
	resp, _ := httpPostJSON(t, t.Context(), srv.URL+"/mcp", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestServer_PostMCP_InvalidJSON_Returns400 covers the JSON-validation branch.
func TestServer_PostMCP_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()

	br := &fakeBridge{healthy: true}
	srv := serverForTest(t, br)
	resp, _ := httpPostJSON(t, t.Context(), srv.URL+"/mcp", []byte(`not json`))
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestServer_Healthz_POST_Returns405 verifies POST on /healthz is rejected.
func TestServer_Healthz_POST_Returns405(t *testing.T) {
	t.Parallel()

	br := &fakeBridge{healthy: true}
	srv := serverForTest(t, br)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/healthz", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// TestNewServer_Validates rejects missing required Config fields.
func TestNewServer_Validates(t *testing.T) {
	t.Parallel()

	_, err := NewServer(Config{ListenAddr: loopbackEphemeral})
	require.Error(t, err)

	_, err = NewServer(Config{Bridge: &fakeBridge{}})
	require.Error(t, err)
}

// TestServer_PostMCP_NonPOST_Returns405 verifies method-not-allowed for GETs.
func TestServer_PostMCP_NonPOST_Returns405(t *testing.T) {
	t.Parallel()

	br := &fakeBridge{healthy: true}
	srv := serverForTest(t, br)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/mcp", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// TestServer_Healthz_HealthyAndUnhealthy verifies /healthz mirrors bridge state.
func TestServer_Healthz_HealthyAndUnhealthy(t *testing.T) {
	t.Parallel()

	br := &fakeBridge{healthy: true}
	srv := serverForTest(t, br)

	resp, _ := httpGet(t, t.Context(), srv.URL+"/healthz")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	br.mu.Lock()
	br.healthy = false
	br.mu.Unlock()
	resp, _ = httpGet(t, t.Context(), srv.URL+"/healthz")
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// TestServer_Shutdown_DrainsInflight verifies in-flight requests complete
// before Shutdown returns and new requests are rejected during drain.
func TestServer_Shutdown_DrainsInflight(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	br := &fakeBridge{healthy: true, send: func(ctx context.Context, frame []byte) ([]byte, error) {
		<-release
		return []byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`), nil
	}}
	srv := serverForTest(t, br)

	type result struct {
		status int
		body   []byte
	}
	results := make(chan result, 1)
	go func() {
		resp, body := httpPostJSON(t, t.Context(), srv.URL+"/mcp", []byte(`{"jsonrpc":"2.0","id":1,"method":"x"}`))
		results <- result{status: resp.StatusCode, body: body}
	}()

	require.Eventually(t, func() bool { return srv.InFlight() == 1 }, time.Second, 5*time.Millisecond)

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownDone <- srv.Shutdown(shutdownCtx)
	}()

	close(release)
	require.NoError(t, <-shutdownDone)
	r := <-results
	require.Equal(t, http.StatusOK, r.status)
	require.JSONEq(t, `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`, string(r.body))
}

// TestServer_SeparateHealthPort_Listens verifies that when a separate health
// port is configured, /healthz is served on it and the main listener does not
// expose it.
func TestServer_SeparateHealthPort_Listens(t *testing.T) {
	t.Parallel()

	br := &fakeBridge{healthy: true}
	srv, err := NewServer(Config{
		Bridge:     br,
		ListenAddr: loopbackEphemeral,
		HealthAddr: loopbackEphemeral,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		_ = srv.Shutdown(context.Background())
	})

	require.NotNil(t, srv.Addr())
	require.NotNil(t, srv.HealthAddr())

	resp, _ := httpGet(t, t.Context(), "http://"+srv.HealthAddr().String()+"/healthz")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = httpGet(t, t.Context(), "http://"+srv.Addr().String()+"/healthz")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// serverForTest constructs and starts a Server bound to an ephemeral port and
// returns it. The server is shut down via t.Cleanup.
type testServer struct {
	URL string
	*Server
}

func serverForTest(t *testing.T, br BridgeIface) *testServer {
	t.Helper()
	srv, err := NewServer(Config{
		Bridge:     br,
		ListenAddr: loopbackEphemeral,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start(t.Context()))
	t.Cleanup(func() {
		_ = srv.Shutdown(context.Background())
	})
	addr := srv.Addr().String()
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, time.Second, 5*time.Millisecond)
	return &testServer{URL: "http://" + addr, Server: srv}
}

func httpGet(t *testing.T, ctx context.Context, url string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, data
}
