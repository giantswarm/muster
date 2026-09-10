package api

import (
	"maps"

	"github.com/mark3labs/mcp-go/mcp"
)

// InputSchemaFromArgs converts a tool's argument metadata to the MCP input
// schema advertised for it. It is the one place the internal argument
// representation becomes JSON Schema, shared by every server that exposes a
// ToolProvider's tools over MCP: the aggregator for its meta-tools and core
// tools, and the local agent when it re-exposes the aggregator's meta-tools.
//
// An argument with a detailed Schema keeps it (the Description overrides the
// schema's own when set); otherwise the schema is derived from Type. A Default
// is copied into the property and Required arguments are listed as such.
func InputSchemaFromArgs(args []ArgMetadata) mcp.ToolInputSchema {
	properties := make(map[string]any)
	required := []string{}

	for _, arg := range args {
		var propSchema map[string]any

		if len(arg.Schema) > 0 {
			propSchema = make(map[string]any)
			maps.Copy(propSchema, arg.Schema)
			if arg.Description != "" {
				propSchema["description"] = arg.Description
			}
		} else {
			propSchema = map[string]any{
				"type":        arg.Type,
				"description": arg.Description,
			}
		}

		if arg.Default != nil {
			propSchema["default"] = arg.Default
		}

		properties[arg.Name] = propSchema

		if arg.Required {
			required = append(required, arg.Name)
		}
	}

	return mcp.ToolInputSchema{
		Type:       "object",
		Properties: properties,
		Required:   required,
	}
}
