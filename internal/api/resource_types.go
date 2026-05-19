package api

// Resource-type identifiers used by the muster CLI and exposed as keys in
// list/get JSON responses. Each kind has a singular form (CLI argument,
// detail-response key) and a plural form (list-response wrapper key).
const (
	ResourceTypeService            = "service"
	ResourceTypeServices           = "services"
	ResourceTypeMCPServer          = "mcpserver"
	ResourceTypeMCPServers         = "mcpservers"
	ResourceTypeWorkflow           = "workflow"
	ResourceTypeWorkflows          = "workflows"
	ResourceTypeWorkflowExecution  = "workflow-execution"
	ResourceTypeWorkflowExecutions = "workflow-executions"

	// Response-only wrapper keys. These match the JSON shape emitted by the
	// muster API; they differ from the singular CLI forms above (e.g. the
	// API returns "mcpServers" camelCase, while the CLI accepts "mcpserver").
	ResponseKeyMCPServer      = "mcpServer"
	ResponseKeyMCPServers     = "mcpServers"
	ResponseKeyServiceClass   = "serviceClass"
	ResponseKeyServiceClasses = "serviceClasses"
	ResponseKeyExecutions     = "executions"

	// MCP primitive aliases recognised at the CLI surface. Singular and
	// plural both resolve to the singular canonical form.
	MCPPrimitiveTool      = "tool"
	MCPPrimitiveTools     = "tools"
	MCPPrimitiveResource  = "resource"
	MCPPrimitiveResources = "resources"
	MCPPrimitivePrompt    = "prompt"
	MCPPrimitivePrompts   = "prompts"
)
