package aggregator

import (
	"context"
	"muster/internal/api"
	"muster/pkg/logging"
	"sync"
)

// EventHandler handles orchestrator events and updates the aggregator accordingly
type EventHandler struct {
	orchestratorAPI api.OrchestratorAPI
	registerFunc    func(context.Context, string) error
	deregisterFunc  func(string) error
	ctx             context.Context
	cancelFunc      context.CancelFunc
	wg              sync.WaitGroup
	mu              sync.RWMutex
	running         bool
}

// NewEventHandler creates a new event handler with simplified callbacks
func NewEventHandler(
	orchestratorAPI api.OrchestratorAPI,
	registerFunc func(context.Context, string) error,
	deregisterFunc func(string) error,
) *EventHandler {
	return &EventHandler{
		orchestratorAPI: orchestratorAPI,
		registerFunc:    registerFunc,
		deregisterFunc:  deregisterFunc,
	}
}

// Start begins listening for orchestrator events
func (eh *EventHandler) Start(ctx context.Context) error {
	eh.mu.Lock()
	defer eh.mu.Unlock()

	if eh.running {
		return nil
	}

	eh.ctx, eh.cancelFunc = context.WithCancel(ctx)
	eh.running = true

	// Subscribe to state changes from the orchestrator
	eventChan := eh.orchestratorAPI.SubscribeToStateChanges()

	eh.wg.Add(1)
	go eh.handleEvents(eventChan)

	logging.Info("Aggregator-EventHandler", "Started event handler for MCP service state changes")
	return nil
}

// Stop stops the event handler
func (eh *EventHandler) Stop() error {
	eh.mu.Lock()
	if !eh.running {
		eh.mu.Unlock()
		return nil
	}

	eh.running = false
	cancelFunc := eh.cancelFunc
	eh.mu.Unlock()

	if cancelFunc != nil {
		cancelFunc()
	}

	// Wait for the event handling goroutine to finish
	eh.wg.Wait()

	logging.Info("Aggregator-EventHandler", "Stopped event handler")
	return nil
}

// IsRunning returns whether the event handler is currently running
func (eh *EventHandler) IsRunning() bool {
	eh.mu.RLock()
	defer eh.mu.RUnlock()
	return eh.running
}

// handleEvents processes orchestrator events in a background goroutine
func (eh *EventHandler) handleEvents(eventChan <-chan api.ServiceStateChangedEvent) {
	defer eh.wg.Done()
	defer func() {
		// Mark as not running when goroutine exits
		eh.mu.Lock()
		eh.running = false
		eh.mu.Unlock()
	}()

	for {
		select {
		case <-eh.ctx.Done():
			logging.Debug("Aggregator-EventHandler", "Event handler context cancelled, stopping")
			return

		case event, ok := <-eventChan:
			if !ok {
				logging.Warn("Aggregator-EventHandler", "Event channel closed, stopping event handler")
				return
			}

			eh.processEvent(event)
		}
	}
}

// processEvent handles a single orchestrator event
func (eh *EventHandler) processEvent(event api.ServiceStateChangedEvent) {
	// Filter for MCP service events only
	if !eh.isMCPServiceEvent(event) {
		return
	}

	logging.Debug("Aggregator-EventHandler", "Processing MCP service event: %s (state=%s, health=%s)",
		event.Name, event.NewState, event.Health)

	// Only register servers that are BOTH Running AND Healthy
	isHealthyAndRunning := event.NewState == "running" && event.Health == "healthy"

	if isHealthyAndRunning {
		// Register the healthy running server
		logging.Info("Aggregator-EventHandler", "Registering healthy MCP server: %s", event.Name)

		if err := eh.registerFunc(context.Background(), event.Name); err != nil {
			logging.Error("Aggregator-EventHandler", err, "Failed to register MCP server %s", event.Name)
		}
	} else {
		// Deregister for any other state/health combination
		// This includes: Stopped, Failed, Starting, Stopping, or Running+Unhealthy
		logging.Info("Aggregator-EventHandler", "Deregistering MCP server %s (state=%s, health=%s)",
			event.Name, event.NewState, event.Health)

		if err := eh.deregisterFunc(event.Name); err != nil {
			// Log as debug since deregistering a non-existent server is not critical
			logging.Debug("Aggregator-EventHandler", "Failed to deregister MCP server %s: %v", event.Name, err)
		}
	}
}

// isMCPServiceEvent checks if the event is related to an MCP service
func (eh *EventHandler) isMCPServiceEvent(event api.ServiceStateChangedEvent) bool {
	return event.ServiceType == "MCPServer"
}
