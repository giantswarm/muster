package mcpserver

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/services"
)

// fakePinger stands in for the MCP client behind a Service; CheckHealth only
// needs Ping.
type fakePinger struct {
	mu   sync.Mutex
	err  error
	pins int
}

func (p *fakePinger) Ping(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pins++
	return p.err
}

func (p *fakePinger) fail(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
}

// healthRecorder captures the health values the service's state-change
// callback publishes, which is what the aggregator acts on.
type healthRecorder struct {
	mu     sync.Mutex
	health []services.HealthStatus
}

func (r *healthRecorder) callback(_ string, _, _ services.ServiceState, health services.HealthStatus, _ error) {
	r.mu.Lock()
	r.health = append(r.health, health)
	r.mu.Unlock()
}

func (r *healthRecorder) published() []services.HealthStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]services.HealthStatus(nil), r.health...)
}

func newConnectedService(t *testing.T, pinger *fakePinger) (*Service, *healthRecorder, *countingEventManager) {
	t.Helper()
	cm := &countingEventManager{counts: map[string]int{}}
	api.RegisterEventManager(cm)
	t.Cleanup(func() { api.RegisterEventManager(nil) })

	svc, err := NewService(&api.MCPServer{
		Name: "probed",
		Type: api.MCPServerTypeStreamableHTTP,
		URL:  "http://127.0.0.1:1/mcp",
	})
	require.NoError(t, err)
	svc.UpdateState(services.StateConnected, services.HealthHealthy, nil)
	if pinger != nil {
		svc.client = pinger
	}

	rec := &healthRecorder{}
	svc.SetStateChangeCallback(rec.callback)
	return svc, rec, cm
}

func healthCheckFailures(svc *Service) int {
	n, _ := svc.GetServiceData()[api.ServiceDataHealthCheckFailures].(int)
	return n
}

func TestCheckHealth_TurnsUnhealthyOnlyAtTheThreshold(t *testing.T) {
	pinger := &fakePinger{err: errors.New("connection refused")}
	svc, rec, cm := newConnectedService(t, pinger)
	ctx := context.Background()

	for i := 1; i < HealthCheckFailureThreshold; i++ {
		health, err := svc.CheckHealth(ctx)
		require.Error(t, err, "probe %d reports its failure", i)
		assert.Equal(t, services.HealthHealthy, health, "probe %d below the threshold keeps the health", i)
		assert.Equal(t, i, healthCheckFailures(svc))
	}
	assert.Empty(t, rec.published(), "no health change is published below the threshold")
	assert.Equal(t, 0, cm.count("MCPServerHealthCheckFailed"))

	health, err := svc.CheckHealth(ctx)
	require.Error(t, err)
	assert.Equal(t, services.HealthUnhealthy, health)
	assert.Equal(t, services.HealthUnhealthy, svc.GetHealth())
	assert.Equal(t, []services.HealthStatus{services.HealthUnhealthy}, rec.published())
	assert.Equal(t, 1, cm.count("MCPServerHealthCheckFailed"))

	// Still failing: no second event, no second transition.
	_, _ = svc.CheckHealth(ctx)
	assert.Equal(t, 1, cm.count("MCPServerHealthCheckFailed"))
	assert.Len(t, rec.published(), 1)
	assert.Equal(t, HealthCheckFailureThreshold+1, healthCheckFailures(svc))
}

func TestCheckHealth_PassingProbeResetsTheCount(t *testing.T) {
	pinger := &fakePinger{err: errors.New("timeout")}
	svc, rec, cm := newConnectedService(t, pinger)
	ctx := context.Background()

	_, _ = svc.CheckHealth(ctx)
	_, _ = svc.CheckHealth(ctx)
	require.Equal(t, 2, healthCheckFailures(svc))

	pinger.fail(nil)
	health, err := svc.CheckHealth(ctx)
	require.NoError(t, err)
	assert.Equal(t, services.HealthHealthy, health)
	assert.Equal(t, 0, healthCheckFailures(svc))
	assert.Empty(t, rec.published(), "healthy stayed healthy: nothing to publish")

	// A later outage starts counting from zero and emits its own event.
	pinger.fail(errors.New("connection refused"))
	for i := 0; i < HealthCheckFailureThreshold; i++ {
		_, _ = svc.CheckHealth(ctx)
	}
	assert.Equal(t, services.HealthUnhealthy, svc.GetHealth())
	assert.Equal(t, 1, cm.count("MCPServerHealthCheckFailed"))

	// Recovery publishes healthy again and re-arms the event gate.
	pinger.fail(nil)
	_, err = svc.CheckHealth(ctx)
	require.NoError(t, err)
	assert.Equal(t, []services.HealthStatus{services.HealthUnhealthy, services.HealthHealthy}, rec.published())
	for i := 0; i < HealthCheckFailureThreshold; i++ {
		pinger.fail(errors.New("connection refused"))
		_, _ = svc.CheckHealth(ctx)
	}
	assert.Equal(t, 2, cm.count("MCPServerHealthCheckFailed"))
}

func TestCheckHealth_MissingClientCountsAsFailure(t *testing.T) {
	svc, _, _ := newConnectedService(t, nil)
	ctx := context.Background()

	for i := 0; i < HealthCheckFailureThreshold; i++ {
		health, err := svc.CheckHealth(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "MCP client not available")
		if i < HealthCheckFailureThreshold-1 {
			assert.Equal(t, services.HealthHealthy, health)
		} else {
			assert.Equal(t, services.HealthUnhealthy, health)
		}
	}
}

func TestCheckHealth_StopResetsTheCount(t *testing.T) {
	pinger := &fakePinger{err: errors.New("connection refused")}
	svc, _, _ := newConnectedService(t, pinger)
	ctx := context.Background()

	_, _ = svc.CheckHealth(ctx)
	_, _ = svc.CheckHealth(ctx)
	require.Equal(t, 2, healthCheckFailures(svc))

	require.NoError(t, svc.Stop(ctx))
	assert.Equal(t, 0, healthCheckFailures(svc))
}
