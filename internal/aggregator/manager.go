package aggregator

import (
	"context"
	"fmt"
	"muster/internal/api"
	"muster/pkg/logging"
	"sync"
	"time"
)

// This file contains aggregator manager logic that coordinates between
// the aggregator server and event handling to provide automatic MCP
// server registration based on health status.

// AggregatorManager combines the aggregator server with event handling
// to provide automatic MCP server registration updates when services change state
type AggregatorManager struct {
	mu     sync.RWMutex
	config AggregatorConfig

	// External dependencies - now using APIs directly
	orchestratorAPI api.OrchestratorAPI
	serviceRegistry api.ServiceRegistryHandler

	// Components
	aggregatorServer *AggregatorServer
	eventHandler     *EventHandler

	// Lifecycle
	ctx        context.Context
	cancelFunc context.CancelFunc
}

// NewAggregatorManager creates a new aggregator manager with event handling
func NewAggregatorManager(
	config AggregatorConfig,
	orchestratorAPI api.OrchestratorAPI,
	serviceRegistry api.ServiceRegistryHandler,
) *AggregatorManager {
	manager := &AggregatorManager{
		config:          config,
		orchestratorAPI: orchestratorAPI,
		serviceRegistry: serviceRegistry,
	}

	// Create the aggregator server
	manager.aggregatorServer = NewAggregatorServer(config)

	return manager
}

// Start starts the aggregator manager
func (am *AggregatorManager) Start(ctx context.Context) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Create cancellable context
	am.ctx, am.cancelFunc = context.WithCancel(ctx)

	// Start the aggregator server first
	if err := am.aggregatorServer.Start(am.ctx); err != nil {
		return fmt.Errorf("failed to start aggregator server: %w", err)
	}

	// Check if APIs are available
	if am.orchestratorAPI == nil {
		am.aggregatorServer.Stop(am.ctx)
		return fmt.Errorf("required APIs not available")
	}

	// Initial sync: Register all healthy running MCP servers
	if err := am.registerHealthyMCPServers(am.ctx); err != nil {
		logging.Warn("Aggregator-Manager", "Error during initial MCP server registration: %v", err)
		// Continue anyway - the event handler will handle future registrations
	}

	// Create event handler with simple register/deregister callbacks
	am.eventHandler = NewEventHandler(
		am.orchestratorAPI,
		am.registerSingleServer,
		am.deregisterSingleServer,
	)

	// Start the event handler for automatic updates
	if err := am.eventHandler.Start(am.ctx); err != nil {
		// Stop the aggregator server if event handler fails
		am.aggregatorServer.Stop(am.ctx)
		return fmt.Errorf("failed to start event handler: %w", err)
	}

	// Start periodic retry mechanism for failed registrations
	go am.retryFailedRegistrations(am.ctx)

	logging.Info("Aggregator-Manager", "Started aggregator manager on %s", am.aggregatorServer.GetEndpoint())
	return nil
}

// Stop stops the aggregator manager
func (am *AggregatorManager) Stop(ctx context.Context) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Cancel context to signal shutdown
	if am.cancelFunc != nil {
		am.cancelFunc()
	}

	// Stop event handler first
	if am.eventHandler != nil {
		if err := am.eventHandler.Stop(); err != nil {
			logging.Error("Aggregator-Manager", err, "Error stopping event handler")
		}
	}

	// Stop aggregator server
	if am.aggregatorServer != nil {
		if err := am.aggregatorServer.Stop(ctx); err != nil {
			logging.Error("Aggregator-Manager", err, "Error stopping aggregator server")
		}
	}

	logging.Info("Aggregator-Manager", "Stopped aggregator manager")
	return nil
}

// GetServiceData returns service data for monitoring
func (am *AggregatorManager) GetServiceData() map[string]interface{} {
	am.mu.RLock()
	defer am.mu.RUnlock()

	data := map[string]interface{}{
		"port": am.config.Port,
		"host": am.config.Host,
		"yolo": am.config.Yolo,
	}

	// Add aggregator server data
	if am.aggregatorServer != nil {
		data["endpoint"] = am.aggregatorServer.GetEndpoint()

		// Get tool/resource/prompt counts
		tools := am.aggregatorServer.GetTools()
		resources := am.aggregatorServer.GetResources()
		prompts := am.aggregatorServer.GetPrompts()

		data["tools"] = len(tools)
		data["resources"] = len(resources)
		data["prompts"] = len(prompts)

		// Get tools with status
		toolsWithStatus := am.aggregatorServer.GetToolsWithStatus()
		data["tools_with_status"] = toolsWithStatus

		// Count blocked tools
		blockedCount := 0
		for _, t := range toolsWithStatus {
			if t.Blocked {
				blockedCount++
			}
		}
		data["blocked_tools"] = blockedCount

		// Get total number of MCP servers from the API
		totalServers := 0
		connectedServers := 0

		if am.serviceRegistry != nil {
			// Get all MCP services
			allServices := am.serviceRegistry.GetByType(api.TypeMCPServer)
			totalServers = len(allServices)

			// Count healthy running services (these have ready clients by definition)
			for _, service := range allServices {
				if service.GetState() == api.StateRunning && service.GetHealth() == api.HealthHealthy {
					connectedServers++
				}
			}
		}

		data["servers_total"] = totalServers
		data["servers_connected"] = connectedServers
	}

	// Add event handler status
	if am.eventHandler != nil {
		data["event_handler_running"] = am.eventHandler.IsRunning()
	}

	return data
}

// registerHealthyMCPServers registers all healthy running MCP servers during initial sync
// In the new architecture, running+healthy guarantees the MCP client is ready
func (am *AggregatorManager) registerHealthyMCPServers(ctx context.Context) error {
	if am.serviceRegistry == nil {
		return fmt.Errorf("service registry not available")
	}

	// Get all MCP services
	mcpServices := am.serviceRegistry.GetByType(api.TypeMCPServer)

	registeredCount := 0
	for _, service := range mcpServices {
		// Only register servers that are running AND healthy (client is guaranteed ready)
		if string(service.GetState()) != "running" || string(service.GetHealth()) != "healthy" {
			continue
		}

		// Register the healthy server (client is guaranteed ready at this point)
		if err := am.registerSingleServer(ctx, service.GetName()); err != nil {
			logging.Warn("Aggregator-Manager", "Failed to register healthy MCP server %s: %v",
				service.GetName(), err)
			// Continue with other servers
		} else {
			registeredCount++
		}
	}

	if registeredCount > 0 {
		logging.Info("Aggregator-Manager", "Initial sync completed: registered %d healthy MCP servers", registeredCount)
	}

	return nil
}

// registerSingleServer registers a single MCP server with the aggregator
// Since this is called only when service is running+healthy, we trust that the client is ready
func (am *AggregatorManager) registerSingleServer(ctx context.Context, serverName string) error {
	// Get the service from registry
	service, exists := am.serviceRegistry.Get(serverName)
	if !exists {
		return fmt.Errorf("service %s not found", serverName)
	}

	// Get service data - much simpler now since running+healthy guarantees client readiness
	serviceData := service.GetServiceData()
	if serviceData == nil {
		return fmt.Errorf("no service data available for %s", serverName)
	}

	// Extract tool prefix
	toolPrefix, _ := serviceData["toolPrefix"].(string)

	// Get MCP client from service data - this is now the authoritative source
	clientInterface, exists := serviceData["client"]
	if !exists || clientInterface == nil {
		return fmt.Errorf("no MCP client available for %s (service state inconsistent)", serverName)
	}

	mcpClient, ok := clientInterface.(MCPClient)
	if !ok {
		return fmt.Errorf("invalid MCP client type for %s", serverName)
	}

	// Register with the aggregator
	if err := am.aggregatorServer.RegisterServer(ctx, serverName, mcpClient, toolPrefix); err != nil {
		return fmt.Errorf("failed to register server: %w", err)
	}

	logging.Info("Aggregator-Manager", "Successfully registered MCP server %s with prefix %s", serverName, toolPrefix)
	return nil
}

// deregisterSingleServer deregisters a single MCP server from the aggregator
func (am *AggregatorManager) deregisterSingleServer(serverName string) error {
	// Deregister from the aggregator
	if err := am.aggregatorServer.DeregisterServer(serverName); err != nil {
		return fmt.Errorf("failed to deregister server: %w", err)
	}

	logging.Info("Aggregator-Manager", "Successfully deregistered MCP server %s", serverName)
	return nil
}

// GetEndpoint returns the aggregator's SSE endpoint URL
func (am *AggregatorManager) GetEndpoint() string {
	am.mu.RLock()
	defer am.mu.RUnlock()

	if am.aggregatorServer != nil {
		return am.aggregatorServer.GetEndpoint()
	}

	return ""
}

// GetAggregatorServer returns the underlying aggregator server for advanced operations
func (am *AggregatorManager) GetAggregatorServer() *AggregatorServer {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.aggregatorServer
}

// GetEventHandler returns the event handler (mainly for testing)
func (am *AggregatorManager) GetEventHandler() *EventHandler {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.eventHandler
}

// ManualRefresh manually triggers a re-sync of all healthy MCP servers
// This can be useful for debugging or forced updates
func (am *AggregatorManager) ManualRefresh(ctx context.Context) error {
	return am.registerHealthyMCPServers(ctx)
}

// retryFailedRegistrations periodically retries registration for services that have ready clients
func (am *AggregatorManager) retryFailedRegistrations(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			am.attemptPendingRegistrations(ctx)
		}
	}
}

// attemptPendingRegistrations tries to register services that are healthy but not yet registered
func (am *AggregatorManager) attemptPendingRegistrations(ctx context.Context) {
	if am.serviceRegistry == nil {
		return
	}

	// Get all MCP services
	mcpServices := am.serviceRegistry.GetByType(api.TypeMCPServer)

	for _, service := range mcpServices {
		// Only try services that are running and healthy (client is guaranteed ready)
		if string(service.GetState()) != "running" || string(service.GetHealth()) != "healthy" {
			continue
		}

		// Check if already registered with aggregator
		if am.aggregatorServer != nil {
			if _, exists := am.aggregatorServer.GetRegistry().GetServerInfo(service.GetName()); exists {
				continue // Already registered
			}
		}

		// Try to register - much simpler now since running+healthy guarantees client readiness
		if err := am.registerSingleServer(ctx, service.GetName()); err != nil {
			logging.Debug("Aggregator-Manager", "Retry registration failed for %s: %v", service.GetName(), err)
		} else {
			logging.Info("Aggregator-Manager", "Successfully registered %s on retry", service.GetName())
		}
	}
}
