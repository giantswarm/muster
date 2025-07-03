package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// ListCommand lists available tools, resources, or prompts
type ListCommand struct {
	*BaseCommand
}

// NewListCommand creates a new list command
func NewListCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *ListCommand {
	return &ListCommand{
		BaseCommand: NewBaseCommand(client, output, transport),
	}
}

// Execute lists tools, resources, or prompts
func (l *ListCommand) Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: %s", l.Usage())
	}

	target := strings.ToLower(args[0])
	switch target {
	case "tools":
		if err := l.client.RefreshToolCache(ctx); err != nil {
			l.output.Error("Failed to refresh tool cache: %v", err)
			// Continue with the cached tools if refresh fails
		}
		return l.listTools()
	case "resources":
		if err := l.client.RefreshResourceCache(ctx); err != nil {
			l.output.Error("Failed to refresh resource cache: %v", err)
			// Continue with the cached tools if refresh fails
		}
		return l.listResources()
	case "prompts":
		if err := l.client.RefreshPromptCache(ctx); err != nil {
			l.output.Error("Failed to refresh prompt cache: %v", err)
			// Continue with the cached tools if refresh fails
		}
		return l.listPrompts()
	case "workflows":
		return l.listWorkflows(ctx)
	case "core-tools":
		if err := l.client.RefreshToolCache(ctx); err != nil {
			l.output.Error("Failed to refresh tool cache: %v", err)
			// Continue with the cached tools if refresh fails
		}
		return l.listCoreTools(ctx)
	default:
		return l.validateTarget(target, []string{"tools", "resources", "prompts", "workflows", "core-tools"})
	}
}

// listTools lists all available tools
func (l *ListCommand) listTools() error {
	tools := l.client.GetToolCache()
	l.output.OutputLine(l.getFormatters().FormatToolsList(tools))
	return nil
}

// listResources lists all available resources
func (l *ListCommand) listResources() error {
	resources := l.client.GetResourceCache()
	l.output.OutputLine(l.getFormatters().FormatResourcesList(resources))
	return nil
}

// listPrompts lists all available prompts
func (l *ListCommand) listPrompts() error {
	prompts := l.client.GetPromptCache()
	l.output.OutputLine(l.getFormatters().FormatPromptsList(prompts))
	return nil
}

// listCoreTools lists muster core tools by filtering tools that start with "core_"
func (l *ListCommand) listCoreTools(ctx context.Context) error {
	l.output.Info("Fetching core muster tools...")

	// Get all tools from cache
	tools := l.client.GetToolCache()

	if len(tools) == 0 {
		l.output.OutputLine("No tools available")
		return nil
	}

	// Filter tools that start with "core" (case-insensitive)
	var coreTools []mcp.Tool
	pattern := "core"

	for _, tool := range tools {
		toolName := strings.ToLower(tool.Name)
		if strings.HasPrefix(toolName, pattern) {
			coreTools = append(coreTools, tool)
		}
	}

	if len(coreTools) == 0 {
		l.output.OutputLine("No core tools found (searched %d total tools)", len(tools))
		return nil
	}

	l.output.OutputLine("Core muster tools (%d found out of %d total):", len(coreTools), len(tools))
	l.output.OutputLine("")

	// Display each core tool
	for i, tool := range coreTools {
		l.output.OutputLine("  %d. %-27s - %s", i+1, tool.Name, tool.Description)
	}

	return nil
}

// listWorkflows lists all available workflows with their descriptions and parameters
func (l *ListCommand) listWorkflows(ctx context.Context) error {
	l.output.Info("Fetching available workflows...")

	// Call the core_workflow_list tool to get workflows
	result, err := l.client.CallTool(ctx, "core_workflow_list", map[string]interface{}{})
	if err != nil {
		l.output.Error("Failed to get workflow list: %v", err)
		return nil
	}

	if result.IsError {
		l.output.Error("Error fetching workflows:")
		for _, content := range result.Content {
			if textContent, ok := content.(mcp.TextContent); ok {
				l.output.OutputLine("  %s", textContent.Text)
			}
		}
		return nil
	}

	// Parse the workflow list from the result
	if len(result.Content) == 0 {
		l.output.OutputLine("No workflows available")
		return nil
	}

	// Parse JSON from the first text content
	var workflows []map[string]interface{}

	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			var jsonResult interface{}
			if err := json.Unmarshal([]byte(textContent.Text), &jsonResult); err == nil {
				if resultMap, ok := jsonResult.(map[string]interface{}); ok {
					if workflowsData, exists := resultMap["workflows"]; exists {
						if workflowArray, ok := workflowsData.([]interface{}); ok {
							for _, workflow := range workflowArray {
								if workflowMap, ok := workflow.(map[string]interface{}); ok {
									workflows = append(workflows, workflowMap)
								}
							}
						}
					}
				}
			}
			break // Only process first text content
		}
	}

	if len(workflows) == 0 {
		l.output.OutputLine("No workflows found")
		return nil
	}

	// Sort workflows by name for consistent ordering
	sort.Slice(workflows, func(i, j int) bool {
		nameI, _ := workflows[i]["name"].(string)
		nameJ, _ := workflows[j]["name"].(string)
		return nameI < nameJ
	})

	l.output.OutputLine("Available workflows (%d found):", len(workflows))
	l.output.OutputLine("")

	// Display each workflow with details
	for i, workflow := range workflows {
		name, _ := workflow["name"].(string)
		description, _ := workflow["description"].(string)
		available, _ := workflow["available"].(bool)

		// Get availability indicator
		availabilityIcon := "✅"
		if !available {
			availabilityIcon = "❌"
		}

		// Format the basic info
		if description != "" {
			l.output.OutputLine("  %d. %s %-20s - %s", i+1, availabilityIcon, name, description)
		} else {
			l.output.OutputLine("  %d. %s %-20s", i+1, availabilityIcon, name)
		}

		// Get workflow details to show parameters
		if params := l.getWorkflowParameters(ctx, name); len(params) > 0 {
			l.output.OutputLine("     Parameters: %s", strings.Join(params, ", "))
		}

		if i < len(workflows)-1 {
			l.output.OutputLine("")
		}
	}

	return nil
}

// getWorkflowParameters fetches parameter names for a specific workflow
func (l *ListCommand) getWorkflowParameters(ctx context.Context, workflowName string) []string {
	// Call the core_workflow_get tool to get workflow details
	result, err := l.client.CallTool(ctx, "core_workflow_get", map[string]interface{}{
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
func (l *ListCommand) Usage() string {
	return "list <tools|resources|prompts|workflows|core-tools>"
}

// Description returns the command description
func (l *ListCommand) Description() string {
	return "List available tools, resources, prompts, workflows, or core muster tools"
}

// Completions returns possible completions
func (l *ListCommand) Completions(input string) []string {
	return l.getCompletionsForTargets([]string{"tools", "resources", "prompts", "workflows", "core-tools"})
}

// Aliases returns command aliases
func (l *ListCommand) Aliases() []string {
	return []string{"ls"}
}
