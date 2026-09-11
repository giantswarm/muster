package metatools

import (
	"encoding/json"
	"fmt"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
)

// Formatters provides utilities for formatting MCP data consistently.
// It supports structured JSON responses for tools, resources, and prompts.
// The formatters ensure consistent presentation across different output modes.
//
// Key features:
//   - JSON formatting for structured data consumption
//   - Search and lookup utilities for cached data
//   - Consistent error handling and fallback formatting
type Formatters struct{}

// NewFormatters creates a new formatters instance.
// The formatters instance is stateless and can be safely used concurrently.
func NewFormatters() *Formatters {
	return &Formatters{}
}

// FormatResourcesListJSON formats a list of resources as structured JSON.
// This format is used for programmatic consumption, MCP server responses,
// and integration with external tools that expect structured data.
//
// Args:
//   - resources: Slice of resources to format
//
// Returns:
//   - JSON string containing array of resource objects with URI, name, description, MIME type, and source server
//   - error: JSON marshaling errors (should be rare)
//
// If no resources are available, returns a simple message string.
func (f *Formatters) FormatResourcesListJSON(resources []api.ResourceOrigin) (string, error) {
	if len(resources) == 0 {
		return "No resources available", nil
	}

	type ResourceInfo struct {
		URI         string `json:"uri"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		MIMEType    string `json:"mimeType,omitempty"`
		Server      string `json:"server,omitempty"`
	}

	resourceList := make([]ResourceInfo, len(resources))
	for i, origin := range resources {
		resource := origin.Resource
		desc := resource.Description
		if desc == "" {
			desc = resource.Name
		}
		resourceList[i] = ResourceInfo{
			URI:         resource.URI,
			Name:        resource.Name,
			Description: desc,
			MIMEType:    resource.MIMEType,
			Server:      origin.Server,
		}
	}

	jsonData, err := json.MarshalIndent(resourceList, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format resources: %w", err)
	}

	return string(jsonData), nil
}

// FormatPromptsListJSON formats a list of prompts as structured JSON.
// This format is used for programmatic consumption, MCP server responses,
// and integration with external tools that expect structured data.
//
// Args:
//   - prompts: Slice of prompts to format
//
// Returns:
//   - JSON string containing array of prompt objects with name and description
//   - error: JSON marshaling errors (should be rare)
//
// If no prompts are available, returns a simple message string.
func (f *Formatters) FormatPromptsListJSON(prompts []api.PromptOrigin) (string, error) {
	if len(prompts) == 0 {
		return "No prompts available", nil
	}

	type promptListEntry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Server      string `json:"server"`
	}

	promptList := make([]promptListEntry, len(prompts))
	for i, origin := range prompts {
		promptList[i] = promptListEntry{
			Name:        origin.Prompt.Name,
			Description: origin.Prompt.Description,
			Server:      origin.Server,
		}
	}

	jsonData, err := json.MarshalIndent(promptList, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format prompts: %w", err)
	}

	return string(jsonData), nil
}

// FieldInvocation is the key under which describe_tool states how the
// described tool is invoked.
const FieldInvocation = "invocation"

// invocationNote tells the caller how to reach the described tool. Every tool
// describe_tool can describe is an aggregated tool inside muster: the
// aggregator advertises only the meta-tools over MCP, so a caller that issues
// the tool's own name as a tool call gets an error it cannot act on. Saying so
// next to the schema puts the rule where the caller reads just before calling.
func invocationNote(name string) string {
	return fmt.Sprintf(
		"Call it through the %s meta-tool: %s{\"name\": %q, \"arguments\": <object matching inputSchema>}. "+
			"Tools inside muster are not callable by name directly — only the meta-tools are.",
		ToolCallTool, ToolCallTool, name)
}

// FormatToolDetailJSON formats detailed tool information as structured JSON.
// This format includes the complete tool schema and is used for programmatic
// consumption and tool introspection.
//
// Args:
//   - tool: The tool to format detailed information for
//
// Returns:
//   - JSON string containing complete tool information including schema
//   - error: JSON marshaling errors (should be rare)
//
// Output format:
//
//	{
//	  api.FieldName: "tool_name",
//	  api.SchemaKeyDescription: "Tool description",
//	  api.FieldInputSchema: { ... },
//	  "invocation": "Call it through the call_tool meta-tool: ..."
//	}
func (f *Formatters) FormatToolDetailJSON(tool mcp.Tool) (string, error) {
	toolInfo := map[string]interface{}{
		api.FieldName:            tool.Name,
		api.SchemaKeyDescription: tool.Description,
		api.FieldInputSchema:     tool.InputSchema,
		FieldInvocation:          invocationNote(tool.Name),
	}
	// Origin and annotations: where the tool comes from, what kind it is and
	// the hints its server declared (or, for a workflow, the derived read-only
	// hint), so a client can tell read-only tools apart without calling them.
	server, kind := originOf(tool)
	if server != "" {
		toolInfo[api.FieldServer] = server
	}
	toolInfo["kind"] = kind
	if annotations := annotationsOf(tool); annotations != nil {
		toolInfo["annotations"] = annotations
	}

	jsonData, err := json.MarshalIndent(toolInfo, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format tool info: %w", err)
	}

	return string(jsonData), nil
}

// FormatResourceDetailJSON formats detailed resource information as structured JSON.
// This format includes all available resource metadata and is used for programmatic
// consumption and resource introspection.
//
// Args:
//   - resource: The resource to format detailed information for
//
// Returns:
//   - JSON string containing complete resource information
//   - error: JSON marshaling errors (should be rare)
func (f *Formatters) FormatResourceDetailJSON(origin api.ResourceOrigin) (string, error) {
	resource := origin.Resource
	resourceInfo := map[string]interface{}{
		"uri":                    resource.URI,
		api.FieldName:            resource.Name,
		api.SchemaKeyDescription: resource.Description,
		api.FieldMimeType:        resource.MIMEType,
		ArgServer:                origin.Server,
	}

	jsonData, err := json.MarshalIndent(resourceInfo, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format resource info: %w", err)
	}

	return string(jsonData), nil
}

// FormatPromptDetailJSON formats detailed prompt information as structured JSON.
// This format includes argument specifications and is used for programmatic
// consumption and prompt introspection.
//
// Args:
//   - prompt: The prompt to format detailed information for
//
// Returns:
//   - JSON string containing complete prompt information including arguments
//   - error: JSON marshaling errors (should be rare)
func (f *Formatters) FormatPromptDetailJSON(origin api.PromptOrigin) (string, error) {
	prompt := origin.Prompt
	promptInfo := map[string]interface{}{
		api.FieldName:            prompt.Name,
		api.SchemaKeyDescription: prompt.Description,
		ArgServer:                origin.Server,
	}

	if len(prompt.Arguments) > 0 {
		args := make([]map[string]interface{}, len(prompt.Arguments))
		for i, arg := range prompt.Arguments {
			args[i] = map[string]interface{}{
				api.FieldName:            arg.Name,
				api.SchemaKeyDescription: arg.Description,
				api.SchemaKeyRequired:    arg.Required,
			}
		}
		promptInfo["arguments"] = args
	}

	jsonData, err := json.MarshalIndent(promptInfo, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format prompt info: %w", err)
	}

	return string(jsonData), nil
}

// FindTool searches for a tool by name in the provided tool list.
// This is a utility method for command implementations and internal lookups.
//
// Args:
//   - tools: Slice of tools to search in
//   - name: Exact name of the tool to find
//
// Returns:
//   - Pointer to the found tool, or nil if not found
//
// The search is case-sensitive and requires exact name matching.
func (f *Formatters) FindTool(tools []mcp.Tool, name string) *mcp.Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return &tool
		}
	}
	return nil
}

// FindResource searches for a resource by URI in the provided resource list.
// This is a utility method for command implementations and internal lookups.
//
// Args:
//   - resources: Slice of resources to search in
//   - uri: Exact URI of the resource to find
//
// Returns:
//   - Pointer to the found resource, or nil if not found
//
// The search is case-sensitive and requires exact URI matching.
func (f *Formatters) FindResource(resources []api.ResourceOrigin, uri string) []api.ResourceOrigin {
	var matches []api.ResourceOrigin
	for _, origin := range resources {
		if origin.Resource.URI == uri {
			matches = append(matches, origin)
		}
	}
	return matches
}

// FindPrompt searches for a prompt by name in the provided prompt list.
// This is a utility method for command implementations and internal lookups.
//
// Args:
//   - prompts: Slice of prompts to search in
//   - name: Exact name of the prompt to find
//
// Returns:
//   - Pointer to the found prompt, or nil if not found
//
// The search is case-sensitive and requires exact name matching.
func (f *Formatters) FindPrompt(prompts []api.PromptOrigin, name string) *api.PromptOrigin {
	for _, origin := range prompts {
		if origin.Prompt.Name == name {
			return &origin
		}
	}
	return nil
}

// SerializeContent serializes MCP content items to a format suitable for JSON.
// This preserves the full structure of content items for proper response unwrapping.
//
// Args:
//   - content: Slice of MCP content interfaces
//
// Returns:
//   - Slice of serializable content representations
func SerializeContent(content []mcp.Content) []interface{} {
	result := make([]interface{}, 0, len(content))
	for _, item := range content {
		if textContent, ok := mcp.AsTextContent(item); ok {
			result = append(result, map[string]interface{}{
				"type": "text",
				"text": textContent.Text,
			})
		} else if imageContent, ok := mcp.AsImageContent(item); ok {
			result = append(result, map[string]interface{}{
				"type":            "image",
				api.FieldMimeType: imageContent.MIMEType,
				"dataSize":        len(imageContent.Data),
			})
		} else if audioContent, ok := mcp.AsAudioContent(item); ok {
			result = append(result, map[string]interface{}{
				"type":            "audio",
				api.FieldMimeType: audioContent.MIMEType,
				"dataSize":        len(audioContent.Data),
			})
		} else {
			// Fallback for unknown content types
			result = append(result, item)
		}
	}
	return result
}
