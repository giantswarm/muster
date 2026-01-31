package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"muster/internal/services"
)

// mockService implements services.Service for testing
type mockService struct {
	name         string
	state        services.ServiceState
	health       services.HealthStatus
	restartCount int
	restartErr   error
	restartMu    sync.Mutex
}

func (m *mockService) Start(ctx context.Context) error                              { return nil }
func (m *mockService) Stop(ctx context.Context) error                               { return nil }
func (m *mockService) GetState() services.ServiceState                              { return m.state }
func (m *mockService) GetHealth() services.HealthStatus                             { return m.health }
func (m *mockService) GetLastError() error                                          { return nil }
func (m *mockService) GetName() string                                              { return m.name }
func (m *mockService) GetType() services.ServiceType                                { return services.TypeMCPServer }
func (m *mockService) GetDependencies() []string                                    { return nil }
func (m *mockService) SetStateChangeCallback(callback services.StateChangeCallback) {}

func (m *mockService) Restart(ctx context.Context) error {
	m.restartMu.Lock()
	defer m.restartMu.Unlock()
	m.restartCount++
	return m.restartErr
}

func (m *mockService) GetRestartCount() int {
	m.restartMu.Lock()
	defer m.restartMu.Unlock()
	return m.restartCount
}

// mockServiceWithData implements services.Service and services.ServiceDataProvider
type mockServiceWithData struct {
	mockService
	serviceData map[string]interface{}
}

func (m *mockServiceWithData) GetServiceData() map[string]interface{} {
	return m.serviceData
}

func TestShouldAttemptRetry(t *testing.T) {
	tests := []struct {
		name           string
		service        services.Service
		expectedResult bool
	}{
		{
			name: "returns false for running service",
			service: &mockServiceWithData{
				mockService: mockService{
					name:  "running-server",
					state: services.StateRunning,
				},
				serviceData: map[string]interface{}{
					"nextRetryAfter": time.Now().Add(-1 * time.Minute),
				},
			},
			expectedResult: false,
		},
		{
			name: "returns false for stopped service",
			service: &mockServiceWithData{
				mockService: mockService{
					name:  "stopped-server",
					state: services.StateStopped,
				},
				serviceData: map[string]interface{}{
					"nextRetryAfter": time.Now().Add(-1 * time.Minute),
				},
			},
			expectedResult: false,
		},
		{
			name: "returns false for service without ServiceDataProvider",
			service: &mockService{
				name:  "no-data-provider",
				state: services.StateFailed,
			},
			expectedResult: false,
		},
		{
			name: "returns false for failed service with nil serviceData",
			service: &mockServiceWithData{
				mockService: mockService{
					name:  "nil-data-server",
					state: services.StateFailed,
				},
				serviceData: nil,
			},
			expectedResult: false,
		},
		{
			name: "returns false for failed service without nextRetryAfter",
			service: &mockServiceWithData{
				mockService: mockService{
					name:  "no-retry-server",
					state: services.StateFailed,
				},
				serviceData: map[string]interface{}{
					"someOtherKey": "value",
				},
			},
			expectedResult: false,
		},
		{
			name: "returns false for failed service with invalid nextRetryAfter type",
			service: &mockServiceWithData{
				mockService: mockService{
					name:  "invalid-type-server",
					state: services.StateFailed,
				},
				serviceData: map[string]interface{}{
					"nextRetryAfter": "not-a-time",
				},
			},
			expectedResult: false,
		},
		{
			name: "returns false for failed service with future nextRetryAfter",
			service: &mockServiceWithData{
				mockService: mockService{
					name:  "future-retry-server",
					state: services.StateFailed,
				},
				serviceData: map[string]interface{}{
					"nextRetryAfter": time.Now().Add(5 * time.Minute),
				},
			},
			expectedResult: false,
		},
		{
			name: "returns true for failed service with expired nextRetryAfter",
			service: &mockServiceWithData{
				mockService: mockService{
					name:  "expired-retry-server",
					state: services.StateFailed,
				},
				serviceData: map[string]interface{}{
					"nextRetryAfter": time.Now().Add(-1 * time.Minute),
				},
			},
			expectedResult: true,
		},
		{
			name: "returns true for unreachable service with expired nextRetryAfter",
			service: &mockServiceWithData{
				mockService: mockService{
					name:  "unreachable-server",
					state: services.StateUnreachable,
				},
				serviceData: map[string]interface{}{
					"nextRetryAfter": time.Now().Add(-1 * time.Minute),
				},
			},
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Orchestrator{}
			result := o.shouldAttemptRetry(tt.service)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestAttemptReconnectFailedServers(t *testing.T) {
	t.Run("retries eligible services", func(t *testing.T) {
		registry := services.NewRegistry()

		// Create an eligible service (failed with expired backoff)
		eligibleService := &mockServiceWithData{
			mockService: mockService{
				name:  "eligible-server",
				state: services.StateFailed,
			},
			serviceData: map[string]interface{}{
				"nextRetryAfter": time.Now().Add(-1 * time.Minute),
			},
		}
		require.NoError(t, registry.Register(eligibleService))

		// Create an ineligible service (running)
		runningService := &mockServiceWithData{
			mockService: mockService{
				name:  "running-server",
				state: services.StateRunning,
			},
			serviceData: map[string]interface{}{},
		}
		require.NoError(t, registry.Register(runningService))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		o := &Orchestrator{
			registry: registry,
			ctx:      ctx,
		}

		o.attemptReconnectFailedServers()

		// Wait for goroutine to complete
		o.retryWg.Wait()

		// Only the eligible service should have been restarted
		assert.Equal(t, 1, eligibleService.GetRestartCount())
		assert.Equal(t, 0, runningService.GetRestartCount())
	})

	t.Run("skips retry when context is cancelled", func(t *testing.T) {
		registry := services.NewRegistry()

		eligibleService := &mockServiceWithData{
			mockService: mockService{
				name:  "eligible-server",
				state: services.StateFailed,
			},
			serviceData: map[string]interface{}{
				"nextRetryAfter": time.Now().Add(-1 * time.Minute),
			},
		}
		require.NoError(t, registry.Register(eligibleService))

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		o := &Orchestrator{
			registry: registry,
			ctx:      ctx,
		}

		o.attemptReconnectFailedServers()
		o.retryWg.Wait()

		// Service should not be restarted because context was cancelled
		assert.Equal(t, 0, eligibleService.GetRestartCount())
	})

	t.Run("respects max concurrent retries", func(t *testing.T) {
		registry := services.NewRegistry()

		// Create more services than MaxConcurrentRetries
		numServices := MaxConcurrentRetries + 3
		serviceList := make([]*mockServiceWithData, numServices)

		for i := 0; i < numServices; i++ {
			svc := &mockServiceWithData{
				mockService: mockService{
					name:  fmt.Sprintf("server-%d", i),
					state: services.StateFailed,
				},
				serviceData: map[string]interface{}{
					"nextRetryAfter": time.Now().Add(-1 * time.Minute),
				},
			}
			serviceList[i] = svc
			require.NoError(t, registry.Register(svc))
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		o := &Orchestrator{
			registry: registry,
			ctx:      ctx,
		}

		o.attemptReconnectFailedServers()
		o.retryWg.Wait()

		// Count how many services were restarted
		restartedCount := 0
		for _, svc := range serviceList {
			if svc.GetRestartCount() > 0 {
				restartedCount++
			}
		}

		// Should respect the max concurrent retries limit
		assert.LessOrEqual(t, restartedCount, MaxConcurrentRetries,
			"should not retry more than MaxConcurrentRetries services at once")
	})
}

func TestRetryFailedMCPServers_Shutdown(t *testing.T) {
	t.Run("waits for in-flight retries on shutdown", func(t *testing.T) {
		registry := services.NewRegistry()

		ctx, cancel := context.WithCancel(context.Background())

		o := &Orchestrator{
			registry: registry,
			ctx:      ctx,
		}

		// Start the retry loop
		done := make(chan struct{})
		go func() {
			o.retryFailedMCPServers()
			close(done)
		}()

		// Give the loop time to start
		time.Sleep(10 * time.Millisecond)

		// Cancel the context
		cancel()

		// The loop should exit cleanly
		select {
		case <-done:
			// Success - loop exited
		case <-time.After(2 * time.Second):
			t.Fatal("retryFailedMCPServers did not exit after context cancellation")
		}
	})
}
