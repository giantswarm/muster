package metatools

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

	// ToolDescribeResource gets resource metadata.
	ToolDescribeResource = "describe_resource"

	// ToolGetResource reads resource contents.
	ToolGetResource = "get_resource"

	// ToolListPrompts lists available prompts.
	ToolListPrompts = "list_prompts"

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
	InputSchema interface{}       `json:"inputSchema,omitempty"`
}

// ListToolsResponse is the response structure from the list_tools meta-tool.
type ListToolsResponse struct {
	Tools                []ToolInfo            `json:"tools"`
	ServersRequiringAuth []ServerRequiringAuth `json:"servers_requiring_auth,omitempty"`
}

// ServerRequiringAuth describes an MCP server that requires authentication.
type ServerRequiringAuth struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	AuthTool string `json:"auth_tool"`
}

// FilterToolsResponse is the response structure from the filter_tools meta-tool.
//
// Total is the number of tools matching the filters across the whole catalogue;
// Tools holds only the current page (bounded by limit/offset). Truncated is true
// when more matches exist beyond the returned page, signalling the client to
// refine the query or page further. TotalTools and FilteredCount are retained
// for backward compatibility: TotalTools is the size of the full catalogue and
// FilteredCount is the number of tools returned in this page.
type FilterToolsResponse struct {
	Filters       FilterCriteria `json:"filters"`
	TotalTools    int            `json:"total_tools"`
	FilteredCount int            `json:"filtered_count"`
	Total         int            `json:"total"`
	Truncated     bool           `json:"truncated"`
	Tools         []ToolInfo     `json:"tools"`
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

// DescribeToolResponse is the response structure from the describe_tool meta-tool.
type DescribeToolResponse struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema,omitempty"`
}
