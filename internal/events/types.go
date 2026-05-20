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

	// ReasonMCPServerTokenForwarded indicates an ID token was forwarded to a downstream server.
	// This event is generated when muster forwards a user's ID token instead of triggering
	// a separate OAuth flow, enabling SSO across the MCP ecosystem.
	ReasonMCPServerTokenForwarded EventReason = "MCPServerTokenForwarded"

	// ReasonMCPServerTokenForwardingFailed indicates ID token forwarding failed.
	// This may trigger fallback to server-specific OAuth if configured.
	ReasonMCPServerTokenForwardingFailed EventReason = "MCPServerTokenForwardingFailed"

	// ReasonMCPServerTokenExchanged indicates a token was successfully exchanged via RFC 8693.
	// This event is generated when muster exchanges a local token for one valid on a remote
	// cluster's Identity Provider, enabling cross-cluster SSO.
	ReasonMCPServerTokenExchanged EventReason = "MCPServerTokenExchanged"

	// ReasonMCPServerTokenExchangeFailed indicates RFC 8693 token exchange failed.
	// This may trigger fallback to server-specific OAuth if configured.
	ReasonMCPServerTokenExchangeFailed EventReason = "MCPServerTokenExchangeFailed"
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

// EventData holds contextual information for event message templating.
type EventData struct {
	// Name is the name of the object involved in the event.
	Name string

	// Namespace is the namespace of the object involved in the event.
	Namespace string

	// Operation is the operation that triggered the event (e.g., "create", "update", "delete").
	Operation string

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
		ReasonWorkflowExecutionFailed,
		ReasonWorkflowValidationFailed,
		ReasonWorkflowUnavailable,
		ReasonWorkflowToolsMissing,
		ReasonWorkflowStepFailed:
		return EventTypeWarning
	default:
		return EventTypeNormal
	}
}
