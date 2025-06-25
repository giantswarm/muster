package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// ClientInterface defines the interface that commands need from the client
// Since we know the concrete types, let's just import the agent package
type ClientInterface interface {
	// Cache access
	GetToolCache() []mcp.Tool
	GetResourceCache() []mcp.Resource
	GetPromptCache() []mcp.Prompt

	// Operations
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error)
	GetResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error)
	GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error)

	// Formatters - expect the concrete Formatters type
	GetFormatters() interface{} // Will cast to *Formatters
}

// FormatterInterface defines the interface for formatting operations
type FormatterInterface interface {
	FormatToolsList(tools []mcp.Tool) string
	FormatResourcesList(resources []mcp.Resource) string
	FormatPromptsList(prompts []mcp.Prompt) string
	FormatToolDetail(tool mcp.Tool) string
	FormatResourceDetail(resource mcp.Resource) string
	FormatPromptDetail(prompt mcp.Prompt) string
	FindTool(tools []mcp.Tool, name string) *mcp.Tool
	FindResource(resources []mcp.Resource, uri string) *mcp.Resource
	FindPrompt(prompts []mcp.Prompt, name string) *mcp.Prompt
}

// TransportInterface defines the interface for transport checking
type TransportInterface interface {
	SupportsNotifications() bool
}

// BaseCommand provides common functionality for all commands
type BaseCommand struct {
	client    ClientInterface
	output    OutputLogger
	transport TransportInterface
}

// NewBaseCommand creates a new base command
func NewBaseCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *BaseCommand {
	return &BaseCommand{
		client:    client,
		output:    output,
		transport: transport,
	}
}

// parseArgs parses command arguments, handling quoted arguments properly
func (b *BaseCommand) parseArgs(args []string, minArgs int, usage string) ([]string, error) {
	if len(args) < minArgs {
		return nil, fmt.Errorf("usage: %s", usage)
	}
	return args, nil
}

// joinArgsFrom joins arguments from a specific index
func (b *BaseCommand) joinArgsFrom(args []string, index int) string {
	if index >= len(args) {
		return ""
	}
	return strings.Join(args[index:], " ")
}

// validateTarget validates a target type for list/describe commands
func (b *BaseCommand) validateTarget(target string, validTargets []string) error {
	for _, valid := range validTargets {
		if strings.ToLower(target) == strings.ToLower(valid) {
			return nil
		}
	}
	return fmt.Errorf("unknown target: %s. Valid targets: %s", target, strings.Join(validTargets, ", "))
}

// getCompletionsForTargets returns completions for a list of valid targets
func (b *BaseCommand) getCompletionsForTargets(targets []string) []string {
	var completions []string
	for _, target := range targets {
		completions = append(completions, target)
	}
	return completions
}

// getToolCompletions returns tool name completions
func (b *BaseCommand) getToolCompletions() []string {
	tools := b.client.GetToolCache()
	var completions []string
	for _, tool := range tools {
		completions = append(completions, tool.Name)
	}
	return completions
}

// getResourceCompletions returns resource URI completions
func (b *BaseCommand) getResourceCompletions() []string {
	resources := b.client.GetResourceCache()
	var completions []string
	for _, resource := range resources {
		completions = append(completions, resource.URI)
	}
	return completions
}

// getPromptCompletions returns prompt name completions
func (b *BaseCommand) getPromptCompletions() []string {
	prompts := b.client.GetPromptCache()
	var completions []string
	for _, prompt := range prompts {
		completions = append(completions, prompt.Name)
	}
	return completions
}

// getFormatters returns the formatters interface cast to the concrete type
func (b *BaseCommand) getFormatters() FormatterInterface {
	return b.client.GetFormatters().(FormatterInterface)
}
