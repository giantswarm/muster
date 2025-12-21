package events

import (
	"fmt"
	"strings"
)

// MessageTemplateEngine provides dynamic message generation for events.
type MessageTemplateEngine struct {
	templates map[EventReason]string
}

// NewMessageTemplateEngine creates a new message template engine with default templates.
func NewMessageTemplateEngine() *MessageTemplateEngine {
	engine := &MessageTemplateEngine{
		templates: make(map[EventReason]string),
	}
	engine.loadDefaultTemplates()
	return engine
}

// loadDefaultTemplates initializes the default message templates for all event reasons.
func (e *MessageTemplateEngine) loadDefaultTemplates() {
	// MCPServer templates

	// CRD Management Events
	e.templates[ReasonMCPServerCreated] = "MCPServer {{.Name}} successfully created in namespace {{.Namespace}}"
	e.templates[ReasonMCPServerUpdated] = "MCPServer {{.Name}} successfully updated in namespace {{.Namespace}}"
	e.templates[ReasonMCPServerDeleted] = "MCPServer {{.Name}} successfully deleted from namespace {{.Namespace}}"

	// Service Lifecycle Events
	e.templates[ReasonMCPServerStarting] = "MCPServer {{.Name}} service is starting up"
	e.templates[ReasonMCPServerStarted] = "MCPServer {{.Name}} service started successfully"
	e.templates[ReasonMCPServerStopped] = "MCPServer {{.Name}} service stopped successfully"
	e.templates[ReasonMCPServerRestarting] = "MCPServer {{.Name}} service is restarting"
	e.templates[ReasonMCPServerFailed] = "MCPServer {{.Name}} operation failed{{if .Error}}: {{.Error}}{{end}}"

	// Tool Discovery Events
	e.templates[ReasonMCPServerToolsDiscovered] = "MCPServer {{.Name}} tools discovered and registered successfully"
	e.templates[ReasonMCPServerToolsUnavailable] = "MCPServer {{.Name}} tools became unavailable{{if .Error}}: {{.Error}}{{end}}"
	e.templates[ReasonMCPServerReconnected] = "MCPServer {{.Name}} reconnected successfully and tools are available"

	// Health and Recovery Events
	e.templates[ReasonMCPServerHealthCheckFailed] = "MCPServer {{.Name}} health check failed{{if .Error}}: {{.Error}}{{end}}"
	e.templates[ReasonMCPServerRecoveryStarted] = "MCPServer {{.Name}} automatic recovery process started"
	e.templates[ReasonMCPServerRecoverySucceeded] = "MCPServer {{.Name}} automatic recovery completed successfully"
	e.templates[ReasonMCPServerRecoveryFailed] = "MCPServer {{.Name}} automatic recovery failed{{if .Error}}: {{.Error}}{{end}}"
	e.templates[ReasonMCPServerAuthRequired] = "MCPServer {{.Name}} requires OAuth authentication to connect"

	// ServiceClass templates
	e.templates[ReasonServiceClassCreated] = "ServiceClass {{.Name}} successfully created in namespace {{.Namespace}}"
	e.templates[ReasonServiceClassUpdated] = "ServiceClass {{.Name}} successfully updated in namespace {{.Namespace}}"
	e.templates[ReasonServiceClassDeleted] = "ServiceClass {{.Name}} successfully deleted from namespace {{.Namespace}}"
	e.templates[ReasonServiceClassValidated] = "ServiceClass {{.Name}} validation completed successfully"
	e.templates[ReasonServiceClassValidationFailed] = "ServiceClass {{.Name}} validation failed{{if .Error}}: {{.Error}}{{end}}"

	// Workflow templates
	// Configuration Management Events
	e.templates[ReasonWorkflowCreated] = "Workflow {{.Name}} successfully created{{if .StepCount}} with {{.StepCount}} steps{{end}}"
	e.templates[ReasonWorkflowUpdated] = "Workflow {{.Name}} successfully updated{{if .StepCount}} with {{.StepCount}} steps{{end}}"
	e.templates[ReasonWorkflowDeleted] = "Workflow {{.Name}} successfully deleted from namespace {{.Namespace}}"
	e.templates[ReasonWorkflowValidationFailed] = "Workflow {{.Name}} validation failed{{if .Error}}: {{.Error}}{{end}}"
	e.templates[ReasonWorkflowValidationSucceeded] = "Workflow {{.Name}} validation completed successfully"

	// Execution Lifecycle Events
	e.templates[ReasonWorkflowExecutionStarted] = "Workflow {{.Name}} execution started{{if .ExecutionID}} (execution: {{.ExecutionID}}){{end}}"
	e.templates[ReasonWorkflowExecutionCompleted] = "Workflow {{.Name}} execution completed successfully{{if .StepCount}} ({{.StepCount}} steps){{end}}{{if .Duration}} in {{.Duration}}{{end}}"
	e.templates[ReasonWorkflowExecutionFailed] = "Workflow {{.Name}} execution failed{{if .StepID}} at step {{.StepID}}{{end}}{{if .Error}}: {{.Error}}{{end}}"
	e.templates[ReasonWorkflowExecutionTracked] = "Workflow {{.Name}} execution state persisted{{if .ExecutionID}} (execution: {{.ExecutionID}}){{end}}"

	// Step-Level Execution Events
	e.templates[ReasonWorkflowStepStarted] = "Workflow {{.Name}} step {{.StepID}} started (tool: {{.StepTool}})"
	e.templates[ReasonWorkflowStepCompleted] = "Workflow {{.Name}} step {{.StepID}} completed successfully"
	e.templates[ReasonWorkflowStepFailed] = "Workflow {{.Name}} step {{.StepID}} failed{{if .AllowFailure}} (allow_failure=true, continuing){{end}}{{if .Error}}: {{.Error}}{{end}}"
	e.templates[ReasonWorkflowStepSkipped] = "Workflow {{.Name}} step {{.StepID}} skipped: condition evaluation returned {{.ConditionResult}}"
	e.templates[ReasonWorkflowStepConditionEvaluated] = "Workflow {{.Name}} step {{.StepID}} condition evaluated to {{.ConditionResult}}"

	// Tool Availability Events
	e.templates[ReasonWorkflowAvailable] = "Workflow {{.Name}} is now available (all required tools are accessible)"
	e.templates[ReasonWorkflowUnavailable] = "Workflow {{.Name}} is unavailable{{if .ToolNames}} (missing tools: {{.ToolNames}}){{end}}"
	e.templates[ReasonWorkflowToolsDiscovered] = "Workflow {{.Name}} required tools discovered and are now available"
	e.templates[ReasonWorkflowToolsMissing] = "Workflow {{.Name}} tools became unavailable{{if .ToolNames}} (missing: {{.ToolNames}}){{end}}"

	// Tool Registration Events
	e.templates[ReasonWorkflowToolRegistered] = "Workflow {{.Name}} registered as tool 'action_{{.Name}}' in aggregator"
	e.templates[ReasonWorkflowToolUnregistered] = "Workflow {{.Name}} tool 'action_{{.Name}}' removed from aggregator"
	e.templates[ReasonWorkflowCapabilitiesRefreshed] = "Aggregator capabilities refreshed after workflow {{.Name}} changes"

	// Legacy templates (kept for compatibility)
	e.templates[ReasonWorkflowExecuted] = "Workflow {{.Name}} executed successfully{{if .StepCount}} ({{.StepCount}} steps){{end}}{{if .Duration}} in {{.Duration}}{{end}}"

	// Service Instance templates
	e.templates[ReasonServiceInstanceCreated] = "Service instance {{.Name}} created from ServiceClass {{.ServiceClass}}"
	e.templates[ReasonServiceInstanceStarting] = "Service instance {{.Name}} starting{{if .StepTool}} with tool {{.StepTool}}{{end}}"
	e.templates[ReasonServiceInstanceStarted] = "Service instance {{.Name}} started successfully and is running"
	e.templates[ReasonServiceInstanceStopping] = "Service instance {{.Name}} stopping{{if .StepTool}} with tool {{.StepTool}}{{end}}"
	e.templates[ReasonServiceInstanceStopped] = "Service instance {{.Name}} stopped successfully"
	e.templates[ReasonServiceInstanceRestarting] = "Service instance {{.Name}} restarting{{if .StepTool}} with tool {{.StepTool}}{{end}}"
	e.templates[ReasonServiceInstanceRestarted] = "Service instance {{.Name}} restarted successfully{{if .Duration}} after {{.Duration}}{{end}}"
	e.templates[ReasonServiceInstanceDeleted] = "Service instance {{.Name}} deleted successfully"
	e.templates[ReasonServiceInstanceFailed] = "Service instance {{.Name}} operation failed{{if .Error}}: {{.Error}}{{end}}"
	e.templates[ReasonServiceInstanceHealthy] = "Service instance {{.Name}} health checks passing{{if .StepCount}} ({{.StepCount}} consecutive successes){{end}}"
	e.templates[ReasonServiceInstanceUnhealthy] = "Service instance {{.Name}} health checks failing{{if .StepCount}} ({{.StepCount}} consecutive failures){{end}}"
	e.templates[ReasonServiceInstanceHealthCheckFailed] = "Service instance {{.Name}} health check failed{{if .Error}}: {{.Error}}{{end}}"
	e.templates[ReasonServiceInstanceHealthCheckRecovered] = "Service instance {{.Name}} health check recovered after {{.StepCount}} failures"
	e.templates[ReasonServiceInstanceStateChanged] = "Service instance {{.Name}} state changed: {{.ConditionResult}}"
	e.templates[ReasonServiceInstanceToolExecutionStarted] = "Service instance {{.Name}} {{.Operation}} tool {{.StepTool}} execution started"
	e.templates[ReasonServiceInstanceToolExecutionCompleted] = "Service instance {{.Name}} {{.Operation}} tool {{.StepTool}} execution completed successfully"
	e.templates[ReasonServiceInstanceToolExecutionFailed] = "Service instance {{.Name}} {{.Operation}} tool {{.StepTool}} execution failed{{if .Error}}: {{.Error}}{{end}}"
}

// Render generates a message for the given event reason and data.
func (e *MessageTemplateEngine) Render(reason EventReason, data EventData) string {
	template, exists := e.templates[reason]
	if !exists {
		// Fallback for unknown event reasons
		return fmt.Sprintf("Event: %s for %s/%s", string(reason), data.Namespace, data.Name)
	}

	return e.renderTemplate(template, data)
}

// SetTemplate allows customizing the message template for a specific event reason.
func (e *MessageTemplateEngine) SetTemplate(reason EventReason, template string) {
	e.templates[reason] = template
}

// GetTemplate returns the template for a specific event reason.
func (e *MessageTemplateEngine) GetTemplate(reason EventReason) (string, bool) {
	template, exists := e.templates[reason]
	return template, exists
}

// renderTemplate performs simple template rendering with EventData.
// This is a simplified template system that supports basic variable substitution.
func (e *MessageTemplateEngine) renderTemplate(template string, data EventData) string {
	result := template

	// Replace basic variables
	result = strings.ReplaceAll(result, "{{.Name}}", data.Name)
	result = strings.ReplaceAll(result, "{{.Namespace}}", data.Namespace)
	result = strings.ReplaceAll(result, "{{.Operation}}", data.Operation)
	result = strings.ReplaceAll(result, "{{.ServiceClass}}", data.ServiceClass)
	result = strings.ReplaceAll(result, "{{.Error}}", data.Error)

	// Replace workflow-specific variables
	result = strings.ReplaceAll(result, "{{.StepID}}", data.StepID)
	result = strings.ReplaceAll(result, "{{.StepTool}}", data.StepTool)
	result = strings.ReplaceAll(result, "{{.ConditionResult}}", data.ConditionResult)
	result = strings.ReplaceAll(result, "{{.ExecutionID}}", data.ExecutionID)

	// Handle duration formatting
	if strings.Contains(result, "{{.Duration}}") {
		if data.Duration > 0 {
			result = strings.ReplaceAll(result, "{{.Duration}}", data.Duration.String())
		} else {
			result = strings.ReplaceAll(result, "{{.Duration}}", "")
		}
	}

	// Handle step count
	if strings.Contains(result, "{{.StepCount}}") {
		if data.StepCount > 0 {
			result = strings.ReplaceAll(result, "{{.StepCount}}", fmt.Sprintf("%d", data.StepCount))
		} else {
			result = strings.ReplaceAll(result, "{{.StepCount}}", "")
		}
	}

	// Handle tool names array
	if strings.Contains(result, "{{.ToolNames}}") {
		if len(data.ToolNames) > 0 {
			result = strings.ReplaceAll(result, "{{.ToolNames}}", strings.Join(data.ToolNames, ", "))
		} else {
			result = strings.ReplaceAll(result, "{{.ToolNames}}", "")
		}
	}

	// Handle allow failure boolean
	if strings.Contains(result, "{{.AllowFailure}}") {
		result = strings.ReplaceAll(result, "{{.AllowFailure}}", fmt.Sprintf("%t", data.AllowFailure))
	}

	// Handle conditional blocks for error messages
	result = e.renderConditionals(result, data)

	return result
}

// renderConditionals handles simple conditional rendering in templates.
// Supports: {{if .FieldName}}content{{end}}
func (e *MessageTemplateEngine) renderConditionals(template string, data EventData) string {
	result := template

	// Handle {{if .Error}}...{{end}}
	result = e.renderConditional(result, "{{if .Error}}", "{{end}}", data.Error != "")

	// Handle {{if .Duration}}...{{end}}
	result = e.renderConditional(result, "{{if .Duration}}", "{{end}}", data.Duration > 0)

	// Handle {{if .StepCount}}...{{end}}
	result = e.renderConditional(result, "{{if .StepCount}}", "{{end}}", data.StepCount > 0)

	// Handle workflow-specific conditionals
	// Handle {{if .StepID}}...{{end}}
	result = e.renderConditional(result, "{{if .StepID}}", "{{end}}", data.StepID != "")

	// Handle {{if .ExecutionID}}...{{end}}
	result = e.renderConditional(result, "{{if .ExecutionID}}", "{{end}}", data.ExecutionID != "")

	// Handle {{if .ToolNames}}...{{end}}
	result = e.renderConditional(result, "{{if .ToolNames}}", "{{end}}", len(data.ToolNames) > 0)

	// Handle {{if .AllowFailure}}...{{end}}
	result = e.renderConditional(result, "{{if .AllowFailure}}", "{{end}}", data.AllowFailure)

	return result
}

// renderConditional handles a single conditional block.
func (e *MessageTemplateEngine) renderConditional(template, startMarker, endMarker string, condition bool) string {
	startIndex := strings.Index(template, startMarker)
	if startIndex == -1 {
		return template
	}

	endIndex := strings.Index(template[startIndex:], endMarker)
	if endIndex == -1 {
		return template
	}

	endIndex += startIndex // Convert to absolute index

	if condition {
		// Keep the content between markers, remove the markers
		before := template[:startIndex]
		content := template[startIndex+len(startMarker) : endIndex]
		after := template[endIndex+len(endMarker):]
		return before + content + after
	} else {
		// Remove the entire conditional block
		before := template[:startIndex]
		after := template[endIndex+len(endMarker):]
		return before + after
	}
}
