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

	// ServiceClass templates
	e.templates[ReasonServiceClassCreated] = "ServiceClass {{.Name}} successfully created in namespace {{.Namespace}}"
	e.templates[ReasonServiceClassUpdated] = "ServiceClass {{.Name}} successfully updated in namespace {{.Namespace}}"
	e.templates[ReasonServiceClassDeleted] = "ServiceClass {{.Name}} successfully deleted from namespace {{.Namespace}}"
	e.templates[ReasonServiceClassValidated] = "ServiceClass {{.Name}} validation completed successfully"
	e.templates[ReasonServiceClassValidationFailed] = "ServiceClass {{.Name}} validation failed{{if .Error}}: {{.Error}}{{end}}"

	// Workflow templates
	e.templates[ReasonWorkflowCreated] = "Workflow {{.Name}} successfully created in namespace {{.Namespace}}"
	e.templates[ReasonWorkflowUpdated] = "Workflow {{.Name}} successfully updated in namespace {{.Namespace}}"
	e.templates[ReasonWorkflowDeleted] = "Workflow {{.Name}} successfully deleted from namespace {{.Namespace}}"
	e.templates[ReasonWorkflowExecuted] = "Workflow {{.Name}} executed successfully{{if .StepCount}} ({{.StepCount}} steps){{end}}{{if .Duration}} in {{.Duration}}{{end}}"
	e.templates[ReasonWorkflowExecutionFailed] = "Workflow {{.Name}} execution failed{{if .Error}}: {{.Error}}{{end}}"

	// Service Instance templates
	e.templates[ReasonServiceInstanceCreated] = "Service instance {{.Name}} created from ServiceClass {{.ServiceClass}}"
	e.templates[ReasonServiceInstanceStarted] = "Service instance {{.Name}} started successfully"
	e.templates[ReasonServiceInstanceStopped] = "Service instance {{.Name}} stopped successfully"
	e.templates[ReasonServiceInstanceDeleted] = "Service instance {{.Name}} deleted successfully"
	e.templates[ReasonServiceInstanceFailed] = "Service instance {{.Name}} operation failed{{if .Error}}: {{.Error}}{{end}}"
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
