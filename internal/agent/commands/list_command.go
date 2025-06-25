package commands

import (
	"context"
	"fmt"
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
		return l.listTools()
	case "resources":
		return l.listResources()
	case "prompts":
		return l.listPrompts()
	case "core-tools":
		return l.listCoreTools(ctx)
	default:
		return l.validateTarget(target, []string{"tools", "resources", "prompts", "core-tools"})
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

// Usage returns the usage string
func (l *ListCommand) Usage() string {
	return "list <tools|resources|prompts|core-tools>"
}

// Description returns the command description
func (l *ListCommand) Description() string {
	return "List available tools, resources, prompts, or core muster tools"
}

// Completions returns possible completions
func (l *ListCommand) Completions(input string) []string {
	return l.getCompletionsForTargets([]string{"tools", "resources", "prompts", "core-tools"})
}

// Aliases returns command aliases
func (l *ListCommand) Aliases() []string {
	return []string{"ls"}
}
