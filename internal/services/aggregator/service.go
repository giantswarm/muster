package aggregator

import (
	"context"
	"fmt"
	"sync"
	"time"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/muster/internal/aggregator"
	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/services"
	"github.com/giantswarm/muster/internal/teleport"
	"github.com/giantswarm/muster/pkg/logging"
)

// AggregatorService implements the Service interface for the MCP aggregator
// This is a thin wrapper around AggregatorManager that handles only lifecycle management
type AggregatorService struct {
	*services.BaseService

	mu              sync.RWMutex
	config          aggregator.AggregatorConfig
	orchestratorAPI api.OrchestratorAPI
	serviceRegistry api.ServiceRegistryHandler
	manager         *aggregator.AggregatorManager

	// Optional CR-driven transport dispatcher (TB-7/TB-8). Stored here at
	// construction time / via SetTransportDispatcher because the aggregator
	// server only exists after Start(); callers that wire the dispatcher
	// before Start would otherwise silently no-op (manager == nil at that
	// point — which masked Teleport routing in the muster-tb pilot, see
	// PLAN §6 TB-7).
	transportDispatcher teleport.TransportDispatcher
	transportClient     ctrlclient.Client
}

// NewAggregatorService creates a new aggregator service
func NewAggregatorService(
	config aggregator.AggregatorConfig,
	orchestratorAPI api.OrchestratorAPI,
	serviceRegistry api.ServiceRegistryHandler,
) *AggregatorService {
	return &AggregatorService{
		BaseService:     services.NewBaseService("mcp-aggregator", services.ServiceType("Aggregator"), []string{}),
		config:          config,
		orchestratorAPI: orchestratorAPI,
		serviceRegistry: serviceRegistry,
	}
}

// Start starts the aggregator service
func (s *AggregatorService) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.GetState() == services.StateRunning {
		return nil
	}

	s.UpdateState(services.StateStarting, services.HealthUnknown, nil)

	// Check if APIs are set
	if s.orchestratorAPI == nil {
		s.UpdateState(services.StateFailed, services.HealthUnhealthy, fmt.Errorf("APIs not set"))
		return fmt.Errorf("aggregator APIs not set")
	}

	// Create the manager with APIs
	s.manager = aggregator.NewAggregatorManager(s.config, s.orchestratorAPI, s.serviceRegistry, s.onManagerErrorCallback)

	// Start the manager
	if err := s.manager.Start(ctx); err != nil {
		s.UpdateState(services.StateFailed, services.HealthUnhealthy, err)
		return fmt.Errorf("failed to start aggregator manager: %w", err)
	}

	// If a CR-driven transport dispatcher was set before Start(), propagate
	// it to the freshly-created aggregator server now. This is the path used
	// by services.go bootstrapping in Kubernetes mode: the dispatcher is
	// constructed eagerly but the manager / server only exist after Start.
	if s.transportDispatcher != nil {
		if srv := s.manager.GetAggregatorServer(); srv != nil {
			srv.SetTransportDispatcher(s.transportDispatcher, s.transportClient)
			logging.Info("Aggregator-Service", "CR-driven transport dispatcher applied to aggregator server")
		} else {
			logging.Warn("Aggregator-Service", "Aggregator server unavailable after manager start; transport dispatcher not applied")
		}
	}

	s.UpdateState(services.StateRunning, services.HealthHealthy, nil)

	logging.Info("Aggregator-Service", "Started MCP aggregator service")
	return nil
}

// SetTransportDispatcher stores the CR-driven transport dispatcher on this
// service. If Start() has already created the underlying aggregator server,
// the dispatcher is propagated immediately; otherwise it is buffered and
// applied during Start().
//
// Idempotent. Safe to call from bootstrapping code that runs before
// Orchestrator.Start().
func (s *AggregatorService) SetTransportDispatcher(d teleport.TransportDispatcher, c ctrlclient.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transportDispatcher = d
	s.transportClient = c
	if s.manager != nil {
		if srv := s.manager.GetAggregatorServer(); srv != nil {
			srv.SetTransportDispatcher(d, c)
		}
	}
}

// onManagerErrorCallback is called when the underlying aggregator manager encounters an error
func (s *AggregatorService) onManagerErrorCallback(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update state to failed if not already in a terminal state
	if s.GetState() != services.StateFailed && s.GetState() != services.StateStopping {
		s.UpdateState(services.StateFailed, services.HealthUnhealthy, err)
		logging.Error("Aggregator-Service", err, "Aggregator manager encountered an error")
	}
}

// Stop stops the aggregator service
func (s *AggregatorService) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.GetState() != services.StateRunning {
		return nil
	}

	s.UpdateState(services.StateStopping, s.GetHealth(), nil)

	// Stop the manager
	if s.manager != nil {
		if err := s.manager.Stop(ctx); err != nil {
			logging.Error("Aggregator-Service", err, "Error stopping aggregator manager")
		}
	}

	s.UpdateState(services.StateStopped, services.HealthUnknown, nil)

	logging.Info("Aggregator-Service", "Stopped MCP aggregator service")
	return nil
}

// Restart restarts the aggregator service
func (s *AggregatorService) Restart(ctx context.Context) error {
	if err := s.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop aggregator service: %w", err)
	}

	// Small delay before restarting
	select {
	case <-time.After(1 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}

	return s.Start(ctx)
}

// GetServiceData implements ServiceDataProvider
func (s *AggregatorService) GetServiceData() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data := map[string]interface{}{
		"service_type": "aggregator",
	}

	// Delegate to manager
	if s.manager != nil {
		managerData := s.manager.GetServiceData()
		for k, v := range managerData {
			data[k] = v
		}
	}

	return data
}

// GetEndpoint returns the aggregator's SSE endpoint URL
func (s *AggregatorService) GetEndpoint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.manager != nil {
		return s.manager.GetEndpoint()
	}

	return ""
}

// GetManager returns the underlying aggregator manager for advanced operations
func (s *AggregatorService) GetManager() *aggregator.AggregatorManager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.manager
}

// ManualRefresh manually triggers a refresh of healthy MCP server registrations
// This can be useful for debugging or forced updates
func (s *AggregatorService) ManualRefresh(ctx context.Context) error {
	s.mu.RLock()
	manager := s.manager
	s.mu.RUnlock()

	if manager != nil {
		return manager.ManualRefresh(ctx)
	}

	return fmt.Errorf("aggregator manager not initialized")
}
