package api

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// MetaToolsDataProvider provides data access for meta-tools.
// This interface is implemented by the aggregator and used by the metatools
// adapter to access tools, resources, and prompts from the aggregated servers.
//
// The interface is designed to support session-scoped visibility, where different
// sessions may see different tools based on their authentication state (OAuth).
// Context is used to pass session information for proper scoping.
type MetaToolsDataProvider interface {
	// ListToolsForContext returns all available tools for the current session context.
	// The returned tools are session-aware based on authentication state.
	//
	// Args:
	//   - ctx: Context containing session information
	//
	// Returns:
	//   - []mcp.Tool: List of available tools for the session
	ListToolsForContext(ctx context.Context) []mcp.Tool

	// CallToolInternal executes a tool by name with the provided arguments.
	// This bypasses the MCP protocol layer and calls tools directly.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - name: Name of the tool to execute
	//   - args: Arguments to pass to the tool
	//
	// Returns:
	//   - *mcp.CallToolResult: The result of the tool execution
	//   - error: Error if execution fails
	CallToolInternal(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error)

	// ListResourcesForContext returns all available resources for the current session context.
	// The returned resources are session-aware based on authentication state.
	//
	// Args:
	//   - ctx: Context containing session information
	//
	// Returns:
	//   - []ResourceOrigin: Available resources for the session, each tagged with its source server
	ListResourcesForContext(ctx context.Context) []ResourceOrigin

	// ReadResource retrieves the contents of a resource by URI.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - uri: URI of the resource to retrieve
	//
	// Returns:
	//   - *mcp.ReadResourceResult: The resource contents
	//   - error: Error if retrieval fails
	ReadResource(ctx context.Context, uri, serverName string) (*mcp.ReadResourceResult, error)

	// ListPromptsForContext returns all available prompts for the current session context.
	// The returned prompts are session-aware based on authentication state.
	//
	// Args:
	//   - ctx: Context containing session information
	//
	// Returns:
	//   - []PromptOrigin: Available prompts for the session, each tagged with its source server
	ListPromptsForContext(ctx context.Context) []PromptOrigin

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

	// ListServersRequiringAuth returns a list of servers that require authentication
	// for the current session. This enables the list_tools meta-tool to inform users
	// about servers that are available but require authentication before their tools
	// become visible.
	//
	// Args:
	//   - ctx: Context containing session information
	//
	// Returns:
	//   - []ServerAuthInfo: List of servers requiring authentication
	ListServersRequiringAuth(ctx context.Context) []ServerAuthInfo
}

// ServerAuthInfo contains information about a server requiring authentication.
// This is used to inform users which servers need authentication via core_auth_login.
type ServerAuthInfo struct {
	// Name is the server name (e.g., "kubernetes", "github")
	Name string `json:"name"`
	// Status is the current auth status (typically "auth_required")
	Status string `json:"status"`
	// AuthTool is the tool to use for authentication (typically "core_auth_login")
	AuthTool string `json:"auth_tool"`
}

// metaToolsDataProvider stores the registered MetaToolsDataProvider implementation.
var metaToolsDataProvider MetaToolsDataProvider

// RegisterMetaToolsDataProvider registers the data provider for meta-tools.
// This is typically the aggregator, which provides access to all tools,
// resources, and prompts from connected MCP servers.
//
// The registration is thread-safe and should be called during system initialization
// after the aggregator is created but before the metatools adapter is wired up.
//
// Args:
//   - p: MetaToolsDataProvider implementation (typically the aggregator)
//
// Thread-safe: Yes, protected by handlerMutex.
func RegisterMetaToolsDataProvider(p MetaToolsDataProvider) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	metaToolsDataProvider = p
}

// GetMetaToolsDataProvider returns the registered meta-tools data provider.
// This provides access to the underlying data layer (typically the aggregator)
// for listing and accessing tools, resources, and prompts.
//
// Returns nil if no provider has been registered yet.
//
// Returns:
//   - MetaToolsDataProvider: The registered provider, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
func GetMetaToolsDataProvider() MetaToolsDataProvider {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return metaToolsDataProvider
}

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
	ListResources(ctx context.Context) ([]ResourceOrigin, error)

	// GetResource retrieves the contents of a resource by URI.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - uri: URI of the resource to retrieve
	//
	// Returns:
	//   - *mcp.ReadResourceResult: The resource contents
	//   - error: Error if retrieval fails
	GetResource(ctx context.Context, uri, serverName string) (*mcp.ReadResourceResult, error)

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
	ListPrompts(ctx context.Context) ([]PromptOrigin, error)

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

	// Server status operations

	// ListServersRequiringAuth returns a list of servers that require authentication
	// for the current session. This enables the list_tools meta-tool to inform users
	// about servers that are available but require authentication.
	//
	// Args:
	//   - ctx: Context containing session information
	//
	// Returns:
	//   - []ServerAuthInfo: List of servers requiring authentication
	ListServersRequiringAuth(ctx context.Context) []ServerAuthInfo
}

// ResourceOrigin pairs an aggregated resource with the server exposing it.
//
// Tool and prompt names carry an "x_<server>_" prefix that identifies their
// origin, but resource URIs do not: ExposedResourceURI passes any URI with a
// scheme through unchanged, and MCP resource URIs always have one. Callers
// listing aggregated resources therefore cannot recover the source server from
// the URI alone, and two servers may legitimately expose the same URI. This
// type carries the attribution alongside the resource instead of encoding it
// into the identifier.
type ResourceOrigin struct {
	Resource mcp.Resource
	Server   string
}

// PromptOrigin pairs an aggregated prompt with the server exposing it.
//
// A prompt name is prefixed, but with the server's *configured* tool prefix
// (spec.toolPrefix) rather than its name -- a server named "gazelle-mcp-pro"
// with prefix "pro" exposes "x_pro_<name>". Deriving the source server by
// matching the server name against the exposed name therefore fails for every
// server whose prefix differs from its name, which is the common case. Carry
// the attribution explicitly instead, as ResourceOrigin does.
type PromptOrigin struct {
	Prompt mcp.Prompt
	Server string
}
