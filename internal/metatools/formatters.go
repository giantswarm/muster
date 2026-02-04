package metatools

import (
	"encoding/json"
	"fmt"

	pkgstrings "muster/pkg/strings"

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

// descriptionMaxLen is the maximum length for descriptions in formatted output.
// Uses the shared constant from pkg/strings for consistency across packages.
const descriptionMaxLen = pkgstrings.DefaultDescriptionMaxLen

// NewFormatters creates a new formatters instance.
// The formatters instance is stateless and can be safely used concurrently.
func NewFormatters() *Formatters {
	return &Formatters{}
}

// FormatToolsListJSON formats a list of tools as structured JSON.
// This format is used for programmatic consumption, MCP server responses,
// and integration with external tools that expect structured data.
//
// Args:
//   - tools: Slice of tools to format
//
// Returns:
//   - JSON string containing array of tool objects with name and description
//   - error: JSON marshaling errors (should be rare)
//
// Output format:
//
//	[
//	  {
//	    "name": "tool_name",
//	    "description": "Tool description"
//	  }
//	]
//
// If no tools are available, returns a simple message string.
func (f *Formatters) FormatToolsListJSON(tools []mcp.Tool) (string, error) {
	if len(tools) == 0 {
		return "No tools available", nil
	}

	type ToolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	toolList := make([]ToolInfo, len(tools))
	for i, tool := range tools {
		toolList[i] = ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
		}
	}

	jsonData, err := json.MarshalIndent(toolList, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format tools: %w", err)
	}

	return string(jsonData), nil
}

// FormatResourcesListJSON formats a list of resources as structured JSON.
// This format is used for programmatic consumption, MCP server responses,
// and integration with external tools that expect structured data.
//
// Args:
//   - resources: Slice of resources to format
//
// Returns:
//   - JSON string containing array of resource objects with URI, name, description, and MIME type
//   - error: JSON marshaling errors (should be rare)
//
// If no resources are available, returns a simple message string.
func (f *Formatters) FormatResourcesListJSON(resources []mcp.Resource) (string, error) {
	if len(resources) == 0 {
		return "No resources available", nil
	}

	type ResourceInfo struct {
		URI         string `json:"uri"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		MIMEType    string `json:"mimeType,omitempty"`
	}

	resourceList := make([]ResourceInfo, len(resources))
	for i, resource := range resources {
		desc := resource.Description
		if desc == "" {
			desc = resource.Name
		}
		resourceList[i] = ResourceInfo{
			URI:         resource.URI,
			Name:        resource.Name,
			Description: desc,
			MIMEType:    resource.MIMEType,
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
func (f *Formatters) FormatPromptsListJSON(prompts []mcp.Prompt) (string, error) {
	if len(prompts) == 0 {
		return "No prompts available", nil
	}

	type PromptInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	promptList := make([]PromptInfo, len(prompts))
	for i, prompt := range prompts {
		promptList[i] = PromptInfo{
			Name:        prompt.Name,
			Description: prompt.Description,
		}
	}

	jsonData, err := json.MarshalIndent(promptList, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format prompts: %w", err)
	}

	return string(jsonData), nil
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
//	  "name": "tool_name",
//	  "description": "Tool description",
//	  "inputSchema": { ... }
//	}
func (f *Formatters) FormatToolDetailJSON(tool mcp.Tool) (string, error) {
	toolInfo := map[string]interface{}{
		"name":        tool.Name,
		"description": tool.Description,
		"inputSchema": tool.InputSchema,
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
func (f *Formatters) FormatResourceDetailJSON(resource mcp.Resource) (string, error) {
	resourceInfo := map[string]interface{}{
		"uri":         resource.URI,
		"name":        resource.Name,
		"description": resource.Description,
		"mimeType":    resource.MIMEType,
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
func (f *Formatters) FormatPromptDetailJSON(prompt mcp.Prompt) (string, error) {
	promptInfo := map[string]interface{}{
		"name":        prompt.Name,
		"description": prompt.Description,
	}

	if len(prompt.Arguments) > 0 {
		args := make([]map[string]interface{}, len(prompt.Arguments))
		for i, arg := range prompt.Arguments {
			args[i] = map[string]interface{}{
				"name":        arg.Name,
				"description": arg.Description,
				"required":    arg.Required,
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
func (f *Formatters) FindResource(resources []mcp.Resource, uri string) *mcp.Resource {
	for _, resource := range resources {
		if resource.URI == uri {
			return &resource
		}
	}
	return nil
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
func (f *Formatters) FindPrompt(prompts []mcp.Prompt, name string) *mcp.Prompt {
	for _, prompt := range prompts {
		if prompt.Name == name {
			return &prompt
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
				"type":     "image",
				"mimeType": imageContent.MIMEType,
				"dataSize": len(imageContent.Data),
			})
		} else if audioContent, ok := mcp.AsAudioContent(item); ok {
			result = append(result, map[string]interface{}{
				"type":     "audio",
				"mimeType": audioContent.MIMEType,
				"dataSize": len(audioContent.Data),
			})
		} else {
			// Fallback for unknown content types
			result = append(result, item)
		}
	}
	return result
}
