package metatools

import (
	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
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
	// rankTerms keeps each tool's tokenised, folded text for filter_tools
	// queries.
	rankTerms *rankTermCache
}

// NewProvider creates a new meta-tools provider instance knowing the built-in
// toolset presets only. The provider is stateless except for the formatters,
// the preset registry and the cache of each tool's ranking terms, and can be
// safely used concurrently across multiple requests.
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
		rankTerms:  newRankTermCache(),
	}
}

// Presets returns the preset registry the provider resolves toolsets against.
func (p *Provider) Presets() *toolset.Registry {
	return p.presets
}

// readOnlyMetaTool annotates the meta-tools that only read. They are open
// world: every one reads the session's catalogue, and listing it first
// connects the session to the servers the person already granted
// (adoptSubjectGrants), which reaches those servers.
var readOnlyMetaTool = api.ReadOnlyAnnotations().OpenWorld(true)

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
		// Tools
		{
			Name: "list_tools",
			Description: toolDescription(
				"List one page of your tools as summaries, plus servers a sign-in would unlock; to find a tool use filter_tools.",
				"Costly: the catalogue can hold hundreds of tools and every page stays in your context, so do not page through it to find one. "+
					"describe_tool gives a tool's schema. truncated says more pages exist at offset + limit."),
			Annotations: readOnlyMetaTool,
			Args: []api.ArgMetadata{
				{
					Name:        ArgLimit,
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Maximum number of tools in this page",
					Default:     defaultListLimit,
				},
				{
					Name:        ArgOffset,
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Number of tools to skip before this page",
					Default:     0,
				},
			},
		},
		{
			Name:        "describe_tool",
			Description: toolDescription("Get one tool's full description, input schema, origin and annotations, and the call_tool call that runs it.", ""),
			Annotations: readOnlyMetaTool,
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
			Description: toolDescription("List muster's own core tools (core_*), apart from the tools of the MCP servers it aggregates.", ""),
			Annotations: readOnlyMetaTool,
			Args: []api.ArgMetadata{
				{
					Name:        "include_schema",
					Type:        api.ArgTypeBoolean,
					Required:    false,
					Description: "Whether to include full tool specifications with input schemas",
					Default:     true,
				},
			},
		},
		{
			Name: "filter_tools",
			Description: toolDescription(
				"Find tools by name pattern, description, labels or a ranked natural-language query, as a small summarised page.",
				"A tool you cannot find may sit behind a sign-in: servers_requiring_auth names each server whose tools the pattern could match, "+
					"and its auth_tool with that server's name starts the sign-in. describe_tool gives a chosen tool's full description and schema. "+
					"The response's toolset names the toolset bounding you: the toolset argument, else X-Muster-Toolset."),
			Annotations: readOnlyMetaTool,
			Args: []api.ArgMetadata{
				{
					Name:        "toolset",
					Type:        api.ArgTypeArray,
					Required:    false,
					Description: "Toolset selectors to resolve for you: preset:<name>, server:<name>, workflow:<name>, tool:<name>, at most 32; never wider than X-Muster-Toolset",
					Schema: map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
				{
					Name:        "include_presets",
					Type:        api.ArgTypeBoolean,
					Required:    false,
					Description: "Include the known toolset presets (name, description, built_in) in the response",
					Default:     false,
				},
				{
					Name:        "pattern",
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Glob on tool names (e.g. x_slack_*); it also picks the servers_requiring_auth entries whose tool prefix it could match",
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
					Description: "Natural-language query: matches come best-first with a score, a name match weighing most; tools that match no word are dropped",
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
					Description: "Whether pattern matching should be case-sensitive",
					Default:     false,
				},
				{
					Name:        "include_schema",
					Type:        api.ArgTypeBoolean,
					Required:    false,
					Description: "Whether to include full input schemas and full descriptions instead of one-line summaries",
					Default:     false,
				},
				{
					Name:        ArgLimit,
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Maximum number of tools in this page",
					Default:     defaultFilterLimit,
				},
				{
					Name:        ArgOffset,
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Number of matching tools to skip before this page",
					Default:     0,
				},
			},
		},
		{
			Name: "call_tool",
			Description: toolDescription(
				"Execute a catalogue tool by name: aggregated tools are callable only this way, never by their own name.",
				"Write name first, then arguments: a name written after a long arguments object gets lost in it. "+
					"A request that declares a toolset (X-Muster-Toolset) can only call tools inside it."),
			Annotations: api.WriteAnnotations(true, false).OpenWorld(true),
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        api.ArgTypeString,
					Required:    true,
					Description: "Name of the tool to call. Always set, and written before arguments.",
				},
				{
					Name:        "arguments",
					Type:        api.ArgTypeObject,
					Required:    false,
					Description: "Arguments to pass to the tool (as JSON object)",
				},
			},
		},

		// Resources
		{
			Name:        "list_resources",
			Description: toolDescription("List the resources of the connected MCP servers, each with the server exposing it.", ""),
			Annotations: readOnlyMetaTool,
			Args:        []api.ArgMetadata{},
		},
		{
			Name: "filter_resources",
			Description: toolDescription(
				"Filter the resources by URI pattern and/or source server, one page at a time.",
				"A URI with a scheme carries no server prefix, so scope to one server with server, not the URI."),
			Annotations: readOnlyMetaTool,
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
					Name:        ArgLimit,
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Maximum number of results to return per page",
				},
				{
					Name:        ArgOffset,
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Number of matches to skip before the returned page",
				},
			},
		},
		{
			Name:        "describe_resource",
			Description: toolDescription("Describe one resource by URI: its name, description, MIME type and server, not its contents.", ""),
			Annotations: readOnlyMetaTool,
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
			Description: toolDescription("Read the contents of one resource by URI.", ""),
			Annotations: readOnlyMetaTool,
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

		// Prompts
		{
			Name:        "list_prompts",
			Description: toolDescription("List the prompts of the connected MCP servers.", ""),
			Annotations: readOnlyMetaTool,
			Args:        []api.ArgMetadata{},
		},
		{
			Name:        "filter_prompts",
			Description: toolDescription("Filter the prompts by name pattern and/or source server, one page at a time; names are x_<server>_<name>.", ""),
			Annotations: readOnlyMetaTool,
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
					Name:        ArgLimit,
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Maximum number of results to return per page",
				},
				{
					Name:        ArgOffset,
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Number of matches to skip before the returned page",
				},
			},
		},
		{
			Name:        "describe_prompt",
			Description: toolDescription("Describe one prompt by name: its description and arguments.", ""),
			Annotations: readOnlyMetaTool,
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
			Description: toolDescription("Render one prompt by name with the given arguments.", ""),
			Annotations: readOnlyMetaTool,
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
