package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/giantswarm/muster/internal/metatools"
	pkgstrings "github.com/giantswarm/muster/pkg/strings"

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
	case "tool", "tools":
		return l.listTools(ctx)
	case "resource", "resources":
		if err := l.client.RefreshResourceCache(ctx); err != nil {
			l.output.Error("Failed to refresh resource cache: %v", err)
			// Continue with the cached resources if refresh fails
		}
		return l.listResources()
	case "prompt", "prompts":
		if err := l.client.RefreshPromptCache(ctx); err != nil {
			l.output.Error("Failed to refresh prompt cache: %v", err)
			// Continue with the cached prompts if refresh fails
		}
		return l.listPrompts()
	case "workflow", "workflows":
		return l.listWorkflows(ctx)
	case "core-tool", "core-tools":
		return l.listCoreTools(ctx)
	default:
		return l.validateTarget(target, []string{
			"tool", "tools",
			"resource", "resources",
			"prompt", "prompts",
			"workflow", "workflows",
			"core-tool", "core-tools",
		})
	}
}

// listTools lists all available tools by calling the list_tools meta-tool.
// This returns the actual tools (core_*, x_*, workflow_*) rather than the
// meta-tools exposed by the MCP native tools/list protocol.
func (l *ListCommand) listTools(ctx context.Context) error {
	// Call the list_tools meta-tool to get actual tools
	result, err := l.client.CallTool(ctx, metatools.ToolListTools, map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}

	if result.IsError {
		l.output.Error("Error listing tools:")
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				l.output.OutputLine("  %s", textContent.Text)
			}
		}
		return nil
	}

	// Parse the JSON response from list_tools
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			var response metatools.ListToolsResponse

			if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
				// Not JSON, just output the raw text
				l.output.OutputLine(textContent.Text)
				return nil
			}

			if len(response.Tools) == 0 {
				l.output.OutputLine("No tools available")
				return nil
			}

			// Sort tools alphabetically by name
			sort.Slice(response.Tools, func(i, j int) bool {
				return response.Tools[i].Name < response.Tools[j].Name
			})

			l.output.OutputLine("Available tools (%d):", len(response.Tools))
			l.output.OutputLine("")

			for i, tool := range response.Tools {
				desc := pkgstrings.TruncateDescription(tool.Description, pkgstrings.DefaultDescriptionMaxLen)
				l.output.OutputLine("  %d. %-30s - %s", i+1, tool.Name, desc)
			}

			// Show servers requiring auth if any
			if len(response.ServersRequiringAuth) > 0 {
				l.output.OutputLine("")
				l.output.OutputLine("Servers requiring authentication:")
				for _, server := range response.ServersRequiringAuth {
					l.output.OutputLine("  - %s (use '%s' to authenticate)", server.Name, server.AuthTool)
				}
			}

			return nil
		}
	}

	l.output.OutputLine("No tools available")
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

// listCoreTools lists muster core tools by calling the list_core_tools meta-tool.
// This returns only the core_* tools which are muster's built-in functionality.
func (l *ListCommand) listCoreTools(ctx context.Context) error {
	l.output.Info("Fetching core muster tools...")

	// Call the list_core_tools meta-tool
	result, err := l.client.CallTool(ctx, metatools.ToolListCoreTools, map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("failed to list core tools: %w", err)
	}

	if result.IsError {
		l.output.Error("Error listing core tools:")
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				l.output.OutputLine("  %s", textContent.Text)
			}
		}
		return nil
	}

	// Parse the JSON response from list_core_tools (uses FilterToolsResponse format)
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			var response metatools.FilterToolsResponse

			if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
				// Not JSON, just output the raw text
				l.output.OutputLine(textContent.Text)
				return nil
			}

			if len(response.Tools) == 0 {
				l.output.OutputLine("No core tools found (searched %d total tools)", response.TotalTools)
				return nil
			}

			// Sort tools alphabetically by name
			sort.Slice(response.Tools, func(i, j int) bool {
				return response.Tools[i].Name < response.Tools[j].Name
			})

			l.output.OutputLine("Core muster tools (%d found out of %d total):", response.FilteredCount, response.TotalTools)
			l.output.OutputLine("")

			for i, tool := range response.Tools {
				desc := pkgstrings.TruncateDescription(tool.Description, pkgstrings.DefaultDescriptionMaxLen)
				l.output.OutputLine("  %d. %-27s - %s", i+1, tool.Name, desc)
			}

			return nil
		}
	}

	l.output.OutputLine("No core tools found")
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
			if textContent, ok := mcp.AsTextContent(content); ok {
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
		if textContent, ok := mcp.AsTextContent(content); ok {
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
		availabilityIndicator := "[available]"
		if !available {
			availabilityIndicator = "[unavailable]"
		}

		// Format the basic info
		if description != "" {
			desc := pkgstrings.TruncateDescription(description, pkgstrings.DefaultDescriptionMaxLen)
			l.output.OutputLine("  %d. %-20s %s - %s", i+1, name, availabilityIndicator, desc)
		} else {
			l.output.OutputLine("  %d. %-20s %s", i+1, name, availabilityIndicator)
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
		if textContent, ok := mcp.AsTextContent(content); ok {
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
