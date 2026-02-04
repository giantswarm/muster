package metatools

import (
	"context"

	"muster/internal/api"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// Adapter connects the metatools package to the central API layer.
// It implements the api.MetaToolsHandler interface by delegating to
// an underlying data provider (typically the aggregator).
//
// The Adapter follows the API service locator pattern, allowing the
// metatools package to be registered during application initialization
// and accessed by handlers through the api.GetMetaTools() function.
type Adapter struct {
	provider *Provider
	// dataProvider will be set during integration (issue #343)
	// For now, the adapter is standalone and returns errors when called
}

// NewAdapter creates a new metatools adapter instance.
// The adapter manages the metatools provider and handles registration
// with the API layer.
//
// Returns:
//   - *Adapter: A new adapter instance ready for registration
func NewAdapter() *Adapter {
	return &Adapter{
		provider: NewProvider(),
	}
}

// Register registers the adapter with the API layer.
// This makes the metatools functionality available through api.GetMetaTools().
//
// This method should be called during application initialization after
// the aggregator has been set up.
func (a *Adapter) Register() {
	api.RegisterMetaTools(a)
	logging.Debug("metatools", "Adapter registered with API layer")
}

// GetProvider returns the underlying metatools provider.
// This allows access to provider functionality for direct use.
//
// Returns:
//   - *Provider: The metatools provider instance
func (a *Adapter) GetProvider() *Provider {
	return a.provider
}

// GetTools returns metadata for all meta-tools this package offers.
// This implements the api.ToolProvider interface for tool discovery.
//
// Returns:
//   - []api.ToolMetadata: List of all meta-tools provided
func (a *Adapter) GetTools() []api.ToolMetadata {
	return a.provider.GetTools()
}

// ExecuteTool executes a specific meta-tool by name with the provided arguments.
// This implements the api.ToolProvider interface for tool execution.
//
// Args:
//   - ctx: Context for the operation
//   - toolName: The name of the meta-tool to execute
//   - args: Arguments for the tool execution
//
// Returns:
//   - *api.CallToolResult: The result of the tool execution
//   - error: Error if the tool doesn't exist or execution fails
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	return a.provider.ExecuteTool(ctx, toolName, args)
}

// Below are the MetaToolsHandler interface implementations.
// These methods will be implemented in issue #343 when integrating
// with the aggregator. For now, they return "not implemented" errors.

// ListTools returns all available tools for the current session.
// NOTE: This will be implemented in issue #343 (Core Integration).
func (a *Adapter) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	// This will be implemented in issue #343 by delegating to the aggregator
	// For now, return empty list to allow compilation
	logging.Debug("metatools", "ListTools called - awaiting integration in issue #343")
	return []mcp.Tool{}, nil
}

// CallTool executes a tool by name with the provided arguments.
// NOTE: This will be implemented in issue #343 (Core Integration).
func (a *Adapter) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	// This will be implemented in issue #343 by delegating to the aggregator
	logging.Debug("metatools", "CallTool called for %s - awaiting integration in issue #343", name)
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.TextContent{
			Type: "text",
			Text: "Meta-tools integration pending (issue #343)",
		}},
		IsError: true,
	}, nil
}

// ListResources returns all available resources for the current session.
// NOTE: This will be implemented in issue #343 (Core Integration).
func (a *Adapter) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	// This will be implemented in issue #343 by delegating to the aggregator
	logging.Debug("metatools", "ListResources called - awaiting integration in issue #343")
	return []mcp.Resource{}, nil
}

// GetResource retrieves the contents of a resource by URI.
// NOTE: This will be implemented in issue #343 (Core Integration).
func (a *Adapter) GetResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	// This will be implemented in issue #343 by delegating to the aggregator
	logging.Debug("metatools", "GetResource called for %s - awaiting integration in issue #343", uri)
	return &mcp.ReadResourceResult{
		Contents: []mcp.ResourceContents{},
	}, nil
}

// ListPrompts returns all available prompts for the current session.
// NOTE: This will be implemented in issue #343 (Core Integration).
func (a *Adapter) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	// This will be implemented in issue #343 by delegating to the aggregator
	logging.Debug("metatools", "ListPrompts called - awaiting integration in issue #343")
	return []mcp.Prompt{}, nil
}

// GetPrompt executes a prompt with the provided arguments.
// NOTE: This will be implemented in issue #343 (Core Integration).
func (a *Adapter) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	// This will be implemented in issue #343 by delegating to the aggregator
	logging.Debug("metatools", "GetPrompt called for %s - awaiting integration in issue #343", name)
	return &mcp.GetPromptResult{
		Messages: []mcp.PromptMessage{},
	}, nil
}
