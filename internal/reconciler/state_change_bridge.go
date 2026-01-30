package reconciler

import (
	"context"
	"sync"
	"time"

	"muster/internal/api"
	"muster/pkg/logging"
)

// StateChangeBridge bridges service state changes from the orchestrator to the
// reconciliation system. This enables the idiomatic Kubernetes controller pattern
// where status is updated in response to runtime state changes, not just spec changes.
//
// The bridge subscribes to orchestrator state change events and triggers reconciliation
// for the affected resources. This ensures that CRD status subresources reflect the
// actual runtime state of services (running, stopped, healthy, unhealthy, etc.).
//
// This implements the "external event source" pattern commonly used in controller-runtime
// based controllers via source.Channel.
type StateChangeBridge struct {
	mu sync.RWMutex

	// orchestratorAPI provides access to state change events
	orchestratorAPI api.OrchestratorAPI

	// reconcileManager is the target for triggering reconciliation
	reconcileManager *Manager

	// namespace is the default namespace for resources
	namespace string

	// ctx is the bridge's context
	ctx context.Context

	// cancelFunc cancels the bridge's context
	cancelFunc context.CancelFunc

	// wg tracks running goroutines
	wg sync.WaitGroup

	// running indicates if the bridge is active
	running bool
}

// NewStateChangeBridge creates a new state change bridge.
//
// Args:
//   - orchestratorAPI: Interface for subscribing to service state changes
//   - reconcileManager: The reconcile manager to trigger reconciliation on
//   - namespace: Default namespace for resources (used when namespace is empty)
//
// Returns a configured but not yet started bridge.
func NewStateChangeBridge(
	orchestratorAPI api.OrchestratorAPI,
	reconcileManager *Manager,
	namespace string,
) *StateChangeBridge {
	if namespace == "" {
		namespace = DefaultNamespace
	}

	return &StateChangeBridge{
		orchestratorAPI:  orchestratorAPI,
		reconcileManager: reconcileManager,
		namespace:        namespace,
	}
}

// Start begins listening for orchestrator state changes and triggering reconciliation.
//
// This method subscribes to service state change events from the orchestrator and
// starts a background goroutine to process them. The method is idempotent - calling
// it multiple times has no additional effect.
//
// Args:
//   - ctx: Context for controlling the bridge lifecycle
//
// Returns nil on successful startup.
func (b *StateChangeBridge) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		return nil
	}

	b.ctx, b.cancelFunc = context.WithCancel(ctx)
	b.running = true

	// Subscribe to state changes from the orchestrator
	eventChan := b.orchestratorAPI.SubscribeToStateChanges()

	b.wg.Add(1)
	go b.processEvents(eventChan)

	logging.Info("StateChangeBridge", "Started bridge for service state change -> reconciliation")
	return nil
}

// Stop gracefully shuts down the bridge and waits for cleanup to complete.
//
// This method cancels the event processing context and waits for the background
// goroutine to finish. The method is idempotent and can be called multiple times.
//
// Returns nil after successful shutdown.
func (b *StateChangeBridge) Stop() error {
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return nil
	}

	b.running = false
	cancelFunc := b.cancelFunc
	b.mu.Unlock()

	if cancelFunc != nil {
		cancelFunc()
	}

	// Wait for the event processing goroutine to finish
	b.wg.Wait()

	logging.Info("StateChangeBridge", "Stopped state change bridge")
	return nil
}

// processEvents processes orchestrator events in a background goroutine.
func (b *StateChangeBridge) processEvents(eventChan <-chan api.ServiceStateChangedEvent) {
	defer b.wg.Done()
	defer func() {
		b.mu.Lock()
		b.running = false
		b.mu.Unlock()
	}()

	for {
		select {
		case <-b.ctx.Done():
			logging.Debug("StateChangeBridge", "Context cancelled, stopping")
			return

		case event, ok := <-eventChan:
			if !ok {
				logging.Warn("StateChangeBridge", "Event channel closed, stopping")
				return
			}

			b.handleEvent(event)
		}
	}
}

// handleEvent processes a single state change event and triggers reconciliation.
func (b *StateChangeBridge) handleEvent(event api.ServiceStateChangedEvent) {
	// Determine the resource type from the service type
	resourceType := b.mapServiceTypeToResourceType(event.ServiceType)
	if resourceType == "" {
		logging.Debug("StateChangeBridge", "Ignoring event for unknown service type: %s", event.ServiceType)
		return
	}

	// Check if reconciliation is enabled for this resource type
	if !b.reconcileManager.IsResourceTypeEnabled(resourceType) {
		logging.Debug("StateChangeBridge", "Skipping state change for disabled resource type: %s/%s",
			resourceType, event.Name)
		return
	}

	logging.Debug("StateChangeBridge", "Triggering reconciliation for %s/%s due to state change: %s -> %s (health: %s)",
		resourceType, event.Name, event.OldState, event.NewState, event.Health)

	// Trigger reconciliation via the manager
	// This will queue the resource for reconciliation, which will:
	// 1. Fetch the current desired state from the CRD/YAML
	// 2. Compare with actual runtime state
	// 3. Update the status subresource with current state
	b.triggerReconcile(resourceType, event.Name)
}

// triggerReconcile triggers reconciliation for a resource.
func (b *StateChangeBridge) triggerReconcile(resourceType ResourceType, name string) {
	// Create a change event for the state change
	event := ChangeEvent{
		Type:      resourceType,
		Name:      name,
		Namespace: b.namespace,
		Operation: OperationUpdate,
		Timestamp: time.Now(),
		Source:    SourceServiceState,
	}

	// Use the manager's internal method to handle the event
	// This ensures proper status tracking and queueing
	b.reconcileManager.handleChangeEvent(event)
}

// mapServiceTypeToResourceType maps orchestrator service types to reconciler resource types.
func (b *StateChangeBridge) mapServiceTypeToResourceType(serviceType string) ResourceType {
	switch serviceType {
	case "MCPServer":
		return ResourceTypeMCPServer
	default:
		// For now, only MCPServer services trigger reconciliation
		// ServiceClass and Workflow are static definitions that don't have runtime state
		return ""
	}
}
