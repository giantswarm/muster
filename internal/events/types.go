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

	// ReasonMCPServerAuthRequired indicates an MCPServer requires OAuth authentication.
	ReasonMCPServerAuthRequired EventReason = "MCPServerAuthRequired"
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
	// Configuration Management Events
	// ReasonWorkflowCreated indicates a Workflow was successfully created.
	ReasonWorkflowCreated EventReason = "WorkflowCreated"

	// ReasonWorkflowUpdated indicates a Workflow was successfully updated.
	ReasonWorkflowUpdated EventReason = "WorkflowUpdated"

	// ReasonWorkflowDeleted indicates a Workflow was successfully deleted.
	ReasonWorkflowDeleted EventReason = "WorkflowDeleted"

	// ReasonWorkflowValidationFailed indicates workflow definition validation failed.
	ReasonWorkflowValidationFailed EventReason = "WorkflowValidationFailed"

	// ReasonWorkflowValidationSucceeded indicates workflow definition validation passed.
	ReasonWorkflowValidationSucceeded EventReason = "WorkflowValidationSucceeded"

	// Execution Lifecycle Events
	// ReasonWorkflowExecutionStarted indicates workflow execution has begun.
	ReasonWorkflowExecutionStarted EventReason = "WorkflowExecutionStarted"

	// ReasonWorkflowExecutionCompleted indicates workflow execution completed successfully.
	ReasonWorkflowExecutionCompleted EventReason = "WorkflowExecutionCompleted"

	// ReasonWorkflowExecutionFailed indicates workflow execution failed.
	ReasonWorkflowExecutionFailed EventReason = "WorkflowExecutionFailed"

	// ReasonWorkflowExecutionTracked indicates execution state was persisted.
	ReasonWorkflowExecutionTracked EventReason = "WorkflowExecutionTracked"

	// Step-Level Execution Events
	// ReasonWorkflowStepStarted indicates individual step began execution.
	ReasonWorkflowStepStarted EventReason = "WorkflowStepStarted"

	// ReasonWorkflowStepCompleted indicates individual step completed successfully.
	ReasonWorkflowStepCompleted EventReason = "WorkflowStepCompleted"

	// ReasonWorkflowStepFailed indicates individual step failed (with allowFailure context).
	ReasonWorkflowStepFailed EventReason = "WorkflowStepFailed"

	// ReasonWorkflowStepSkipped indicates step was skipped due to condition evaluation.
	ReasonWorkflowStepSkipped EventReason = "WorkflowStepSkipped"

	// ReasonWorkflowStepConditionEvaluated indicates step condition was evaluated.
	ReasonWorkflowStepConditionEvaluated EventReason = "WorkflowStepConditionEvaluated"

	// Tool Availability Events
	// ReasonWorkflowAvailable indicates all required tools became available.
	ReasonWorkflowAvailable EventReason = "WorkflowAvailable"

	// ReasonWorkflowUnavailable indicates required tools became unavailable.
	ReasonWorkflowUnavailable EventReason = "WorkflowUnavailable"

	// ReasonWorkflowToolsDiscovered indicates new required tools are discovered and available.
	ReasonWorkflowToolsDiscovered EventReason = "WorkflowToolsDiscovered"

	// ReasonWorkflowToolsMissing indicates specific tools became unavailable.
	ReasonWorkflowToolsMissing EventReason = "WorkflowToolsMissing"

	// Tool Registration Events
	// ReasonWorkflowToolRegistered indicates workflow was registered as action_<workflow-name> tool.
	ReasonWorkflowToolRegistered EventReason = "WorkflowToolRegistered"

	// ReasonWorkflowToolUnregistered indicates workflow tool was removed from aggregator.
	ReasonWorkflowToolUnregistered EventReason = "WorkflowToolUnregistered"

	// ReasonWorkflowCapabilitiesRefreshed indicates aggregator capabilities were updated after workflow changes.
	ReasonWorkflowCapabilitiesRefreshed EventReason = "WorkflowCapabilitiesRefreshed"

	// Legacy event reasons (kept for compatibility)
	// ReasonWorkflowExecuted indicates a Workflow was successfully executed.
	ReasonWorkflowExecuted EventReason = "WorkflowExecuted"
)

// Service Instance event reasons
const (
	// ReasonServiceInstanceCreated indicates a service instance was successfully created.
	ReasonServiceInstanceCreated EventReason = "ServiceInstanceCreated"

	// ReasonServiceInstanceStarting indicates a service instance is beginning to start.
	ReasonServiceInstanceStarting EventReason = "ServiceInstanceStarting"

	// ReasonServiceInstanceStarted indicates a service instance was successfully started.
	ReasonServiceInstanceStarted EventReason = "ServiceInstanceStarted"

	// ReasonServiceInstanceStopping indicates a service instance is beginning to stop.
	ReasonServiceInstanceStopping EventReason = "ServiceInstanceStopping"

	// ReasonServiceInstanceStopped indicates a service instance was successfully stopped.
	ReasonServiceInstanceStopped EventReason = "ServiceInstanceStopped"

	// ReasonServiceInstanceRestarting indicates a service instance restart is initiated.
	ReasonServiceInstanceRestarting EventReason = "ServiceInstanceRestarting"

	// ReasonServiceInstanceRestarted indicates a service instance restart completed successfully.
	ReasonServiceInstanceRestarted EventReason = "ServiceInstanceRestarted"

	// ReasonServiceInstanceDeleted indicates a service instance was successfully deleted.
	ReasonServiceInstanceDeleted EventReason = "ServiceInstanceDeleted"

	// ReasonServiceInstanceFailed indicates a service instance operation failed.
	ReasonServiceInstanceFailed EventReason = "ServiceInstanceFailed"

	// ReasonServiceInstanceHealthy indicates a service instance health checks are passing.
	ReasonServiceInstanceHealthy EventReason = "ServiceInstanceHealthy"

	// ReasonServiceInstanceUnhealthy indicates a service instance health checks are failing.
	ReasonServiceInstanceUnhealthy EventReason = "ServiceInstanceUnhealthy"

	// ReasonServiceInstanceHealthCheckFailed indicates an individual health check failed.
	ReasonServiceInstanceHealthCheckFailed EventReason = "ServiceInstanceHealthCheckFailed"

	// ReasonServiceInstanceHealthCheckRecovered indicates health check recovered after failures.
	ReasonServiceInstanceHealthCheckRecovered EventReason = "ServiceInstanceHealthCheckRecovered"

	// ReasonServiceInstanceStateChanged indicates detailed state transitions.
	ReasonServiceInstanceStateChanged EventReason = "ServiceInstanceStateChanged"

	// ReasonServiceInstanceToolExecutionStarted indicates ServiceClass lifecycle tool execution began.
	ReasonServiceInstanceToolExecutionStarted EventReason = "ServiceInstanceToolExecutionStarted"

	// ReasonServiceInstanceToolExecutionCompleted indicates ServiceClass lifecycle tool execution succeeded.
	ReasonServiceInstanceToolExecutionCompleted EventReason = "ServiceInstanceToolExecutionCompleted"

	// ReasonServiceInstanceToolExecutionFailed indicates ServiceClass lifecycle tool execution failed.
	ReasonServiceInstanceToolExecutionFailed EventReason = "ServiceInstanceToolExecutionFailed"
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

	// Workflow-specific fields
	// StepID is the ID of the workflow step involved in the event.
	StepID string

	// StepTool is the tool used in the workflow step.
	StepTool string

	// ConditionResult is the result of step condition evaluation.
	ConditionResult string

	// ExecutionID is the unique identifier for a workflow execution.
	ExecutionID string

	// ToolNames contains the list of tools (for availability events).
	ToolNames []string

	// AllowFailure indicates whether a failed step allows failure.
	AllowFailure bool
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
		ReasonWorkflowValidationFailed,
		ReasonWorkflowUnavailable,
		ReasonWorkflowToolsMissing,
		ReasonWorkflowStepFailed,
		ReasonServiceInstanceFailed,
		ReasonServiceInstanceUnhealthy,
		ReasonServiceInstanceHealthCheckFailed,
		ReasonServiceInstanceToolExecutionFailed:
		return EventTypeWarning
	default:
		return EventTypeNormal
	}
}
