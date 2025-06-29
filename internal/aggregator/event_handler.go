package aggregator

import (
	"context"
	"muster/internal/api"
	"muster/pkg/logging"
	"sync"
)

// EventHandler manages automatic MCP server registration based on service lifecycle events.
//
// The event handler bridges the gap between the muster service orchestrator and the
// aggregator by listening for service state changes and automatically registering or
// deregistering MCP servers as they become healthy or unhealthy.
//
// Key responsibilities:
//   - Subscribe to orchestrator service state changes
//   - Filter events to only process MCP service changes
//   - Automatically register healthy running MCP servers
//   - Automatically deregister unhealthy or stopped MCP servers
//   - Maintain separation of concerns through callback functions
//
// The handler operates asynchronously and is designed to be resilient to temporary
// failures in the registration process.
type EventHandler struct {
	orchestratorAPI api.OrchestratorAPI                 // Interface for receiving service events
	registerFunc    func(context.Context, string) error // Callback for server registration
	deregisterFunc  func(string) error                  // Callback for server deregistration

	// Lifecycle management
	ctx        context.Context    // Context for coordinating shutdown
	cancelFunc context.CancelFunc // Function to cancel the context
	wg         sync.WaitGroup     // WaitGroup for coordinating goroutine shutdown
	mu         sync.RWMutex       // Protects the running state
	running    bool               // Indicates whether the event handler is active
}

// NewEventHandler creates a new event handler with the specified dependencies and callbacks.
//
// The event handler uses callback functions to maintain loose coupling with the
// aggregator manager. This design allows the handler to focus solely on event
// processing while delegating the actual registration logic to the caller.
//
// Parameters:
//   - orchestratorAPI: Interface for subscribing to service state changes
//   - registerFunc: Callback function to register a server by name
//   - deregisterFunc: Callback function to deregister a server by name
//
// Returns a configured but not yet started event handler.
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

// Start begins listening for orchestrator events and processing them asynchronously.
//
// This method subscribes to service state change events from the orchestrator and
// starts a background goroutine to process them. The method is idempotent - calling
// it multiple times has no additional effect.
//
// The event processing continues until the provided context is cancelled or the
// Stop method is called.
//
// Parameters:
//   - ctx: Context for controlling the event handler lifecycle
//
// Returns nil on successful startup. The method does not wait for event processing
// to complete, as that happens asynchronously.
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

// Stop gracefully shuts down the event handler and waits for cleanup to complete.
//
// This method cancels the event processing context and waits for the background
// goroutine to finish processing any in-flight events. The method is idempotent
// and can be called multiple times safely.
//
// Returns nil after successful shutdown.
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

// IsRunning returns whether the event handler is currently active.
//
// This method is thread-safe and can be used to check the handler's status
// for monitoring or debugging purposes.
//
// Returns true if the event handler is currently processing events.
func (eh *EventHandler) IsRunning() bool {
	eh.mu.RLock()
	defer eh.mu.RUnlock()
	return eh.running
}

// handleEvents processes orchestrator events in a background goroutine.
//
// This method runs continuously until the context is cancelled or the event
// channel is closed. It filters incoming events to only process MCP service
// changes and delegates the actual registration/deregistration logic to the
// configured callback functions.
//
// The method automatically updates the running state when it exits to ensure
// accurate status reporting.
//
// Parameters:
//   - eventChan: Read-only channel for receiving service state change events
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

// processEvent handles a single orchestrator event and determines the appropriate action.
//
// This method implements the core business logic for automatic server registration:
//   - Filters out non-MCP service events
//   - Registers servers that become healthy and running
//   - Deregisters servers that become unhealthy, stopped, or failed
//
// The registration/deregistration operations are performed through callback functions
// to maintain separation of concerns.
//
// Parameters:
//   - event: Service state change event from the orchestrator
func (eh *EventHandler) processEvent(event api.ServiceStateChangedEvent) {
	// Filter for MCP service events only
	if !eh.isMCPServiceEvent(event) {
		return
	}

	logging.Debug("Aggregator-EventHandler", "Processing MCP service event: %s (state=%s, health=%s)",
		event.Name, event.NewState, event.Health)

	// Only register servers that are BOTH Running AND Healthy
	// This ensures that the MCP client is ready and the server is functioning properly
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

// isMCPServiceEvent checks if the given event is related to an MCP service.
//
// This method filters events to ensure that only MCP server state changes are
// processed by the event handler. Other service types are ignored to avoid
// unnecessary processing overhead.
//
// Parameters:
//   - event: Service state change event to examine
//
// Returns true if the event is related to an MCP service, false otherwise.
func (eh *EventHandler) isMCPServiceEvent(event api.ServiceStateChangedEvent) bool {
	return event.ServiceType == "MCPServer"
}
