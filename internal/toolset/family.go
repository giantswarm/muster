package toolset

import (
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/apimachinery/pkg/runtime"
)

// SetFamilyMembers advertises servers as the members a family-grouped tool
// runs on: a required string parameter named instanceArg whose enum lists
// them, and a description trailer naming them. It replaces what a previous
// call advertised. Properties and Required are deep-copied — including
// nested object properties and array items — so the per-server cached tool
// schema is not mutated by callers that walk the result.
func SetFamilyMembers(tool *mcp.Tool, instanceArg string, servers []string) {
	enumVals := make([]any, len(servers))
	for i, s := range servers {
		enumVals[i] = s
	}

	properties := runtime.DeepCopyJSON(tool.InputSchema.Properties)
	if properties == nil {
		properties = make(map[string]any, 1)
	}
	properties[instanceArg] = map[string]any{
		"type":        "string",
		"description": "Target instance to execute this tool on. Available: " + strings.Join(servers, ", "),
		"enum":        enumVals,
	}

	required := make([]string, 0, len(tool.InputSchema.Required)+1)
	required = append(required, tool.InputSchema.Required...)
	if !slices.Contains(required, instanceArg) {
		required = append(required, instanceArg)
	}

	tool.InputSchema.Properties = properties
	tool.InputSchema.Required = required

	// A single trailer is kept across repeated calls by treating an existing
	// parenthesised "available on servers" suffix as canonical.
	description := tool.Description
	if idx := strings.LastIndex(description, " (available on servers:"); idx >= 0 {
		description = description[:idx]
	}
	tool.Description = description + " (available on servers: " + strings.Join(servers, ", ") + ")"
}
