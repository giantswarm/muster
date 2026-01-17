package cmd

// mcpPrimitiveTypes maps MCP primitive type aliases to their canonical names.
// Used by both list and get commands for consistent handling of MCP primitives
// (tools, resources, prompts) which are handled differently from core resources.
var mcpPrimitiveTypes = map[string]string{
	"tool":      "tool",
	"tools":     "tool",
	"resource":  "resource",
	"resources": "resource",
	"prompt":    "prompt",
	"prompts":   "prompt",
}

// IsMCPPrimitiveType checks if a resource type is an MCP primitive.
// Returns the canonical type name and true if it's an MCP primitive.
func IsMCPPrimitiveType(resourceType string) (string, bool) {
	mcpType, ok := mcpPrimitiveTypes[resourceType]
	return mcpType, ok
}
