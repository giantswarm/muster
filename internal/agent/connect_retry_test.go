package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/server"
)

// gatewayUpstreamFailure is agentgateway's answer to a request whose dial of
// the aggregator's pod ran into its connect timeout: the pod it dialled was
// replaced.
const gatewayUpstreamFailure = "mcp: failed to send message: http upstream error: http request failed: upstream call failed: Connect: deadline has elapsed"

// noConnectRetryDelays makes the retries of a test immediate.
func noConnectRetryDelays(t *testing.T) {
	t.Helper()
	saved := connectRetryDelays
	connectRetryDelays = []time.Duration{0}
	t.Cleanup(func() { connectRetryDelays = saved })
}

// flakyGateway stands in front of a real aggregator and answers its first
// failures requests as agentgateway does while the aggregator's pod is being
// replaced; every later request passes through.
func flakyGateway(t *testing.T, failures int32) (url string, requests *atomic.Int32) {
	t.Helper()
	aggregator := server.NewStreamableHTTPServer(server.NewMCPServer("aggregator", "1.0.0"), server.WithStateful(true))
	requests = &atomic.Int32{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) <= failures {
			http.Error(w, gatewayUpstreamFailure, http.StatusInternalServerError)
			return
		}
		aggregator.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp", requests
}

func TestConnectRetriesGatewayUpstreamFailure(t *testing.T) {
	noConnectRetryDelays(t)
	url, requests := flakyGateway(t, 3)

	c := NewClient(url, nil, TransportStreamableHTTP)
	defer func() { _ = c.Close() }()

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect through a gateway that recovers: %v", err)
	}
	if got := requests.Load(); got < 4 {
		t.Errorf("gateway saw %d requests, want the 3 failed initializes and a successful one", got)
	}
	if c.serverInfo == nil || c.serverInfo.Name != "aggregator" {
		t.Errorf("serverInfo = %+v, want the aggregator's", c.serverInfo)
	}
}

func TestConnectWithoutRetryBudgetFailsOnce(t *testing.T) {
	noConnectRetryDelays(t)
	url, requests := flakyGateway(t, 1)

	c := NewClient(url, nil, TransportStreamableHTTP)
	c.SetConnectRetryBudget(0)
	defer func() { _ = c.Close() }()

	err := c.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "upstream call failed") {
		t.Fatalf("Connect = %v, want the gateway's upstream failure", err)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("gateway saw %d requests, want 1", got)
	}
}

func TestConnectGivesUpWhenTheBudgetIsSpent(t *testing.T) {
	noConnectRetryDelays(t)
	url, requests := flakyGateway(t, 1<<30)

	c := NewClient(url, nil, TransportStreamableHTTP)
	c.SetConnectRetryBudget(time.Nanosecond)
	defer func() { _ = c.Close() }()

	if err := c.Connect(context.Background()); err == nil || !strings.Contains(err.Error(), "upstream call failed") {
		t.Fatalf("Connect = %v, want the gateway's upstream failure", err)
	}
	if got := requests.Load(); got < 1 {
		t.Errorf("gateway saw %d requests, want at least 1", got)
	}
}

func TestConnectDoesNotRetryARefusal(t *testing.T) {
	noConnectRetryDelays(t)
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer ts.Close()

	c := NewClient(ts.URL+"/mcp", nil, TransportStreamableHTTP)
	defer func() { _ = c.Close() }()

	if err := c.Connect(context.Background()); err == nil {
		t.Fatal("Connect to a server refusing every request succeeded")
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("server saw %d requests, want 1: a refusal is final", got)
	}
}

func TestRetryConnectStopsWithTheCallersContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := retryConnect(ctx, time.Hour, nil, func(context.Context) error {
		attempts++
		cancel()
		return syscall.ECONNREFUSED
	})
	if !errors.Is(err, syscall.ECONNREFUSED) || attempts != 1 {
		t.Errorf("retryConnect = %v after %d attempts, want the refused connection after 1", err, attempts)
	}
}

func TestIsTransientConnectError(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{name: "gateway upstream connect timeout", err: fmt.Errorf("initialization failed: request failed with status 500: %s", gatewayUpstreamFailure), want: true},
		{name: "envoy no healthy upstream", err: errors.New("request failed with status 503: no healthy upstream"), want: true},
		{name: "bad gateway", err: errors.New("request failed with status 502: bad gateway"), want: true},
		{name: "gateway timeout", err: errors.New("request failed with status 504: "), want: true},
		{name: "connection refused", err: fmt.Errorf("failed to send request: %w", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}), want: true},
		{name: "connection reset", err: fmt.Errorf("failed to send request: %w", syscall.ECONNRESET), want: true},
		{name: "attempt deadline", err: fmt.Errorf("initialization failed: %w", context.DeadlineExceeded), want: true},
		{name: "dns timeout", err: &net.DNSError{Err: "timeout", Name: "muster", IsTimeout: true}, want: true},
		{name: "no such host", err: &net.DNSError{Err: "no such host", Name: "muster", IsNotFound: true}},
		{name: "unauthorized", err: fmt.Errorf("initialization failed: %w", transport.ErrUnauthorized)},
		{name: "legacy sse server", err: transport.ErrLegacySSEServer},
		{name: "client error", err: errors.New("request failed with status 400: bad request")},
		{name: "internal error of muster itself", err: errors.New("request failed with status 500: panic")},
		{name: "caller canceled", ctx: canceled, err: syscall.ECONNREFUSED},
		{name: "no error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			if got := isTransientConnectError(ctx, tt.err); got != tt.want {
				t.Errorf("isTransientConnectError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
