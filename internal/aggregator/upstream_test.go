package aggregator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testProxyBase = "http://localhost:8080"
	testProxyDial = "http://localhost:8080/mcp/kubernetes"
)

func TestProxyURLFor(t *testing.T) {
	tests := []struct {
		name, proxy, server, want string
	}{
		{name: "happy path", proxy: testProxyBase, server: "kubernetes", want: testProxyDial},
		{name: "trailing slash trimmed", proxy: testProxyBase + "/", server: "kubernetes", want: testProxyDial},
		{name: "multiple trailing slashes", proxy: testProxyBase + "///", server: "kubernetes", want: testProxyDial},
		{name: "cluster-mode service", proxy: "http://muster-agw.muster.svc.cluster.local:8080", server: "github", want: "http://muster-agw.muster.svc.cluster.local:8080/mcp/github"},
		{name: "empty proxy yields relative", proxy: "", server: "x", want: "/mcp/x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := proxyURLFor(tc.proxy, tc.server); got != tc.want {
				t.Errorf("proxyURLFor(%q, %q) = %q, want %q", tc.proxy, tc.server, got, tc.want)
			}
		})
	}
}

func TestReadinessURLFor_ZeroPortIsClusterMode(t *testing.T) {
	require.Empty(t, readinessURLFor(0), "zero port must yield empty URL so cluster mode skips probing")
}

func TestWaitForAgentgatewayReady_EmptyURLNoop(t *testing.T) {
	require.NoError(t, waitForAgentgatewayReady(t.Context(), nil, ""))
}

func TestWaitForAgentgatewayReady_PollsUntilHealthy(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	require.NoError(t, waitForAgentgatewayReady(t.Context(), srv.Client(), srv.URL))
	require.GreaterOrEqual(t, atomic.LoadInt32(&hits), int32(3))
}

func TestWaitForAgentgatewayReady_DeadlineFires(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := waitForAgentgatewayReady(ctx, srv.Client(), srv.URL)
	require.Error(t, err, "must surface the timeout when agentgateway never reports ready")
}
