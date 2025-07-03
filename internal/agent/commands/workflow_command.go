package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// WorkflowCommand executes workflows with parameters
type WorkflowCommand struct {
	*BaseCommand
}

// NewWorkflowCommand creates a new workflow command
func NewWorkflowCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *WorkflowCommand {
	return &WorkflowCommand{
		BaseCommand: NewBaseCommand(client, output, transport),
	}
}

// Execute executes a workflow with the given parameters
func (w *WorkflowCommand) Execute(ctx context.Context, args []string) error {
	parsed, err := w.parseArgs(args, 1, w.Usage())
	if err != nil {
		return err
	}

	workflowName := parsed[0]

	// Parse workflow parameters from remaining arguments
	workflowParams := w.parseWorkflowParameters(parsed[1:])

	// Show what we're doing
	if len(workflowParams) > 0 {
		w.output.Info("Executing workflow '%s' with parameters: %v", workflowName, workflowParams)
	} else {
		w.output.Info("Executing workflow '%s'...", workflowName)
	}

	// Execute the workflow using workflow_<workflow-name> pattern
	toolName := fmt.Sprintf("workflow_%s", workflowName)

	// Call the workflow tool
	result, err := w.client.CallTool(ctx, toolName, workflowParams)
	if err != nil {
		w.output.Error("Workflow execution failed: %v", err)
		return nil
	}

	// Handle error results
	if result.IsError {
		w.output.OutputLine("Workflow returned an error:")
		for _, content := range result.Content {
			if textContent, ok := content.(mcp.TextContent); ok {
				w.output.OutputLine("  %s", textContent.Text)
			}
		}
		return nil
	}

	// Display formatted results
	w.output.Success("Workflow execution completed successfully!")
	w.output.OutputLine("")

	// Parse and format the result nicely
	for _, content := range result.Content {
		switch v := content.(type) {
		case mcp.TextContent:
			// Try to parse as JSON and format it nicely
			var jsonObj interface{}
			if err := json.Unmarshal([]byte(v.Text), &jsonObj); err == nil {
				w.formatWorkflowResult(jsonObj)
			} else {
				// Fallback to plain text if not JSON
				w.output.OutputLine(v.Text)
			}
		case mcp.ImageContent:
			w.output.OutputLine("[Image: MIME type %s, %d bytes]", v.MIMEType, len(v.Data))
		case mcp.AudioContent:
			w.output.OutputLine("[Audio: MIME type %s, %d bytes]", v.MIMEType, len(v.Data))
		default:
			w.output.OutputLine("%+v", content)
		}
	}

	return nil
}

// formatWorkflowResult formats workflow execution results in a user-friendly way
func (w *WorkflowCommand) formatWorkflowResult(data interface{}) {
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		// If it's not a map, just show it as formatted JSON
		if b, err := json.MarshalIndent(data, "", "  "); err == nil {
			w.output.OutputLine(string(b))
		} else {
			w.output.OutputLine("%+v", data)
		}
		return
	}

	// Display execution summary
	if executionID, exists := dataMap["execution_id"]; exists {
		w.output.OutputLine("Execution ID: %s", executionID)
	}

	if workflow, exists := dataMap["workflow"]; exists {
		w.output.OutputLine("Workflow: %s", workflow)
	}

	if status, exists := dataMap["status"]; exists {
		statusStr := fmt.Sprintf("%v", status)
		var statusIcon string
		switch strings.ToLower(statusStr) {
		case "completed":
			statusIcon = "✅"
		case "failed":
			statusIcon = "❌"
		case "running":
			statusIcon = "⏳"
		default:
			statusIcon = "ℹ️"
		}
		w.output.OutputLine("Status: %s %s", statusIcon, statusStr)
	}

	// Show input parameters if they exist
	if input, exists := dataMap["input"]; exists {
		if inputMap, ok := input.(map[string]interface{}); ok && len(inputMap) > 0 {
			w.output.OutputLine("")
			w.output.OutputLine("📝 Input Parameters:")

			// Sort parameters for consistent display
			var paramNames []string
			for paramName := range inputMap {
				paramNames = append(paramNames, paramName)
			}
			sort.Strings(paramNames)

			for _, paramName := range paramNames {
				value := inputMap[paramName]
				w.output.OutputLine("  %s: %v", paramName, value)
			}
		}
	}

	// Show step results if they exist
	if results, exists := dataMap["results"]; exists {
		if resultsMap, ok := results.(map[string]interface{}); ok && len(resultsMap) > 0 {
			w.output.OutputLine("")
			w.output.OutputLine("🔄 Step Results:")

			// Sort step names for consistent display
			var stepNames []string
			for stepName := range resultsMap {
				stepNames = append(stepNames, stepName)
			}
			sort.Strings(stepNames)

			for _, stepName := range stepNames {
				stepResult := resultsMap[stepName]
				if stepMap, ok := stepResult.(map[string]interface{}); ok {
					status := "unknown"
					if stepStatus, exists := stepMap["status"]; exists {
						status = fmt.Sprintf("%v", stepStatus)
					}

					// Format status with icon
					var statusIcon string
					switch strings.ToLower(status) {
					case "completed":
						statusIcon = "✅"
					case "failed":
						statusIcon = "❌"
					case "skipped":
						statusIcon = "⏭️"
					default:
						statusIcon = "ℹ️"
					}

					w.output.OutputLine("  %s: %s %s", stepName, statusIcon, status)

					// Try to show meaningful details from the result
					if result, exists := stepMap["result"]; exists {
						if resultMap, ok := result.(map[string]interface{}); ok {
							if name, exists := resultMap["name"]; exists {
								w.output.OutputLine("    Created: %v", name)
							} else if health, exists := resultMap["health"]; exists {
								w.output.OutputLine("    Health: %v", health)
							} else if state, exists := resultMap["state"]; exists {
								w.output.OutputLine("    State: %v", state)
							}
						}
					}
				}
			}
		}
	}

	w.output.OutputLine("")
}

// parseWorkflowParameters parses workflow parameters from args in key=value format
func (w *WorkflowCommand) parseWorkflowParameters(args []string) map[string]interface{} {
	params := make(map[string]interface{})

	for _, arg := range args {
		if strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				params[parts[0]] = parts[1]
			}
		}
	}

	return params
}

// getWorkflowNames fetches available workflow names for completion
func (w *WorkflowCommand) getWorkflowNames(ctx context.Context) []string {
	// Call the core_workflow_list tool to get workflows
	result, err := w.client.CallTool(ctx, "core_workflow_list", map[string]interface{}{})
	if err != nil {
		return []string{}
	}

	if result.IsError {
		return []string{}
	}

	// Parse workflow names from result
	var names []string

	// Try to parse JSON from the first text content
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			var jsonResult interface{}
			if err := json.Unmarshal([]byte(textContent.Text), &jsonResult); err == nil {
				if resultMap, ok := jsonResult.(map[string]interface{}); ok {
					if workflowsData, exists := resultMap["workflows"]; exists {
						if workflows, ok := workflowsData.([]interface{}); ok {
							for _, workflow := range workflows {
								if workflowMap, ok := workflow.(map[string]interface{}); ok {
									if name, exists := workflowMap["name"]; exists {
										if nameStr, ok := name.(string); ok {
											names = append(names, nameStr)
										}
									}
								}
							}
						}
					}
				}
			}
			break // Only process first text content
		}
	}

	sort.Strings(names)
	return names
}

// getWorkflowParameters fetches parameter names for a specific workflow
func (w *WorkflowCommand) getWorkflowParameters(ctx context.Context, workflowName string) []string {
	// Call the core_workflow_get tool to get workflow details
	result, err := w.client.CallTool(ctx, "core_workflow_get", map[string]interface{}{
		"name": workflowName,
	})
	if err != nil {
		return []string{}
	}

	if result.IsError {
		return []string{}
	}

	// Extract parameter names from workflow definition
	var paramNames []string

	// Try to parse JSON from the first text content
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			var jsonResult interface{}
			if err := json.Unmarshal([]byte(textContent.Text), &jsonResult); err == nil {
				if resultMap, ok := jsonResult.(map[string]interface{}); ok {
					if workflowData, exists := resultMap["workflow"]; exists {
						if workflow, ok := workflowData.(map[string]interface{}); ok {
							if args, exists := workflow["args"]; exists {
								if argsMap, ok := args.(map[string]interface{}); ok {
									for paramName := range argsMap {
										paramNames = append(paramNames, paramName)
									}
								}
							}
						}
					}
				}
			}
			break // Only process first text content
		}
	}

	sort.Strings(paramNames)
	return paramNames
}

// Usage returns the usage string
func (w *WorkflowCommand) Usage() string {
	return "workflow <workflow-name> [param1=value1] [param2=value2] ..."
}

// Description returns the command description
func (w *WorkflowCommand) Description() string {
	return "Execute a workflow with optional parameters"
}

// Completions returns possible completions
func (w *WorkflowCommand) Completions(input string) []string {
	parts := strings.Fields(input)

	// Create a background context for completion queries
	ctx := context.Background()

	if len(parts) == 1 {
		// Complete workflow names
		return w.getWorkflowNames(ctx)
	} else if len(parts) >= 2 {
		// Complete parameter names for the specified workflow
		workflowName := parts[1]
		paramNames := w.getWorkflowParameters(ctx, workflowName)

		// Format as param= for easy completion
		var completions []string
		for _, param := range paramNames {
			completions = append(completions, param+"=")
		}
		return completions
	}

	return []string{}
}

// Aliases returns command aliases
func (w *WorkflowCommand) Aliases() []string {
	return []string{"wf", "run-workflow"}
}
