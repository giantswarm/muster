package cmd

const (
	resourceTypeService            = "service"
	resourceTypeServices           = "services"
	resourceTypeMCPServer          = "mcpserver"
	resourceTypeMCPServers         = "mcpservers"
	resourceTypeWorkflow           = "workflow"
	resourceTypeWorkflows          = "workflows"
	resourceTypeWorkflowExecution  = "workflow-execution"
	resourceTypeWorkflowExecutions = "workflow-executions"

	mcpPrimitiveTool      = "tool"
	mcpPrimitiveTools     = "tools"
	mcpPrimitiveResource  = "resource"
	mcpPrimitiveResources = "resources"
	mcpPrimitivePrompt    = "prompt"
	mcpPrimitivePrompts   = "prompts"
)

// mcpPrimitiveTypes maps MCP primitive type aliases (singular and plural)
// to their canonical names. Used by list and get commands to dispatch
// MCP primitives separately from core resources.
var mcpPrimitiveTypes = map[string]string{
	mcpPrimitiveTool:      mcpPrimitiveTool,
	mcpPrimitiveTools:     mcpPrimitiveTool,
	mcpPrimitiveResource:  mcpPrimitiveResource,
	mcpPrimitiveResources: mcpPrimitiveResource,
	mcpPrimitivePrompt:    mcpPrimitivePrompt,
	mcpPrimitivePrompts:   mcpPrimitivePrompt,
}
