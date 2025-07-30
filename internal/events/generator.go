package events

import (
	"context"

	"muster/internal/client"
	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
	"muster/pkg/logging"
)

// EventGenerator provides event generation utilities using the unified MusterClient.
// It automatically adapts to the current client mode (Kubernetes vs filesystem)
// through the MusterClient interface.
type EventGenerator struct {
	client    client.MusterClient
	templates *MessageTemplateEngine
}

// NewEventGenerator creates a new EventGenerator using the provided MusterClient.
func NewEventGenerator(musterClient client.MusterClient) *EventGenerator {
	return &EventGenerator{
		client:    musterClient,
		templates: NewMessageTemplateEngine(),
	}
}

// MCPServerEvent generates an event for an MCPServer CRD.
func (g *EventGenerator) MCPServerEvent(server *musterv1alpha1.MCPServer, reason EventReason, data EventData) error {
	// Populate event data with server information
	data.Name = server.Name
	data.Namespace = server.Namespace

	message := g.templates.Render(reason, data)
	eventType := string(getEventType(reason))

	logging.Debug("events", "Generating MCPServer event: reason=%s, message=%s, type=%s",
		string(reason), message, eventType)

	return g.client.CreateEvent(context.Background(), server, string(reason), message, eventType)
}

// ServiceClassEvent generates an event for a ServiceClass CRD.
func (g *EventGenerator) ServiceClassEvent(serviceClass *musterv1alpha1.ServiceClass, reason EventReason, data EventData) error {
	// Populate event data with ServiceClass information
	data.Name = serviceClass.Name
	data.Namespace = serviceClass.Namespace

	message := g.templates.Render(reason, data)
	eventType := string(getEventType(reason))

	logging.Debug("events", "Generating ServiceClass event: reason=%s, message=%s, type=%s",
		string(reason), message, eventType)

	return g.client.CreateEvent(context.Background(), serviceClass, string(reason), message, eventType)
}

// WorkflowEvent generates an event for a Workflow CRD.
func (g *EventGenerator) WorkflowEvent(workflow *musterv1alpha1.Workflow, reason EventReason, data EventData) error {
	// Populate event data with Workflow information
	data.Name = workflow.Name
	data.Namespace = workflow.Namespace

	message := g.templates.Render(reason, data)
	eventType := string(getEventType(reason))

	logging.Debug("events", "Generating Workflow event: reason=%s, message=%s, type=%s",
		string(reason), message, eventType)

	return g.client.CreateEvent(context.Background(), workflow, string(reason), message, eventType)
}

// CRDEvent generates an event for a CRD by type, name, and namespace.
// This is useful when you don't have the actual CRD object but know its details.
func (g *EventGenerator) CRDEvent(crdType, name, namespace string, reason EventReason, data EventData) error {
	// Populate event data with CRD information
	data.Name = name
	data.Namespace = namespace

	message := g.templates.Render(reason, data)
	eventType := string(getEventType(reason))

	logging.Debug("events", "Generating CRD event: type=%s, reason=%s, message=%s, eventType=%s",
		crdType, string(reason), message, eventType)

	return g.client.CreateEventForCRD(context.Background(), crdType, name, namespace, string(reason), message, eventType)
}

// ServiceInstanceEvent generates an event for a service instance.
// Since service instances are not CRDs, this creates events using the CreateEventForCRD method
// with a synthetic CRD type.
func (g *EventGenerator) ServiceInstanceEvent(serviceName, serviceClass, namespace string, reason EventReason, data EventData) error {
	// Populate event data with service instance information
	data.Name = serviceName
	data.Namespace = namespace
	data.ServiceClass = serviceClass

	message := g.templates.Render(reason, data)
	eventType := string(getEventType(reason))

	logging.Debug("events", "Generating service instance event: service=%s, reason=%s, message=%s, type=%s",
		serviceName, string(reason), message, eventType)

	// For service instances, we use a synthetic CRD type since they're not actual CRDs
	return g.client.CreateEventForCRD(context.Background(), "ServiceInstance", serviceName, namespace, string(reason), message, eventType)
}

// SetTemplate allows customizing the message template for a specific event reason.
func (g *EventGenerator) SetTemplate(reason EventReason, template string) {
	g.templates.SetTemplate(reason, template)
}

// GetTemplate returns the template for a specific event reason.
func (g *EventGenerator) GetTemplate(reason EventReason) (string, bool) {
	return g.templates.GetTemplate(reason)
}

// IsKubernetesMode returns true if the generator is using Kubernetes mode.
func (g *EventGenerator) IsKubernetesMode() bool {
	return g.client.IsKubernetesMode()
}
