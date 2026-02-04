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

// ToolInfo represents basic tool information returned by list_tools and filter_tools.
type ToolInfo struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema,omitempty"`
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
type FilterToolsResponse struct {
	Filters       FilterCriteria `json:"filters"`
	TotalTools    int            `json:"total_tools"`
	FilteredCount int            `json:"filtered_count"`
	Tools         []ToolInfo     `json:"tools"`
}

// FilterCriteria describes the filter parameters applied.
type FilterCriteria struct {
	Pattern           string `json:"pattern,omitempty"`
	DescriptionFilter string `json:"description_filter,omitempty"`
	CaseSensitive     bool   `json:"case_sensitive"`
	IncludeSchema     bool   `json:"include_schema"`
}

// DescribeToolResponse is the response structure from the describe_tool meta-tool.
type DescribeToolResponse struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema,omitempty"`
}
