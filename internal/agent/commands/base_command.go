package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// ClientInterface defines the interface that commands need from the client.
// This interface abstracts the client functionality required by commands,
// enabling them to access cached data and perform operations without
// depending directly on the concrete client implementation.
//
// The interface provides:
//   - Access to cached tools, resources, and prompts
//   - Core MCP operations (tool calls, resource access, prompt execution)
//   - Formatter access for consistent output formatting
type ClientInterface interface {
	// Cache access methods return the currently cached items
	GetToolCache() []mcp.Tool
	GetResourceCache() []mcp.Resource
	GetPromptCache() []mcp.Prompt

	// Core MCP operations for executing commands
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error)
	GetResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error)
	GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error)

	// Formatters access for consistent output formatting
	// Returns the concrete Formatters type that will be cast by commands
	GetFormatters() interface{}
}

// FormatterInterface defines the interface for formatting operations.
// This provides a clean abstraction for commands to format MCP data
// consistently across different output modes and contexts.
//
// The interface supports:
//   - List formatting for browsing available items
//   - Detail formatting for comprehensive item information
//   - Search and lookup utilities for finding specific items
type FormatterInterface interface {
	// List formatting methods for displaying collections
	FormatToolsList(tools []mcp.Tool) string
	FormatResourcesList(resources []mcp.Resource) string
	FormatPromptsList(prompts []mcp.Prompt) string

	// Detail formatting methods for individual items
	FormatToolDetail(tool mcp.Tool) string
	FormatResourceDetail(resource mcp.Resource) string
	FormatPromptDetail(prompt mcp.Prompt) string

	// Search utilities for finding items by identifier
	FindTool(tools []mcp.Tool, name string) *mcp.Tool
	FindResource(resources []mcp.Resource, uri string) *mcp.Resource
	FindPrompt(prompts []mcp.Prompt, name string) *mcp.Prompt
}

// TransportInterface defines the interface for transport capability checking.
// Commands use this to adapt their behavior based on transport capabilities,
// particularly for features like real-time notifications.
type TransportInterface interface {
	// SupportsNotifications returns whether the transport supports real-time notifications
	SupportsNotifications() bool
}

// BaseCommand provides common functionality for all REPL commands.
// It encapsulates shared dependencies and utility methods that most
// commands need, reducing code duplication and ensuring consistent
// behavior across the command system.
//
// Key features:
//   - Dependency injection for client, logger, and transport
//   - Argument parsing and validation utilities
//   - Common completion helpers for tools, resources, and prompts
//   - Consistent error handling patterns
type BaseCommand struct {
	client    ClientInterface    // MCP client for operations
	output    OutputLogger       // Logger for user-facing output
	transport TransportInterface // Transport for capability checking
}

// NewBaseCommand creates a new base command with the specified dependencies.
// This constructor ensures all commands have access to the core functionality
// they need while maintaining clean separation of concerns.
//
// Args:
//   - client: MCP client interface for operations
//   - output: Logger interface for user-facing output
//   - transport: Transport interface for capability checking
//
// Returns:
//   - Configured base command instance
func NewBaseCommand(client ClientInterface, output OutputLogger, transport TransportInterface) *BaseCommand {
	return &BaseCommand{
		client:    client,
		output:    output,
		transport: transport,
	}
}

// parseArgs parses and validates command arguments against minimum requirements.
// This utility method provides consistent argument validation across commands
// and generates appropriate usage messages when validation fails.
//
// Args:
//   - args: Command arguments to validate
//   - minArgs: Minimum number of arguments required
//   - usage: Usage string to display on validation failure
//
// Returns:
//   - Validated arguments slice
//   - Error with usage information if validation fails
func (b *BaseCommand) parseArgs(args []string, minArgs int, usage string) ([]string, error) {
	if len(args) < minArgs {
		return nil, fmt.Errorf("usage: %s", usage)
	}
	return args, nil
}

// joinArgsFrom joins arguments starting from a specific index into a single string.
// This is useful for commands that accept free-form text or JSON arguments
// where multiple command line arguments should be treated as one logical argument.
//
// Args:
//   - args: Argument slice to process
//   - index: Starting index for joining (0-based)
//
// Returns:
//   - Joined string, or empty string if index is out of bounds
func (b *BaseCommand) joinArgsFrom(args []string, index int) string {
	if index >= len(args) {
		return ""
	}
	return strings.Join(args[index:], " ")
}

// validateTarget validates that a target type is one of the allowed values.
// This is used by commands that operate on different types of MCP items
// (tools, resources, prompts) to ensure valid target specification.
//
// Args:
//   - target: The target type to validate (case-insensitive)
//   - validTargets: Slice of valid target type names
//
// Returns:
//   - Error if target is not in validTargets, nil if valid
func (b *BaseCommand) validateTarget(target string, validTargets []string) error {
	for _, valid := range validTargets {
		if strings.ToLower(target) == strings.ToLower(valid) {
			return nil
		}
	}
	return fmt.Errorf("unknown target: %s. Valid targets: %s", target, strings.Join(validTargets, ", "))
}

// getCompletionsForTargets returns completion suggestions for valid targets.
// This provides tab completion support for commands that accept target types.
//
// Args:
//   - targets: Slice of valid target names to suggest
//
// Returns:
//   - Slice of completion suggestions
func (b *BaseCommand) getCompletionsForTargets(targets []string) []string {
	var completions []string
	for _, target := range targets {
		completions = append(completions, target)
	}
	return completions
}

// getToolCompletions returns tool name completions from the client cache.
// This provides tab completion support for commands that accept tool names.
//
// Returns:
//   - Slice of available tool names for completion
func (b *BaseCommand) getToolCompletions() []string {
	tools := b.client.GetToolCache()
	var completions []string
	for _, tool := range tools {
		completions = append(completions, tool.Name)
	}
	return completions
}

// getResourceCompletions returns resource URI completions from the client cache.
// This provides tab completion support for commands that accept resource URIs.
//
// Returns:
//   - Slice of available resource URIs for completion
func (b *BaseCommand) getResourceCompletions() []string {
	resources := b.client.GetResourceCache()
	var completions []string
	for _, resource := range resources {
		completions = append(completions, resource.URI)
	}
	return completions
}

// getPromptCompletions returns prompt name completions from the client cache.
// This provides tab completion support for commands that accept prompt names.
//
// Returns:
//   - Slice of available prompt names for completion
func (b *BaseCommand) getPromptCompletions() []string {
	prompts := b.client.GetPromptCache()
	var completions []string
	for _, prompt := range prompts {
		completions = append(completions, prompt.Name)
	}
	return completions
}

// getFormatters returns the formatters interface cast to the concrete type.
// This provides access to formatting utilities while maintaining interface
// abstraction for the base command functionality.
//
// Returns:
//   - FormatterInterface for consistent output formatting
func (b *BaseCommand) getFormatters() FormatterInterface {
	return b.client.GetFormatters().(FormatterInterface)
}
