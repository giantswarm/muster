package metatools

import (
	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/toolset"
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
	// presets are the toolset presets a request's X-Muster-Toolset (or the
	// filter_tools toolset argument) may name: the built-ins plus the
	// installation's toolsetPresets configuration.
	presets *toolset.Registry
	// serverLabels resolves an MCPServer name to its resource labels for the
	// label: preset selector (#1168); nil when unavailable.
	serverLabels ServerLabelsSource
}

// NewProvider creates a new meta-tools provider instance knowing the built-in
// toolset presets only. The provider is stateless except for the formatters
// and the preset registry and can be safely used concurrently across multiple
// requests.
//
// Returns:
//   - *Provider: A new provider instance ready to handle meta-tool requests
func NewProvider() *Provider {
	return NewProviderWithPresets(toolset.BuiltIns())
}

// NewProviderWithPresets creates a meta-tools provider that resolves toolsets
// against the given preset registry (built-ins plus configured presets).
func NewProviderWithPresets(presets *toolset.Registry) *Provider {
	if presets == nil {
		presets = toolset.BuiltIns()
	}
	return &Provider{
		formatters: NewFormatters(),
		presets:    presets,
	}
}

// Presets returns the preset registry the provider resolves toolsets against.
func (p *Provider) Presets() *toolset.Registry {
	return p.presets
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
					Type:        api.ArgTypeString,
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
					Type:        api.ArgTypeBoolean,
					Required:    false,
					Description: "Whether to include full tool specifications with input schemas (default: true)",
					Default:     true,
				},
			},
		},
		{
			Name:        "filter_tools",
			Description: "Discover tools cheaply: filter by name pattern, description, or labels, optionally rank by a natural-language query, and get a bounded, summarised page. Full descriptions and schemas are omitted by default — use describe_tool for the authoritative detail of a chosen tool. Pass a toolset to see which tools a set of selectors resolves to for you, and include_presets to list the known toolset presets. The response's toolset field names the toolset the tools were resolved within — the argument, else the toolset declared for the request (X-Muster-Toolset) — so a scoped caller can learn what bounds it.",
			Args: []api.ArgMetadata{
				{
					Name:        "toolset",
					Type:        api.ArgTypeArray,
					Required:    false,
					Description: "Inline toolset selectors to resolve against your catalogue: preset:<name>, server:<name>, workflow:<name>, tool:<name> (exact names, at most 32). The response carries the tools they select, the selectors that matched nothing (toolset_unmatched) and the known presets. When the request also declares a toolset (X-Muster-Toolset), the argument resolves within it and never widens it.",
					Schema:      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				{
					Name:        "include_presets",
					Type:        api.ArgTypeBoolean,
					Required:    false,
					Description: "Include the known toolset presets (name, description, built_in) in the response (default: false)",
					Default:     false,
				},
				{
					Name:        "pattern",
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Pattern to match against tool names (supports wildcards like *)",
				},
				{
					Name:        "description_filter",
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Filter by description content (case-insensitive substring match)",
				},
				{
					Name:        "query",
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Natural-language query. When set, matching tools are relevance-ranked (lexical BM25 over name + summary) and returned best-first with a score; non-matching tools are dropped.",
				},
				{
					Name:        "labels",
					Type:        api.ArgTypeObject,
					Required:    false,
					Description: "Label facets to scope discovery, as key=value pairs. A tool must carry every given label to match (e.g. {\"category\": \"observability\"}).",
				},
				{
					Name:        "case_sensitive",
					Type:        api.ArgTypeBoolean,
					Required:    false,
					Description: "Whether pattern matching should be case-sensitive (default: false)",
					Default:     false,
				},
				{
					Name:        "include_schema",
					Type:        api.ArgTypeBoolean,
					Required:    false,
					Description: "Whether to include full input schemas and full descriptions instead of one-line summaries (default: false for cheap discovery)",
					Default:     false,
				},
				{
					Name:        "limit",
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Maximum number of tools to return in this page (default: 5). Increase to page through more matches.",
					Default:     defaultFilterLimit,
				},
				{
					Name:        "offset",
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Number of matching tools to skip before this page (default: 0)",
					Default:     0,
				},
			},
		},

		// Execution tool
		{
			Name:        "call_tool",
			Description: "Execute a tool with the given arguments. A request that declares a toolset (X-Muster-Toolset) can only call tools inside it; anything else is refused naming the toolset.",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        api.ArgTypeString,
					Required:    true,
					Description: "Name of the tool to call",
				},
				{
					Name:        "arguments",
					Type:        api.ArgTypeObject,
					Required:    false,
					Description: "Arguments to pass to the tool (as JSON object)",
				},
			},
		},

		// Resource tools
		{
			Name:        "list_resources",
			Description: "List all available resources from connected MCP servers, each tagged with the server exposing it",
			Args:        []api.ArgMetadata{},
		},
		{
			Name:        "filter_resources",
			Description: "Filter aggregated resources by URI pattern and/or source server. Resource URIs carrying a scheme are exposed unprefixed, so the \"server\" argument -- not the URI -- is the reliable way to scope to one server",
			Args: []api.ArgMetadata{
				{
					Name:        "pattern",
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Glob pattern to match against the resource URI (e.g. \"board://*\")",
				},
				{
					Name:        ArgServer,
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Only return resources exposed by this server",
				},
				{
					Name:        "case_sensitive",
					Type:        api.ArgTypeBoolean,
					Required:    false,
					Description: "Match case-sensitively (default false)",
				},
				{
					Name:        "limit",
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Maximum number of results to return per page",
				},
				{
					Name:        "offset",
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Number of matches to skip before the returned page",
				},
			},
		},
		{
			Name:        "describe_resource",
			Description: "Get detailed information about a specific resource",
			Args: []api.ArgMetadata{
				{
					Name:        "uri",
					Type:        api.ArgTypeString,
					Required:    true,
					Description: "URI of the resource to describe",
				},
				{
					Name:        ArgServer,
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Server to describe the resource from. Only needed when several servers expose the same URI; list_resources reports the server for every entry",
				},
			},
		},
		{
			Name:        "get_resource",
			Description: "Retrieve the contents of a resource",
			Args: []api.ArgMetadata{
				{
					Name:        "uri",
					Type:        api.ArgTypeString,
					Required:    true,
					Description: "URI of the resource to retrieve",
				},
				{
					Name:        ArgServer,
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Server to read from. Only needed when several servers expose the same URI; list_resources reports the server for every entry",
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
			Name:        "filter_prompts",
			Description: "Filter aggregated prompts by name pattern and/or source server. Prompt names are prefixed \"x_<server>_<name>\", so a pattern scopes to one server exactly as it does for tools",
			Args: []api.ArgMetadata{
				{
					Name:        "pattern",
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Glob pattern to match against the prompt name (e.g. \"x_myserver_*\")",
				},
				{
					Name:        ArgServer,
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Only return prompts exposed by this server",
				},
				{
					Name:        "case_sensitive",
					Type:        api.ArgTypeBoolean,
					Required:    false,
					Description: "Match case-sensitively (default false)",
				},
				{
					Name:        "limit",
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Maximum number of results to return per page",
				},
				{
					Name:        "offset",
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Number of matches to skip before the returned page",
				},
			},
		},
		{
			Name:        "describe_prompt",
			Description: "Get detailed information about a specific prompt",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        api.ArgTypeString,
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
					Type:        api.ArgTypeString,
					Required:    true,
					Description: "Name of the prompt to get",
				},
				{
					Name:        "arguments",
					Type:        api.ArgTypeObject,
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
