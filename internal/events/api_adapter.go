package events

import (
	"context"

	"muster/internal/api"
	"muster/internal/client"
	"muster/pkg/logging"
)

// Adapter implements the EventManagerHandler interface using the unified MusterClient.
// It bridges the events package functionality with the API service locator pattern.
type Adapter struct {
	generator *EventGenerator
}

// NewAdapter creates a new events adapter using the provided MusterClient.
func NewAdapter(musterClient client.MusterClient) *Adapter {
	return &Adapter{
		generator: NewEventGenerator(musterClient),
	}
}

// Register registers this adapter with the API service locator.
// This method follows the standard pattern used by all service adapters.
func (a *Adapter) Register() {
	api.RegisterEventManager(a)
	logging.Debug("events", "Event manager adapter registered with API")
}

// CreateEvent creates an event for a specific object reference.
// Implements EventManagerHandler.CreateEvent.
func (a *Adapter) CreateEvent(ctx context.Context, objectRef api.ObjectReference, reason, message, eventType string) error {
	logging.Debug("events", "Creating event for %s %s/%s: %s - %s (%s)",
		objectRef.Kind, objectRef.Namespace, objectRef.Name, reason, message, eventType)

	// Use the generator's CRDEvent method which works with object references
	data := EventData{
		Name:      objectRef.Name,
		Namespace: objectRef.Namespace,
	}

	// Map the object reference to a CRD type if possible
	crdType := objectRef.Kind
	switch objectRef.Kind {
	case "MCPServer":
		return a.generator.CRDEvent("MCPServer", objectRef.Name, objectRef.Namespace, EventReason(reason), data)
	case "ServiceClass":
		return a.generator.CRDEvent("ServiceClass", objectRef.Name, objectRef.Namespace, EventReason(reason), data)
	case "Workflow":
		return a.generator.CRDEvent("Workflow", objectRef.Name, objectRef.Namespace, EventReason(reason), data)
	case "ServiceInstance":
		return a.generator.CRDEvent("ServiceInstance", objectRef.Name, objectRef.Namespace, EventReason(reason), data)
	default:
		// For unknown types, use the general CRDEvent method
		return a.generator.CRDEvent(crdType, objectRef.Name, objectRef.Namespace, EventReason(reason), data)
	}
}

// CreateEventForCRD creates an event for a CRD by type, name, and namespace.
// Implements EventManagerHandler.CreateEventForCRD.
func (a *Adapter) CreateEventForCRD(ctx context.Context, crdType, name, namespace, reason, message, eventType string) error {
	logging.Debug("events", "Creating CRD event for %s %s/%s: %s - %s (%s)",
		crdType, namespace, name, reason, message, eventType)

	data := EventData{
		Name:      name,
		Namespace: namespace,
	}

	return a.generator.CRDEvent(crdType, name, namespace, EventReason(reason), data)
}

// IsKubernetesMode returns true if the event manager is using Kubernetes mode.
// Implements EventManagerHandler.IsKubernetesMode.
func (a *Adapter) IsKubernetesMode() bool {
	return a.generator.IsKubernetesMode()
}

// GetGenerator returns the underlying EventGenerator for advanced usage scenarios.
// This method is not part of the EventManagerHandler interface but provides
// access to advanced event generation features when needed.
func (a *Adapter) GetGenerator() *EventGenerator {
	return a.generator
}
