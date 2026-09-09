package orchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/services"
)

// mockHealthService is a mockService that also implements
// services.HealthChecker, answering every probe with a fixed result.
type mockHealthService struct {
	mockService
	probeHealth services.HealthStatus
	probeErr    error
	probes      atomic.Int32
	// block, when non-nil, holds every probe until it is closed.
	block chan struct{}
}

var _ services.HealthChecker = (*mockHealthService)(nil)

func (m *mockHealthService) CheckHealth(context.Context) (services.HealthStatus, error) {
	m.probes.Add(1)
	if m.block != nil {
		<-m.block
	}
	return m.probeHealth, m.probeErr
}

func (m *mockHealthService) GetHealthCheckInterval() time.Duration { return time.Second }

func newHealthTestOrchestrator(t *testing.T, svcs ...services.Service) *Orchestrator {
	t.Helper()
	registry := services.NewRegistry()
	for _, svc := range svcs {
		require.NoError(t, registry.Register(svc))
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Orchestrator{registry: registry, ctx: ctx}
}

func TestCheckConnectedServersHealth(t *testing.T) {
	t.Run("restarts a connected server that reports unhealthy", func(t *testing.T) {
		svc := &mockHealthService{
			mockService: mockService{name: "dead-remote", state: services.StateConnected},
			probeHealth: services.HealthUnhealthy,
			probeErr:    errors.New("MCP ping failed: connection refused"),
		}
		o := newHealthTestOrchestrator(t, svc)

		o.checkConnectedServersHealth()
		o.retryWg.Wait()

		assert.Equal(t, int32(1), svc.probes.Load())
		assert.Equal(t, 1, svc.GetRestartCount())
	})

	t.Run("leaves a healthy server alone", func(t *testing.T) {
		svc := &mockHealthService{
			mockService: mockService{name: "fine-remote", state: services.StateConnected},
			probeHealth: services.HealthHealthy,
		}
		o := newHealthTestOrchestrator(t, svc)

		o.checkConnectedServersHealth()
		o.retryWg.Wait()

		assert.Equal(t, int32(1), svc.probes.Load())
		assert.Equal(t, 0, svc.GetRestartCount())
	})

	t.Run("a failed probe below the service's threshold triggers no restart", func(t *testing.T) {
		svc := &mockHealthService{
			mockService: mockService{name: "flaky-remote", state: services.StateRunning},
			probeHealth: services.HealthHealthy,
			probeErr:    errors.New("MCP ping failed: timeout"),
		}
		o := newHealthTestOrchestrator(t, svc)

		o.checkConnectedServersHealth()
		o.retryWg.Wait()

		assert.Equal(t, 0, svc.GetRestartCount())
	})

	t.Run("probes only connected or running servers", func(t *testing.T) {
		states := []services.ServiceState{
			services.StateFailed, services.StateUnreachable, services.StateAuthRequired,
			services.StateStarting, services.StateStopped, services.StateDisconnected,
		}
		var svcs []services.Service
		var mocks []*mockHealthService
		for _, state := range states {
			m := &mockHealthService{
				mockService: mockService{name: "server-" + string(state), state: state},
				probeHealth: services.HealthUnhealthy,
			}
			mocks = append(mocks, m)
			svcs = append(svcs, m)
		}
		o := newHealthTestOrchestrator(t, svcs...)

		o.checkConnectedServersHealth()
		o.retryWg.Wait()

		for _, m := range mocks {
			assert.Equal(t, int32(0), m.probes.Load(), "%s must not be probed", m.name)
			assert.Equal(t, 0, m.GetRestartCount(), "%s must not be restarted", m.name)
		}
	})

	t.Run("skips services that do not implement HealthChecker", func(t *testing.T) {
		svc := &mockServiceWithData{
			mockService: mockService{name: "plain", state: services.StateConnected},
			serviceData: map[string]interface{}{},
		}
		o := newHealthTestOrchestrator(t, svc)

		o.checkConnectedServersHealth()
		o.retryWg.Wait()

		assert.Equal(t, 0, svc.GetRestartCount())
	})

	t.Run("does not stack a probe on one still in flight", func(t *testing.T) {
		svc := &mockHealthService{
			mockService: mockService{name: "slow-remote", state: services.StateConnected},
			probeHealth: services.HealthUnhealthy,
			block:       make(chan struct{}),
		}
		o := newHealthTestOrchestrator(t, svc)

		o.checkConnectedServersHealth()
		o.checkConnectedServersHealth()
		require.Eventually(t, func() bool { return svc.probes.Load() == 1 }, time.Second, time.Millisecond)

		close(svc.block)
		o.retryWg.Wait()

		assert.Equal(t, int32(1), svc.probes.Load(), "the second tick must not probe while the first probe runs")
		assert.Equal(t, 1, svc.GetRestartCount())

		// The slot is free again once the probe and its restart are done.
		o.checkConnectedServersHealth()
		o.retryWg.Wait()
		assert.Equal(t, int32(2), svc.probes.Load())
	})

	t.Run("skips probing when the orchestrator is shutting down", func(t *testing.T) {
		svc := &mockHealthService{
			mockService: mockService{name: "remote", state: services.StateConnected},
			probeHealth: services.HealthUnhealthy,
		}
		registry := services.NewRegistry()
		require.NoError(t, registry.Register(svc))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		o := &Orchestrator{registry: registry, ctx: ctx}

		o.checkConnectedServersHealth()
		o.retryWg.Wait()

		assert.Equal(t, int32(0), svc.probes.Load())
		assert.Equal(t, 0, svc.GetRestartCount())
	})
}
