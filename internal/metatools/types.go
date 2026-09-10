package metatools

import (
	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/toolset"

	"github.com/mark3labs/mcp-go/mcp"
)

// Meta-tool name constants.
// These are the meta-tools exposed by the aggregator that wrap actual tool access.
const (
	// ToolListTools lists all available tools for the session.
	ToolListTools = "list_tools"

	// ToolDescribeTool gets detailed schema for a specific tool.
	ToolDescribeTool = "describe_tool"

	// ToolFilterTools searches/filters tools by pattern.
	ToolFilterTools = "filter_tools"

	// ToolListCoreTools lists only Muster core tools.
	ToolListCoreTools = "list_core_tools"

	// ToolCallTool executes any tool by name.
	ToolCallTool = "call_tool"

	// ToolListResources lists available MCP resources.
	ToolListResources = "list_resources"

	// ToolFilterResources filters resources by source server and URI pattern.
	ToolFilterResources = "filter_resources"

	// ToolDescribeResource gets resource metadata.
	ToolDescribeResource = "describe_resource"

	// ToolGetResource reads resource contents.
	ToolGetResource = "get_resource"

	// ToolListPrompts lists available prompts.
	ToolListPrompts = "list_prompts"

	// ToolFilterPrompts filters prompts by source server and name pattern.
	ToolFilterPrompts = "filter_prompts"

	// ToolDescribePrompt gets prompt details.
	ToolDescribePrompt = "describe_prompt"

	// ToolGetPrompt executes a prompt.
	ToolGetPrompt = "get_prompt"
)

// ItemKind identifies the kind of MCP capability an aggregator entry represents.
type ItemKind string

const (
	ItemKindTool     ItemKind = "tool"
	ItemKindResource ItemKind = "resource"
	ItemKindPrompt   ItemKind = "prompt"
)

// String returns the canonical lowercase token for the item kind.
func (k ItemKind) String() string { return string(k) }

// ToolInfo represents basic tool information returned by list_tools and filter_tools.
//
// In discovery mode filter_tools populates Summary (a one-line, length-capped
// excerpt) and omits the full Description and InputSchema to keep the payload
// cheap; the authoritative full text and schema remain available via
// describe_tool. Score is set only when results were relevance-ranked by a
// query, and Labels are included only when the tool carries discovery facets.
type ToolInfo struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Score       float64           `json:"score,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	// Server is the MCPServer (or family) the tool comes from; omitted for
	// workflows and core tools.
	Server string `json:"server,omitempty"`
	// Kind is "tool" (served by an MCPServer), "workflow" or "core".
	Kind string `json:"kind,omitempty"`
	// Annotations are the tool's MCP annotations as its server declared them
	// (for a workflow, the derived readOnlyHint); omitted when it has none.
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
	InputSchema interface{}      `json:"inputSchema,omitempty"`
}

// Text returns the human-readable line for the tool: the full description
// when the entry carries one, else the discovery tier's one-line summary.
func (t ToolInfo) Text() string {
	if t.Description != "" {
		return t.Description
	}
	return t.Summary
}

// ToolAnnotations are the MCP tool annotations muster forwards from the
// downstream server, plus the derived read-only hint of a workflow whose step
// tools are all read-only. Only the hints the server set are present.
type ToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}

// annotationsOf projects a tool's MCP annotations; nil when it carries none.
func annotationsOf(tool mcp.Tool) *ToolAnnotations {
	a := tool.Annotations
	if a.ReadOnlyHint == nil && a.DestructiveHint == nil && a.IdempotentHint == nil && a.OpenWorldHint == nil {
		return nil
	}
	return &ToolAnnotations{
		ReadOnlyHint:    a.ReadOnlyHint,
		DestructiveHint: a.DestructiveHint,
		IdempotentHint:  a.IdempotentHint,
		OpenWorldHint:   a.OpenWorldHint,
	}
}

// originOf projects the origin the aggregator recorded on a tool into the
// server and kind fields the meta-tools report.
func originOf(tool mcp.Tool) (server, kind string) {
	kind = string(toolset.KindOf(tool))
	if origin, ok := toolset.ToolOriginOf(tool); ok {
		server = origin.Server
	}
	return server, kind
}

// ListToolsResponse is the response structure from the list_tools meta-tool:
// one page of the caller's catalogue in the discovery tier's shape (summarised
// entries, limit/offset echoed in Filters, Total and Truncated for paging),
// plus the servers a sign-in would unlock. ServersRequiringAuth is neither
// paged nor narrowed by a toolset.
type ListToolsResponse struct {
	FilterToolsResponse
	ServersRequiringAuth []api.ServerAuthInfo `json:"servers_requiring_auth,omitempty"`
}

// FilterToolsResponse is the response structure from the filter_tools meta-tool.
//
// Total is the number of tools matching the filters across the whole catalogue;
// Tools holds only the current page (bounded by limit/offset). Truncated is true
// when more matches exist beyond the returned page, signalling the client to
// refine the query or page further. TotalTools and FilteredCount are retained
// for backward compatibility: TotalTools is the size of the full catalogue and
// FilteredCount is the number of tools returned in this page.
//
// Note the FilteredCount semantics changed with the discovery tier: before
// pagination it meant "tools matching the filters" (now carried by Total), and
// it now equals len(Tools), i.e. the page size capped by limit. New callers
// should prefer Total for the match count and Truncated to detect more pages.
type FilterToolsResponse struct {
	Filters    FilterCriteria `json:"filters"`
	TotalTools int            `json:"total_tools"`
	// FilteredCount equals len(Tools) (the current page size). Deprecated: use
	// Total for the number of matches across the catalogue.
	FilteredCount int        `json:"filtered_count"`
	Total         int        `json:"total"`
	Truncated     bool       `json:"truncated"`
	Tools         []ToolInfo `json:"tools"`
	// Toolset echoes the selectors of the toolset the tools above were
	// resolved within: the toolset argument when one was given, else the
	// request's X-Muster-Toolset. Absent when the request is unscoped.
	Toolset []string `json:"toolset,omitempty"`
	// ToolsetUnmatched lists the selectors of that toolset that select no tool
	// for the caller.
	ToolsetUnmatched []string `json:"toolset_unmatched,omitempty"`
	// Presets lists the known toolset presets when include_presets is set or a
	// toolset argument was given; a request-declared toolset alone does not
	// add them.
	Presets []toolset.Info `json:"presets,omitempty"`
}

// FilterCriteria describes the filter parameters applied.
type FilterCriteria struct {
	Pattern           string            `json:"pattern,omitempty"`
	DescriptionFilter string            `json:"description_filter,omitempty"`
	Query             string            `json:"query,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	CaseSensitive     bool              `json:"case_sensitive"`
	IncludeSchema     bool              `json:"include_schema"`
	Limit             int               `json:"limit"`
	Offset            int               `json:"offset"`
}

// MetaToolNames is the canonical set of meta-tools the aggregator exposes
// directly over MCP, as opposed to aggregated tools reached through call_tool.
//
// Clients that route on this distinction must derive their list from here.
// filter_resources and filter_prompts were added to the provider without
// updating the agent's hand-maintained copy, so the CLI reported "tool not
// found" for tools the server was advertising -- a mismatch nothing could
// catch, because the copy was a map literal rather than a type.
var MetaToolNames = []string{
	ToolListTools,
	ToolDescribeTool,
	ToolFilterTools,
	ToolListCoreTools,
	ToolCallTool,
	ToolListResources,
	ToolFilterResources,
	ToolDescribeResource,
	ToolGetResource,
	ToolListPrompts,
	ToolFilterPrompts,
	ToolDescribePrompt,
	ToolGetPrompt,
}

// IsMetaTool reports whether name is one of the aggregator's meta-tools.
func IsMetaTool(name string) bool {
	for _, n := range MetaToolNames {
		if n == name {
			return true
		}
	}
	return false
}

// ArgServer is the argument name selecting which MCP server a resource or
// prompt request applies to, and the response field naming the server an
// aggregated capability came from. Resource URIs carrying a scheme are exposed
// unprefixed, so this is the only way to express "this server's resources".
const ArgServer = "server"

// ArgLimit and ArgOffset are the paging arguments every listing meta-tool
// shares: the maximum number of entries in the returned page and the number
// of matches to skip before it.
const (
	ArgLimit  = "limit"
	ArgOffset = "offset"
)

// ResourceInfo is one entry in a filter_resources response.
//
// Server is always populated: resource URIs carrying a scheme are exposed
// unprefixed, so unlike a tool or prompt name the URI does not identify where
// the resource came from, and two servers can expose the same one.
type ResourceInfo struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
	Server      string `json:"server"`
}

// FilterResourcesResponse is the response structure from the filter_resources
// meta-tool, mirroring FilterToolsResponse.
type FilterResourcesResponse struct {
	Filters        CapabilityFilterCriteria `json:"filters"`
	TotalResources int                      `json:"total_resources"`
	FilteredCount  int                      `json:"filtered_count"`
	Total          int                      `json:"total"`
	Truncated      bool                     `json:"truncated"`
	Resources      []ResourceInfo           `json:"resources"`
}

// PromptInfo is one entry in a filter_prompts response.
//
// Server carries the source server explicitly. The exposed name is prefixed
// with the server's *configured* tool prefix rather than its name, so the name
// alone does not identify the server.
type PromptInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Server      string `json:"server"`
}

// FilterPromptsResponse is the response structure from the filter_prompts
// meta-tool, mirroring FilterToolsResponse.
type FilterPromptsResponse struct {
	Filters       CapabilityFilterCriteria `json:"filters"`
	TotalPrompts  int                      `json:"total_prompts"`
	FilteredCount int                      `json:"filtered_count"`
	Total         int                      `json:"total"`
	Truncated     bool                     `json:"truncated"`
	Prompts       []PromptInfo             `json:"prompts"`
}

// CapabilityFilterCriteria describes the filter parameters applied by
// filter_resources and filter_prompts. It is deliberately narrower than
// FilterCriteria: neither relevance ranking nor label facets apply to
// resources or prompts.
type CapabilityFilterCriteria struct {
	Pattern       string `json:"pattern,omitempty"`
	Server        string `json:"server,omitempty"`
	CaseSensitive bool   `json:"case_sensitive"`
	Limit         int    `json:"limit"`
	Offset        int    `json:"offset"`
}

// DescribeToolResponse is the response structure from the describe_tool meta-tool.
type DescribeToolResponse struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Server      string           `json:"server,omitempty"`
	Kind        string           `json:"kind,omitempty"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
	InputSchema interface{}      `json:"inputSchema,omitempty"`
}
