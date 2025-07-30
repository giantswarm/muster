package aggregator

import (
	"context"
	"muster/internal/api"
	"muster/internal/events"
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
// Args:
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
// Args:
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
// Args:
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
//   - Generates Kubernetes events for MCPServer lifecycle operations
//
// The registration/deregistration operations are performed through callback functions
// to maintain separation of concerns.
//
// Args:
//   - event: Service state change event from the orchestrator
func (eh *EventHandler) processEvent(event api.ServiceStateChangedEvent) {
	// Filter for MCP service events only
	if !eh.isMCPServiceEvent(event) {
		return
	}

	logging.Debug("Aggregator-EventHandler", "Processing MCP service event: %s (state=%s, health=%s)",
		event.Name, event.NewState, event.Health)

	// Generate events for service state transitions
	eh.generateServiceStateEvents(event)

	// Only register servers that are BOTH Running AND Healthy
	// This ensures that the MCP client is ready and the server is functioning properly
	isHealthyAndRunning := event.NewState == "running" && event.Health == "healthy"

	if isHealthyAndRunning {
		// Register the healthy running server
		logging.Info("Aggregator-EventHandler", "Registering healthy MCP server: %s", event.Name)

		if err := eh.registerFunc(context.Background(), event.Name); err != nil {
			logging.Error("Aggregator-EventHandler", err, "Failed to register MCP server %s", event.Name)
			// Generate event for failed tool registration
			eh.generateEvent(event.Name, events.ReasonMCPServerToolsUnavailable, events.EventData{
				Error: err.Error(),
			})
		} else {
			// Generate event for successful tool discovery and registration
			eh.generateEvent(event.Name, events.ReasonMCPServerToolsDiscovered, events.EventData{})
		}
	} else {
		// Deregister for any other state/health combination
		// This includes: Stopped, Failed, Starting, Stopping, or Running+Unhealthy
		logging.Info("Aggregator-EventHandler", "Deregistering MCP server %s (state=%s, health=%s)",
			event.Name, event.NewState, event.Health)

		if err := eh.deregisterFunc(event.Name); err != nil {
			// Log as debug since deregistering a non-existent server is not critical
			logging.Debug("Aggregator-EventHandler", "Failed to deregister MCP server %s: %v", event.Name, err)
		} else {
			// Generate event for tools becoming unavailable
			eh.generateEvent(event.Name, events.ReasonMCPServerToolsUnavailable, events.EventData{})
		}
	}
}

// generateServiceStateEvents generates appropriate Kubernetes events based on service state transitions
func (eh *EventHandler) generateServiceStateEvents(event api.ServiceStateChangedEvent) {
	// Map service states to appropriate event reasons
	switch event.NewState {
	case "starting":
		eh.generateEvent(event.Name, events.ReasonMCPServerStarting, events.EventData{})
	case "running":
		if event.Health == "healthy" {
			eh.generateEvent(event.Name, events.ReasonMCPServerStarted, events.EventData{})
		}
	case "stopped":
		eh.generateEvent(event.Name, events.ReasonMCPServerStopped, events.EventData{})
	case "failed":
		eventData := events.EventData{}
		if event.Error != nil {
			eventData.Error = event.Error.Error()
		}
		eh.generateEvent(event.Name, events.ReasonMCPServerFailed, eventData)
	}

	// Generate health-related events
	if event.NewState == "running" && event.Health == "unhealthy" {
		eventData := events.EventData{}
		if event.Error != nil {
			eventData.Error = event.Error.Error()
		}
		eh.generateEvent(event.Name, events.ReasonMCPServerHealthCheckFailed, eventData)
	}

	// Check for restart patterns (stopped -> starting or failed -> starting)
	if event.NewState == "starting" && (event.OldState == "stopped" || event.OldState == "failed") {
		eh.generateEvent(event.Name, events.ReasonMCPServerRestarting, events.EventData{})
	}
}

// generateEvent creates a Kubernetes event for an MCPServer service instance
func (eh *EventHandler) generateEvent(serviceName string, reason events.EventReason, data events.EventData) {
	eventManager := api.GetEventManager()
	if eventManager == nil {
		logging.Debug("Aggregator-EventHandler", "Event manager not available, skipping event generation for %s", serviceName)
		return
	}

	// Create an object reference for the MCPServer CRD
	// MCPServer lifecycle events should be associated with the MCPServer CRD resource
	objectRef := api.ObjectReference{
		Kind:      "MCPServer",
		Name:      serviceName,
		Namespace: "default", // TODO: Make configurable or derive from service
	}

	// Populate service-specific data
	data.Name = serviceName
	if data.Namespace == "" {
		data.Namespace = "default"
	}

	err := eventManager.CreateEvent(context.Background(), objectRef, string(reason), "", string(events.EventTypeNormal))
	if err != nil {
		logging.Debug("Aggregator-EventHandler", "Failed to generate event for %s: %v", serviceName, err)
	} else {
		logging.Debug("Aggregator-EventHandler", "Generated event %s for MCPServer %s", string(reason), serviceName)
	}
}

// isMCPServiceEvent checks if the given event is related to an MCP service.
//
// This method filters events to ensure that only MCP server state changes are
// processed by the event handler. Other service types are ignored to avoid
// unnecessary processing overhead.
//
// Args:
//   - event: Service state change event to examine
//
// Returns true if the event is related to an MCP service, false otherwise.
func (eh *EventHandler) isMCPServiceEvent(event api.ServiceStateChangedEvent) bool {
	return event.ServiceType == "MCPServer"
}
