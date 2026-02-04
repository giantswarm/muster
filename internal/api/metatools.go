package api

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// MetaToolsHandler provides server-side meta-tool functionality for AI assistants.
// It enables tool discovery, execution, and resource/prompt access through
// a unified interface that can be used by the metatools package.
//
// The handler provides the data access layer that the metatools package
// handlers use to retrieve and manipulate tools, resources, and prompts.
// It abstracts the underlying aggregator implementation, following the
// API service locator pattern.
//
// This interface will be implemented by the aggregator or a dedicated
// adapter in a future integration issue.
type MetaToolsHandler interface {
	// Tool operations

	// ListTools returns all available tools for the current session.
	// The returned tools are session-aware based on authentication state.
	//
	// Args:
	//   - ctx: Context containing session information
	//
	// Returns:
	//   - []mcp.Tool: List of available tools
	//   - error: Error if listing fails
	ListTools(ctx context.Context) ([]mcp.Tool, error)

	// CallTool executes a tool by name with the provided arguments.
	// This is the primary interface for tool execution through meta-tools.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - name: Name of the tool to execute
	//   - args: Arguments to pass to the tool
	//
	// Returns:
	//   - *mcp.CallToolResult: The result of the tool execution
	//   - error: Error if execution fails
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error)

	// Resource operations

	// ListResources returns all available resources for the current session.
	// The returned resources are session-aware based on authentication state.
	//
	// Args:
	//   - ctx: Context containing session information
	//
	// Returns:
	//   - []mcp.Resource: List of available resources
	//   - error: Error if listing fails
	ListResources(ctx context.Context) ([]mcp.Resource, error)

	// GetResource retrieves the contents of a resource by URI.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - uri: URI of the resource to retrieve
	//
	// Returns:
	//   - *mcp.ReadResourceResult: The resource contents
	//   - error: Error if retrieval fails
	GetResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error)

	// Prompt operations

	// ListPrompts returns all available prompts for the current session.
	// The returned prompts are session-aware based on authentication state.
	//
	// Args:
	//   - ctx: Context containing session information
	//
	// Returns:
	//   - []mcp.Prompt: List of available prompts
	//   - error: Error if listing fails
	ListPrompts(ctx context.Context) ([]mcp.Prompt, error)

	// GetPrompt executes a prompt with the provided arguments.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - name: Name of the prompt to execute
	//   - args: Arguments to pass to the prompt (as string values)
	//
	// Returns:
	//   - *mcp.GetPromptResult: The prompt result with messages
	//   - error: Error if execution fails
	GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error)
}
