package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/services"
)

// blockingHealthService answers a probe only once the probe's context is
// cancelled, and records that it returned.
type blockingHealthService struct {
	mockService
	probing  chan struct{}
	returned atomic.Bool
}

func (m *blockingHealthService) CheckHealth(ctx context.Context) (services.HealthStatus, error) {
	select {
	case m.probing <- struct{}{}:
	default:
	}
	<-ctx.Done()
	m.returned.Store(true)
	return services.HealthHealthy, nil
}

func (m *blockingHealthService) GetHealthCheckInterval() time.Duration { return time.Second }

// TestStopWaitsForTheMaintenanceLoop: Stop returns only after the loop that
// Start launched and the probes it started have finished, so nothing of a
// stopped orchestrator still runs (and reads shared state) afterwards.
func TestStopWaitsForTheMaintenanceLoop(t *testing.T) {
	api.RegisterMCPServerManager(&mockMCPServerManager{servers: map[string]api.MCPServerInfo{}})
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	svc := &blockingHealthService{
		mockService: mockService{name: "slow-remote", state: services.StateConnected},
		probing:     make(chan struct{}),
	}
	o := New(Config{Aggregator: config.AggregatorConfig{}})
	o.retryInterval = time.Hour
	o.healthCheckInterval = time.Millisecond
	require.NoError(t, o.registry.Register(svc))

	require.NoError(t, o.Start(context.Background()))
	select {
	case <-svc.probing:
	case <-time.After(5 * time.Second):
		t.Fatal("the maintenance loop never probed the connected server")
	}

	require.NoError(t, o.Stop())
	assert.True(t, svc.returned.Load(), "Stop returned while a health probe was still running")
}
