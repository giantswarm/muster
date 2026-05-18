package cmd

import "github.com/giantswarm/muster/internal/api"

// Local aliases for resource-type identifiers so the existing cmd/*
// switch statements and slice literals stay terse. Source of truth lives
// in internal/api so internal/cli can reference the same identifiers.
const (
	resourceTypeService            = api.ResourceTypeService
	resourceTypeServices           = api.ResourceTypeServices
	resourceTypeMCPServer          = api.ResourceTypeMCPServer
	resourceTypeMCPServers         = api.ResourceTypeMCPServers
	resourceTypeWorkflow           = api.ResourceTypeWorkflow
	resourceTypeWorkflows          = api.ResourceTypeWorkflows
	resourceTypeWorkflowExecution  = api.ResourceTypeWorkflowExecution
	resourceTypeWorkflowExecutions = api.ResourceTypeWorkflowExecutions

	mcpPrimitiveTool      = api.MCPPrimitiveTool
	mcpPrimitiveTools     = api.MCPPrimitiveTools
	mcpPrimitiveResource  = api.MCPPrimitiveResource
	mcpPrimitiveResources = api.MCPPrimitiveResources
	mcpPrimitivePrompt    = api.MCPPrimitivePrompt
	mcpPrimitivePrompts   = api.MCPPrimitivePrompts
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
