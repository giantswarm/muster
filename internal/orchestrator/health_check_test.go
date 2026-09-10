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
}

var _ services.HealthChecker = (*mockHealthService)(nil)

func (m *mockHealthService) CheckHealth(context.Context) (services.HealthStatus, error) {
	m.probes.Add(1)
	return m.probeHealth, m.probeErr
}

func (m *mockHealthService) GetHealthCheckInterval() time.Duration { return time.Second }

func newHealthTestOrchestrator(t *testing.T, svcs ...services.Service) (*Orchestrator, context.CancelFunc) {
	t.Helper()
	registry := services.NewRegistry()
	for _, svc := range svcs {
		require.NoError(t, registry.Register(svc))
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Orchestrator{registry: registry, ctx: ctx}, cancel
}

func TestCheckConnectedServersHealth(t *testing.T) {
	t.Run("probes connected and running servers and leaves the recovery to the service", func(t *testing.T) {
		dead := &mockHealthService{
			mockService: mockService{name: "dead-remote", state: services.StateConnected},
			probeHealth: services.HealthUnhealthy,
			probeErr:    errors.New("MCP ping failed: connection refused"),
		}
		fine := &mockHealthService{
			mockService: mockService{name: "fine-stdio", state: services.StateRunning},
			probeHealth: services.HealthHealthy,
		}
		o, _ := newHealthTestOrchestrator(t, dead, fine)

		o.checkConnectedServersHealth()
		o.retryWg.Wait()

		assert.Equal(t, int32(1), dead.probes.Load())
		assert.Equal(t, int32(1), fine.probes.Load())
		// The service that failed its probes has put itself on the reconnect
		// schedule; the retry loop restarts it, not the probe.
		assert.Equal(t, 0, dead.GetRestartCount())
		assert.Equal(t, 0, fine.GetRestartCount())
	})

	t.Run("probes only connected or running servers", func(t *testing.T) {
		states := []services.ServiceState{
			services.StateFailed, services.StateUnreachable, services.StateAuthRequired,
			services.StateStarting, services.StateStopping, services.StateStopped, services.StateDisconnected,
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
		o, _ := newHealthTestOrchestrator(t, svcs...)

		o.checkConnectedServersHealth()
		o.retryWg.Wait()

		for _, m := range mocks {
			assert.Equal(t, int32(0), m.probes.Load(), "%s must not be probed", m.name)
		}
	})

	t.Run("skips services that do not implement HealthChecker", func(t *testing.T) {
		svc := &mockServiceWithData{
			mockService: mockService{name: "plain", state: services.StateConnected},
			serviceData: map[string]interface{}{},
		}
		o, _ := newHealthTestOrchestrator(t, svc)

		o.checkConnectedServersHealth()
		o.retryWg.Wait()

		assert.Equal(t, 0, svc.GetRestartCount())
	})

	t.Run("skips probing when the orchestrator is shutting down", func(t *testing.T) {
		svc := &mockHealthService{
			mockService: mockService{name: "remote", state: services.StateConnected},
			probeHealth: services.HealthUnhealthy,
		}
		o, cancel := newHealthTestOrchestrator(t, svc)
		cancel()

		o.checkConnectedServersHealth()
		o.retryWg.Wait()

		assert.Equal(t, int32(0), svc.probes.Load())
	})
}

// TestRetryLoopProbesOnTheHealthTicker wires the maintenance loop with a short
// probe interval and asserts that connected servers are probed by it.
func TestRetryLoopProbesOnTheHealthTicker(t *testing.T) {
	previous := HealthCheckInterval
	HealthCheckInterval = 5 * time.Millisecond
	t.Cleanup(func() { HealthCheckInterval = previous })

	svc := &mockHealthService{
		mockService: mockService{name: "remote", state: services.StateConnected},
		probeHealth: services.HealthHealthy,
	}
	o, cancel := newHealthTestOrchestrator(t, svc)

	done := make(chan struct{})
	go func() {
		o.retryFailedMCPServers()
		close(done)
	}()

	require.Eventually(t, func() bool { return svc.probes.Load() >= 2 }, 2*time.Second, time.Millisecond,
		"the health ticker must drive probes of the connected server")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retry loop did not stop after the context was cancelled")
	}
}

// TestFailedProbesFeedTheRetryLoop: a service that failed its probes reports
// Failed with a reconnect due now, the shape mcpserver.Service produces, and
// the existing retry loop restarts it.
func TestFailedProbesFeedTheRetryLoop(t *testing.T) {
	svc := &mockServiceWithData{
		mockService: mockService{name: "probed-out", state: services.StateFailed},
		serviceData: map[string]interface{}{
			"nextRetryAfter": time.Now(),
		},
	}
	o, _ := newHealthTestOrchestrator(t, svc)

	o.attemptReconnectFailedServers()
	o.retryWg.Wait()

	assert.Equal(t, 1, svc.GetRestartCount())
}
