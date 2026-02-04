package metatools

import (
	"github.com/giantswarm/muster/internal/api"
)

// Provider implements the api.ToolProvider interface for meta-tools.
// It provides the discovery and execution tools that AI assistants use
// to interact with the muster aggregator's tool ecosystem.
//
// The Provider is session-aware and uses the context session ID for
// tool visibility when appropriate. It retrieves tools, resources, and
// prompts through the API layer's service locator pattern.
type Provider struct {
	formatters *Formatters
}

// NewProvider creates a new meta-tools provider instance.
// The provider is stateless except for the formatters and can be safely
// used concurrently across multiple requests.
//
// Returns:
//   - *Provider: A new provider instance ready to handle meta-tool requests
func NewProvider() *Provider {
	return &Provider{
		formatters: NewFormatters(),
	}
}

// GetTools returns metadata for all meta-tools this provider offers.
// This implements the api.ToolProvider interface for tool discovery.
//
// The meta-tools provide AI assistants with the ability to:
//   - Discover available tools, resources, and prompts
//   - Get detailed information about specific primitives
//   - Execute tools and retrieve resources/prompts
//   - Filter and search the tool catalog
//
// Returns:
//   - []api.ToolMetadata: List of all meta-tools provided
func (p *Provider) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		// Discovery tools
		{
			Name:        "list_tools",
			Description: "List all available tools from connected MCP servers",
			Args:        []api.ArgMetadata{},
		},
		{
			Name:        "describe_tool",
			Description: "Get detailed information about a specific tool including its input schema",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the tool to describe",
				},
			},
		},
		{
			Name:        "list_core_tools",
			Description: "List core muster tools (built-in functionality separate from external MCP servers)",
			Args: []api.ArgMetadata{
				{
					Name:        "include_schema",
					Type:        "boolean",
					Required:    false,
					Description: "Whether to include full tool specifications with input schemas (default: true)",
					Default:     true,
				},
			},
		},
		{
			Name:        "filter_tools",
			Description: "Filter available tools based on name patterns or descriptions with full specifications",
			Args: []api.ArgMetadata{
				{
					Name:        "pattern",
					Type:        "string",
					Required:    false,
					Description: "Pattern to match against tool names (supports wildcards like *)",
				},
				{
					Name:        "description_filter",
					Type:        "string",
					Required:    false,
					Description: "Filter by description content (case-insensitive substring match)",
				},
				{
					Name:        "case_sensitive",
					Type:        "boolean",
					Required:    false,
					Description: "Whether pattern matching should be case-sensitive (default: false)",
					Default:     false,
				},
				{
					Name:        "include_schema",
					Type:        "boolean",
					Required:    false,
					Description: "Whether to include full tool specifications with input schemas (default: true)",
					Default:     true,
				},
			},
		},

		// Execution tool
		{
			Name:        "call_tool",
			Description: "Execute a tool with the given arguments",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the tool to call",
				},
				{
					Name:        "arguments",
					Type:        "object",
					Required:    false,
					Description: "Arguments to pass to the tool (as JSON object)",
				},
			},
		},

		// Resource tools
		{
			Name:        "list_resources",
			Description: "List all available resources from connected MCP servers",
			Args:        []api.ArgMetadata{},
		},
		{
			Name:        "describe_resource",
			Description: "Get detailed information about a specific resource",
			Args: []api.ArgMetadata{
				{
					Name:        "uri",
					Type:        "string",
					Required:    true,
					Description: "URI of the resource to describe",
				},
			},
		},
		{
			Name:        "get_resource",
			Description: "Retrieve the contents of a resource",
			Args: []api.ArgMetadata{
				{
					Name:        "uri",
					Type:        "string",
					Required:    true,
					Description: "URI of the resource to retrieve",
				},
			},
		},

		// Prompt tools
		{
			Name:        "list_prompts",
			Description: "List all available prompts from connected MCP servers",
			Args:        []api.ArgMetadata{},
		},
		{
			Name:        "describe_prompt",
			Description: "Get detailed information about a specific prompt",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the prompt to describe",
				},
			},
		},
		{
			Name:        "get_prompt",
			Description: "Get a prompt with the given arguments",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the prompt to get",
				},
				{
					Name:        "arguments",
					Type:        "object",
					Required:    false,
					Description: "Arguments to pass to the prompt (as JSON object with string values)",
				},
			},
		},
	}
}

// GetFormatters returns the formatters instance used by this provider.
// This allows handlers to access formatting utilities without creating
// new instances.
//
// Returns:
//   - *Formatters: The formatters instance
func (p *Provider) GetFormatters() *Formatters {
	return p.formatters
}
