package events

import (
	"time"
)

// EventType represents the type/severity of a Kubernetes Event.
type EventType string

const (
	// EventTypeNormal indicates normal, non-problematic events.
	EventTypeNormal EventType = "Normal"

	// EventTypeWarning indicates events that may require attention.
	EventTypeWarning EventType = "Warning"
)

// EventReason represents the reason code for an event.
type EventReason string

// MCPServer event reasons
const (
	// ReasonMCPServerCreated indicates an MCPServer was successfully created.
	ReasonMCPServerCreated EventReason = "MCPServerCreated"

	// ReasonMCPServerUpdated indicates an MCPServer was successfully updated.
	ReasonMCPServerUpdated EventReason = "MCPServerUpdated"

	// ReasonMCPServerDeleted indicates an MCPServer was successfully deleted.
	ReasonMCPServerDeleted EventReason = "MCPServerDeleted"

	// ReasonMCPServerStarted indicates an MCPServer service was started.
	ReasonMCPServerStarted EventReason = "MCPServerStarted"

	// ReasonMCPServerStopped indicates an MCPServer service was stopped.
	ReasonMCPServerStopped EventReason = "MCPServerStopped"

	// ReasonMCPServerFailed indicates an MCPServer operation failed.
	ReasonMCPServerFailed EventReason = "MCPServerFailed"
)

// ServiceClass event reasons
const (
	// ReasonServiceClassCreated indicates a ServiceClass was successfully created.
	ReasonServiceClassCreated EventReason = "ServiceClassCreated"

	// ReasonServiceClassUpdated indicates a ServiceClass was successfully updated.
	ReasonServiceClassUpdated EventReason = "ServiceClassUpdated"

	// ReasonServiceClassDeleted indicates a ServiceClass was successfully deleted.
	ReasonServiceClassDeleted EventReason = "ServiceClassDeleted"

	// ReasonServiceClassValidated indicates a ServiceClass was successfully validated.
	ReasonServiceClassValidated EventReason = "ServiceClassValidated"

	// ReasonServiceClassValidationFailed indicates a ServiceClass validation failed.
	ReasonServiceClassValidationFailed EventReason = "ServiceClassValidationFailed"
)

// Workflow event reasons
const (
	// ReasonWorkflowCreated indicates a Workflow was successfully created.
	ReasonWorkflowCreated EventReason = "WorkflowCreated"

	// ReasonWorkflowUpdated indicates a Workflow was successfully updated.
	ReasonWorkflowUpdated EventReason = "WorkflowUpdated"

	// ReasonWorkflowDeleted indicates a Workflow was successfully deleted.
	ReasonWorkflowDeleted EventReason = "WorkflowDeleted"

	// ReasonWorkflowExecuted indicates a Workflow was successfully executed.
	ReasonWorkflowExecuted EventReason = "WorkflowExecuted"

	// ReasonWorkflowExecutionFailed indicates a Workflow execution failed.
	ReasonWorkflowExecutionFailed EventReason = "WorkflowExecutionFailed"
)

// Service Instance event reasons
const (
	// ReasonServiceInstanceCreated indicates a service instance was successfully created.
	ReasonServiceInstanceCreated EventReason = "ServiceInstanceCreated"

	// ReasonServiceInstanceStarted indicates a service instance was successfully started.
	ReasonServiceInstanceStarted EventReason = "ServiceInstanceStarted"

	// ReasonServiceInstanceStopped indicates a service instance was successfully stopped.
	ReasonServiceInstanceStopped EventReason = "ServiceInstanceStopped"

	// ReasonServiceInstanceDeleted indicates a service instance was successfully deleted.
	ReasonServiceInstanceDeleted EventReason = "ServiceInstanceDeleted"

	// ReasonServiceInstanceFailed indicates a service instance operation failed.
	ReasonServiceInstanceFailed EventReason = "ServiceInstanceFailed"
)

// EventData holds contextual information for event message templating.
type EventData struct {
	// Name is the name of the object involved in the event.
	Name string

	// Namespace is the namespace of the object involved in the event.
	Namespace string

	// Operation is the operation that triggered the event (e.g., "create", "update", "delete").
	Operation string

	// ServiceClass is the ServiceClass name for service instance events.
	ServiceClass string

	// Arguments contains additional key-value data for the event.
	Arguments map[string]interface{}

	// Error contains error information for failure events.
	Error string

	// Duration is the duration of an operation (for execution events).
	Duration time.Duration

	// StepCount is the number of steps in a workflow execution.
	StepCount int
}

// ObjectReference represents a reference to a Kubernetes object for event creation.
type ObjectReference struct {
	// APIVersion is the API version of the object.
	APIVersion string

	// Kind is the kind of the object.
	Kind string

	// Name is the name of the object.
	Name string

	// Namespace is the namespace of the object.
	Namespace string

	// UID is the unique identifier of the object (optional).
	UID string
}

// getEventType returns the appropriate EventType for a given EventReason.
func getEventType(reason EventReason) EventType {
	switch reason {
	case ReasonMCPServerFailed,
		ReasonServiceClassValidationFailed,
		ReasonWorkflowExecutionFailed,
		ReasonServiceInstanceFailed:
		return EventTypeWarning
	default:
		return EventTypeNormal
	}
}
