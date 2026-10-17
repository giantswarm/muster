package toolset

import (
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/apimachinery/pkg/runtime"
)

// familyTrailerPrefix opens the description trailer SetFamilyMembers writes;
// FamilyMembers reads it back.
const familyTrailerPrefix = " (available on servers: "

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
	if idx := strings.LastIndex(description, strings.TrimSuffix(familyTrailerPrefix, " ")); idx >= 0 {
		description = description[:idx]
	}
	tool.Description = description + familyTrailerPrefix + strings.Join(servers, ", ") + ")"
}

// FamilyMembers returns the members a family-grouped tool's description
// trailer names, in trailer order, or nil when the description carries no
// trailer.
func FamilyMembers(description string) []string {
	idx := strings.LastIndex(description, familyTrailerPrefix)
	if idx < 0 {
		return nil
	}
	list, ok := strings.CutSuffix(description[idx+len(familyTrailerPrefix):], ")")
	if !ok || list == "" {
		return nil
	}
	return strings.Split(list, ", ")
}
