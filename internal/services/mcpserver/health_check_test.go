package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/services"
)

// fakeProbeClient stands in for the MCP client behind a Service. CheckHealth
// needs Ping, the modern-protocol path needs NegotiatedProtocolVersion and
// ListTools, and the threshold closes the client.
type fakeProbeClient struct {
	mu      sync.Mutex
	pingErr error
	listErr error
	version string
	pings   int
	lists   int
	closes  int
}

func (p *fakeProbeClient) Ping(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pings++
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.pingErr
}

func (p *fakeProbeClient) ListTools(context.Context) ([]mcp.Tool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lists++
	return nil, p.listErr
}

func (p *fakeProbeClient) NegotiatedProtocolVersion() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version
}

func (p *fakeProbeClient) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closes++
	return nil
}

func (p *fakeProbeClient) fail(err error) {
	p.mu.Lock()
	p.pingErr = err
	p.mu.Unlock()
}

func (p *fakeProbeClient) counts() (pings, lists, closes int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pings, p.lists, p.closes
}

// transitionRecorder captures the state changes the service publishes, which
// is what the aggregator and the reconciler act on.
type transitionRecorder struct {
	mu          sync.Mutex
	transitions []string
}

func (r *transitionRecorder) callback(_ string, _, newState services.ServiceState, health services.HealthStatus, _ error) {
	r.mu.Lock()
	r.transitions = append(r.transitions, string(newState)+"/"+string(health))
	r.mu.Unlock()
}

func (r *transitionRecorder) published() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.transitions...)
}

func newConnectedService(t *testing.T, def *api.MCPServer, client interface{}) (*Service, *transitionRecorder, *countingEventManager) {
	t.Helper()
	cm := &countingEventManager{counts: map[string]int{}}
	api.RegisterEventManager(cm)
	t.Cleanup(func() { api.RegisterEventManager(nil) })

	if def == nil {
		def = &api.MCPServer{Name: "probed", Type: api.MCPServerTypeStreamableHTTP, URL: "http://127.0.0.1:1/mcp"}
	}
	svc, err := NewService(def)
	require.NoError(t, err)
	svc.UpdateState(services.StateConnected, services.HealthHealthy, nil)
	if client != nil {
		svc.client = client
	}

	rec := &transitionRecorder{}
	svc.SetStateChangeCallback(rec.callback)
	return svc, rec, cm
}

func healthCheckFailures(svc *Service) int {
	n, _ := svc.GetServiceData()[api.ServiceDataHealthCheckFailures].(int)
	return n
}

func TestCheckHealth_FailsTheServerAtTheThreshold(t *testing.T) {
	client := &fakeProbeClient{pingErr: errors.New("connection refused")}
	svc, rec, cm := newConnectedService(t, nil, client)
	ctx := context.Background()

	for i := 1; i < HealthCheckFailureThreshold; i++ {
		health, err := svc.CheckHealth(ctx)
		require.Error(t, err, "probe %d reports its failure", i)
		assert.Equal(t, services.HealthHealthy, health, "probe %d below the threshold keeps the health", i)
		assert.Equal(t, services.StateConnected, svc.GetState())
		assert.Equal(t, i, healthCheckFailures(svc))
	}
	assert.Empty(t, rec.published(), "nothing is published below the threshold")
	assert.Equal(t, 0, cm.count("MCPServerHealthCheckFailed"))

	before := time.Now()
	health, err := svc.CheckHealth(ctx)
	require.Error(t, err)
	assert.Equal(t, services.HealthUnhealthy, health)

	// The client is closed and the server is Failed with a reconnect due now,
	// the shape the retry loop, the aggregator and the reconciler already act on.
	_, _, closes := client.counts()
	assert.Equal(t, 1, closes)
	assert.False(t, svc.IsClientReady())
	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.Equal(t, services.HealthUnhealthy, svc.GetHealth())
	assert.Equal(t, []string{"failed/unhealthy"}, rec.published())
	data := svc.GetServiceData()
	next, ok := data[api.ServiceDataNextRetryAfter].(time.Time)
	require.True(t, ok, "a reconnect is scheduled")
	assert.False(t, next.After(time.Now()), "the reconnect is due at once")
	assert.False(t, next.Before(before))
	assert.Equal(t, 0, data[api.ServiceDataConsecutiveFailures], "the probes do not inflate the reconnect backoff")
	assert.Equal(t, HealthCheckFailureThreshold, healthCheckFailures(svc))
	assert.True(t, svc.isReconnectingAfterProbe())
	assert.Equal(t, 1, cm.count("MCPServerHealthCheckFailed"))

	// Failed is not Running: a probe that still arrives is not counted.
	_, _ = svc.CheckHealth(ctx)
	assert.Equal(t, HealthCheckFailureThreshold, healthCheckFailures(svc))
	assert.Equal(t, 1, cm.count("MCPServerHealthCheckFailed"))
}

func TestCheckHealth_PassingProbeResetsTheCount(t *testing.T) {
	client := &fakeProbeClient{pingErr: errors.New("timeout")}
	svc, rec, _ := newConnectedService(t, nil, client)
	ctx := context.Background()

	_, _ = svc.CheckHealth(ctx)
	_, _ = svc.CheckHealth(ctx)
	require.Equal(t, 2, healthCheckFailures(svc))

	client.fail(nil)
	health, err := svc.CheckHealth(ctx)
	require.NoError(t, err)
	assert.Equal(t, services.HealthHealthy, health)
	assert.Equal(t, 0, healthCheckFailures(svc))
	assert.Equal(t, services.StateConnected, svc.GetState())
	assert.Empty(t, rec.published(), "healthy stayed healthy: nothing to publish")

	// The next outage is counted from zero.
	client.fail(errors.New("connection refused"))
	for i := 1; i < HealthCheckFailureThreshold; i++ {
		_, _ = svc.CheckHealth(ctx)
		assert.Equal(t, services.StateConnected, svc.GetState())
	}
	_, _ = svc.CheckHealth(ctx)
	assert.Equal(t, services.StateFailed, svc.GetState())
}

func TestCheckHealth_SkipsServersWithoutASharedClient(t *testing.T) {
	t.Run("no client: a per-session OAuth server synced to Connected by a login", func(t *testing.T) {
		svc, rec, cm := newConnectedService(t, nil, nil)

		for i := 0; i <= HealthCheckFailureThreshold; i++ {
			health, err := svc.CheckHealth(context.Background())
			require.NoError(t, err)
			assert.Equal(t, services.HealthHealthy, health)
		}
		assert.Equal(t, 0, healthCheckFailures(svc))
		assert.Equal(t, services.StateConnected, svc.GetState())
		assert.Empty(t, rec.published())
		assert.Equal(t, 0, cm.count("MCPServerHealthCheckFailed"))
	})

	t.Run("session-level auth: served per session, never probed", func(t *testing.T) {
		client := &fakeProbeClient{pingErr: errors.New("connection refused")}
		def := &api.MCPServer{
			Name: "sso", Type: api.MCPServerTypeStreamableHTTP, URL: "http://127.0.0.1:1/mcp",
			Auth: &api.MCPServerAuth{ForwardToken: true},
		}
		svc, _, _ := newConnectedService(t, def, client)

		for i := 0; i <= HealthCheckFailureThreshold; i++ {
			_, err := svc.CheckHealth(context.Background())
			require.NoError(t, err)
		}
		pings, _, _ := client.counts()
		assert.Equal(t, 0, pings)
		assert.Equal(t, 0, healthCheckFailures(svc))
		assert.Equal(t, services.StateConnected, svc.GetState())
	})
}

func TestCheckHealth_NotCountedWhenTheServerIsNotRunning(t *testing.T) {
	client := &fakeProbeClient{pingErr: errors.New("connection refused")}
	svc, _, _ := newConnectedService(t, nil, client)
	svc.UpdateState(services.StateStopping, services.HealthHealthy, nil)

	health, err := svc.CheckHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, services.HealthHealthy, health)
	assert.Equal(t, 0, healthCheckFailures(svc))
}

func TestCheckHealth_DoesNotCountAnswersOrCancellations(t *testing.T) {
	t.Run("a JSON-RPC error reply is an answer from a live server", func(t *testing.T) {
		client := &fakeProbeClient{pingErr: mcp.ErrMethodNotFound}
		svc, _, _ := newConnectedService(t, nil, client)

		for i := 0; i <= HealthCheckFailureThreshold; i++ {
			health, err := svc.CheckHealth(context.Background())
			require.NoError(t, err)
			assert.Equal(t, services.HealthHealthy, health)
		}
		assert.Equal(t, 0, healthCheckFailures(svc))
		assert.Equal(t, services.StateConnected, svc.GetState())
	})

	t.Run("a probe cancelled by its caller says nothing about the backend", func(t *testing.T) {
		client := &fakeProbeClient{}
		svc, _, _ := newConnectedService(t, nil, client)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		for i := 0; i <= HealthCheckFailureThreshold; i++ {
			health, err := svc.CheckHealth(ctx)
			require.NoError(t, err)
			assert.Equal(t, services.HealthHealthy, health)
		}
		assert.Equal(t, 0, healthCheckFailures(svc))
	})

	t.Run("the probe's own deadline is a failed probe", func(t *testing.T) {
		client := &fakeProbeClient{pingErr: context.DeadlineExceeded}
		svc, _, _ := newConnectedService(t, nil, client)

		_, err := svc.CheckHealth(context.Background())
		require.Error(t, err)
		assert.Equal(t, 1, healthCheckFailures(svc))
	})
}

func TestCheckHealth_ProbesModernProtocolWithToolsList(t *testing.T) {
	client := &fakeProbeClient{version: mcp.ProtocolVersion20260728, pingErr: errors.New("ping must not be used")}
	svc, _, _ := newConnectedService(t, nil, client)

	health, err := svc.CheckHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, services.HealthHealthy, health)
	pings, lists, _ := client.counts()
	assert.Equal(t, 0, pings, "mcp-go answers ping locally on 2026-07-28; it proves nothing")
	assert.Equal(t, 1, lists)

	client.mu.Lock()
	client.listErr = errors.New("connection refused")
	client.mu.Unlock()
	_, err = svc.CheckHealth(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MCP tools/list failed")
	assert.Equal(t, 1, healthCheckFailures(svc))
}

func TestCheckHealth_ProbeUsesTheServersTimeout(t *testing.T) {
	def := &api.MCPServer{Name: "slow", Type: api.MCPServerTypeStreamableHTTP, URL: "http://127.0.0.1:1/mcp", Timeout: 60}
	svc, _, _ := newConnectedService(t, def, &fakeProbeClient{})

	probeCtx, cancel := svc.probeContext(context.Background())
	defer cancel()
	deadline, ok := probeCtx.Deadline()
	require.True(t, ok)
	assert.Greater(t, time.Until(deadline), 55*time.Second, "spec.timeout bounds the probe, not a fixed 10s")
}

func TestCheckHealth_StopResetsTheCount(t *testing.T) {
	client := &fakeProbeClient{pingErr: errors.New("connection refused")}
	svc, _, _ := newConnectedService(t, nil, client)
	ctx := context.Background()

	_, _ = svc.CheckHealth(ctx)
	_, _ = svc.CheckHealth(ctx)
	require.Equal(t, 2, healthCheckFailures(svc))

	require.NoError(t, svc.Stop(ctx))
	assert.Equal(t, 0, healthCheckFailures(svc))
}

// TestStart_AfterFailedProbesRetriesAnyError: a reconnect that follows failed
// probes is scheduled again whatever the endpoint answered. Here it is a 404
// on initialize, which mcp-go reports as a legacy-SSE server and which is not
// a transient connectivity error; before, such a start left the server Failed
// with no schedule and it was never retried.
func TestStart_AfterFailedProbesRetriesAnyError(t *testing.T) {
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "route not found", http.StatusNotFound)
	}))
	defer notFound.Close()
	def := &api.MCPServer{Name: "behind-route", Type: api.MCPServerTypeStreamableHTTP, URL: notFound.URL + "/mcp", Timeout: 5}

	t.Run("a first connect against the 404 is not scheduled (unchanged)", func(t *testing.T) {
		svc, err := NewService(def)
		require.NoError(t, err)

		require.Error(t, svc.Start(context.Background()))
		assert.Equal(t, services.StateFailed, svc.GetState())
		_, scheduled := svc.GetServiceData()[api.ServiceDataNextRetryAfter]
		assert.False(t, scheduled)
	})

	t.Run("a reconnect after failed probes is scheduled", func(t *testing.T) {
		client := &fakeProbeClient{pingErr: errors.New("connection refused")}
		svc, _, _ := newConnectedService(t, def, client)
		for i := 0; i < HealthCheckFailureThreshold; i++ {
			_, _ = svc.CheckHealth(context.Background())
		}
		require.Equal(t, services.StateFailed, svc.GetState())

		// The retry loop restarts it; Restart skips Stop for a Failed service
		// and calls Start, which runs into the 404.
		err := svc.Start(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "4xx for initialize POST")
		assert.Equal(t, services.StateFailed, svc.GetState())
		data := svc.GetServiceData()
		next, scheduled := data[api.ServiceDataNextRetryAfter].(time.Time)
		require.True(t, scheduled, "the failed reconnect is on the backoff schedule")
		assert.True(t, next.After(time.Now()))
		assert.Equal(t, 1, data[api.ServiceDataConsecutiveFailures])
		assert.True(t, svc.isReconnectingAfterProbe(), "the flag stays until an attempt reaches the endpoint")
	})
}
