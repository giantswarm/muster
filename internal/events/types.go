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
	// CRD Management Events
	// ReasonMCPServerCreated indicates an MCPServer CRD was successfully created.
	ReasonMCPServerCreated EventReason = "MCPServerCreated"

	// ReasonMCPServerUpdated indicates an MCPServer CRD was successfully updated.
	ReasonMCPServerUpdated EventReason = "MCPServerUpdated"

	// ReasonMCPServerDeleted indicates an MCPServer CRD was successfully deleted.
	ReasonMCPServerDeleted EventReason = "MCPServerDeleted"

	// Service Lifecycle Events
	// ReasonMCPServerStarting indicates an MCPServer service is beginning to start.
	ReasonMCPServerStarting EventReason = "MCPServerStarting"

	// ReasonMCPServerStarted indicates an MCPServer service was started successfully.
	ReasonMCPServerStarted EventReason = "MCPServerStarted"

	// ReasonMCPServerStopped indicates an MCPServer service was stopped.
	ReasonMCPServerStopped EventReason = "MCPServerStopped"

	// ReasonMCPServerRestarting indicates an MCPServer service is being restarted.
	ReasonMCPServerRestarting EventReason = "MCPServerRestarting"

	// ReasonMCPServerFailed indicates an MCPServer operation failed.
	ReasonMCPServerFailed EventReason = "MCPServerFailed"

	// Tool Discovery Events
	// ReasonMCPServerToolsDiscovered indicates tools were successfully discovered from an MCPServer.
	ReasonMCPServerToolsDiscovered EventReason = "MCPServerToolsDiscovered"

	// ReasonMCPServerToolsUnavailable indicates tool discovery failed or tools became unavailable.
	ReasonMCPServerToolsUnavailable EventReason = "MCPServerToolsUnavailable"

	// ReasonMCPServerReconnected indicates connection to an MCPServer was restored.
	ReasonMCPServerReconnected EventReason = "MCPServerReconnected"

	// Health and Recovery Events
	// ReasonMCPServerHealthCheckFailed indicates health checks failed for an MCPServer.
	ReasonMCPServerHealthCheckFailed EventReason = "MCPServerHealthCheckFailed"

	// ReasonMCPServerRecoveryStarted indicates automatic recovery began for an MCPServer.
	ReasonMCPServerRecoveryStarted EventReason = "MCPServerRecoveryStarted"

	// ReasonMCPServerRecoverySucceeded indicates automatic recovery succeeded for an MCPServer.
	ReasonMCPServerRecoverySucceeded EventReason = "MCPServerRecoverySucceeded"

	// ReasonMCPServerRecoveryFailed indicates automatic recovery failed for an MCPServer.
	ReasonMCPServerRecoveryFailed EventReason = "MCPServerRecoveryFailed"
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

	// Tool Availability Events
	// ReasonServiceClassAvailable indicates all required tools became available (transitions to Available=true).
	ReasonServiceClassAvailable EventReason = "ServiceClassAvailable"

	// ReasonServiceClassUnavailable indicates required tools became unavailable (transitions to Available=false).
	ReasonServiceClassUnavailable EventReason = "ServiceClassUnavailable"

	// ReasonServiceClassToolsDiscovered indicates new required tools are discovered and available.
	ReasonServiceClassToolsDiscovered EventReason = "ServiceClassToolsDiscovered"

	// ReasonServiceClassToolsMissing indicates specific tools became unavailable.
	ReasonServiceClassToolsMissing EventReason = "ServiceClassToolsMissing"

	// ReasonServiceClassToolsRestored indicates previously missing tools became available again.
	ReasonServiceClassToolsRestored EventReason = "ServiceClassToolsRestored"
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
		ReasonMCPServerToolsUnavailable,
		ReasonMCPServerHealthCheckFailed,
		ReasonMCPServerRecoveryFailed,
		ReasonServiceClassValidationFailed,
		ReasonServiceClassUnavailable,
		ReasonServiceClassToolsMissing,
		ReasonWorkflowExecutionFailed,
		ReasonServiceInstanceFailed:
		return EventTypeWarning
	default:
		return EventTypeNormal
	}
}
