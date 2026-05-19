package cmd

import "github.com/giantswarm/muster/internal/api"

// mcpPrimitiveTypes maps MCP primitive type aliases (singular and plural)
// to their canonical singular form. Used by list and get commands to
// dispatch MCP primitives separately from core resources.
var mcpPrimitiveTypes = map[string]string{
	api.MCPPrimitiveTool:      api.MCPPrimitiveTool,
	api.MCPPrimitiveTools:     api.MCPPrimitiveTool,
	api.MCPPrimitiveResource:  api.MCPPrimitiveResource,
	api.MCPPrimitiveResources: api.MCPPrimitiveResource,
	api.MCPPrimitivePrompt:    api.MCPPrimitivePrompt,
	api.MCPPrimitivePrompts:   api.MCPPrimitivePrompt,
}
