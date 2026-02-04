package metatools

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"

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
// These methods delegate to the MetaToolsDataProvider (typically the aggregator)
// to access tools, resources, and prompts.

// getDataProvider retrieves the data provider from the API layer.
// Returns an error if no data provider is registered.
func (a *Adapter) getDataProvider() (api.MetaToolsDataProvider, error) {
	provider := api.GetMetaToolsDataProvider()
	if provider == nil {
		return nil, fmt.Errorf("meta-tools data provider not available")
	}
	return provider, nil
}

// ListTools returns all available tools for the current session.
// This delegates to the data provider (aggregator) which handles session-scoped
// tool visibility for OAuth-protected servers.
func (a *Adapter) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	provider, err := a.getDataProvider()
	if err != nil {
		logging.Warn("metatools", "ListTools: %v", err)
		return []mcp.Tool{}, nil
	}

	tools := provider.ListToolsForContext(ctx)
	logging.Debug("metatools", "ListTools: returning %d tools", len(tools))
	return tools, nil
}

// CallTool executes a tool by name with the provided arguments.
// This delegates to the data provider (aggregator) for tool execution.
func (a *Adapter) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	provider, err := a.getDataProvider()
	if err != nil {
		logging.Warn("metatools", "CallTool: %v", err)
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Meta-tools data provider not available: %v", err),
			}},
			IsError: true,
		}, nil
	}

	logging.Debug("metatools", "CallTool: calling tool %s with args %v", name, args)
	result, err := provider.CallToolInternal(ctx, name, args)
	if err != nil {
		logging.Error("metatools", err, "CallTool failed for %s", name)
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Tool execution failed: %v", err),
			}},
			IsError: true,
		}, nil
	}

	return result, nil
}

// ListResources returns all available resources for the current session.
// This delegates to the data provider (aggregator) which handles session-scoped
// resource visibility for OAuth-protected servers.
func (a *Adapter) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	provider, err := a.getDataProvider()
	if err != nil {
		logging.Warn("metatools", "ListResources: %v", err)
		return []mcp.Resource{}, nil
	}

	resources := provider.ListResourcesForContext(ctx)
	logging.Debug("metatools", "ListResources: returning %d resources", len(resources))
	return resources, nil
}

// GetResource retrieves the contents of a resource by URI.
// This delegates to the data provider (aggregator) for resource access.
func (a *Adapter) GetResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	provider, err := a.getDataProvider()
	if err != nil {
		logging.Warn("metatools", "GetResource: %v", err)
		return &mcp.ReadResourceResult{
			Contents: []mcp.ResourceContents{},
		}, nil
	}

	logging.Debug("metatools", "GetResource: reading resource %s", uri)
	result, err := provider.ReadResource(ctx, uri)
	if err != nil {
		logging.Error("metatools", err, "GetResource failed for %s", uri)
		return &mcp.ReadResourceResult{
			Contents: []mcp.ResourceContents{},
		}, nil
	}

	return result, nil
}

// ListPrompts returns all available prompts for the current session.
// This delegates to the data provider (aggregator) which handles session-scoped
// prompt visibility for OAuth-protected servers.
func (a *Adapter) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	provider, err := a.getDataProvider()
	if err != nil {
		logging.Warn("metatools", "ListPrompts: %v", err)
		return []mcp.Prompt{}, nil
	}

	prompts := provider.ListPromptsForContext(ctx)
	logging.Debug("metatools", "ListPrompts: returning %d prompts", len(prompts))
	return prompts, nil
}

// GetPrompt executes a prompt with the provided arguments.
// This delegates to the data provider (aggregator) for prompt execution.
func (a *Adapter) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	provider, err := a.getDataProvider()
	if err != nil {
		logging.Warn("metatools", "GetPrompt: %v", err)
		return &mcp.GetPromptResult{
			Messages: []mcp.PromptMessage{},
		}, nil
	}

	logging.Debug("metatools", "GetPrompt: getting prompt %s with args %v", name, args)
	result, err := provider.GetPrompt(ctx, name, args)
	if err != nil {
		logging.Error("metatools", err, "GetPrompt failed for %s", name)
		return &mcp.GetPromptResult{
			Messages: []mcp.PromptMessage{},
		}, nil
	}

	return result, nil
}

// ListServersRequiringAuth returns a list of servers that require authentication
// for the current session. This is used by the list_tools handler to inform
// users about available servers that need authentication.
func (a *Adapter) ListServersRequiringAuth(ctx context.Context) []api.ServerAuthInfo {
	provider, err := a.getDataProvider()
	if err != nil {
		logging.Warn("metatools", "ListServersRequiringAuth: %v", err)
		return []api.ServerAuthInfo{}
	}

	return provider.ListServersRequiringAuth(ctx)
}
