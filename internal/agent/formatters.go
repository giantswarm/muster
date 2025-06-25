package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Formatters provides utilities for formatting MCP data consistently
type Formatters struct{}

// NewFormatters creates a new formatters instance
func NewFormatters() *Formatters {
	return &Formatters{}
}

// FormatToolsList formats tools list for console output
func (f *Formatters) FormatToolsList(tools []mcp.Tool) string {
	if len(tools) == 0 {
		return "No tools available."
	}

	var output []string
	output = append(output, fmt.Sprintf("Available tools (%d):", len(tools)))
	for i, tool := range tools {
		output = append(output, fmt.Sprintf("  %d. %-30s - %s", i+1, tool.Name, tool.Description))
	}
	return strings.Join(output, "\n")
}

// FormatResourcesList formats resources list for console output
func (f *Formatters) FormatResourcesList(resources []mcp.Resource) string {
	if len(resources) == 0 {
		return "No resources available."
	}

	var output []string
	output = append(output, fmt.Sprintf("Available resources (%d):", len(resources)))
	for i, resource := range resources {
		desc := resource.Description
		if desc == "" {
			desc = resource.Name
		}
		output = append(output, fmt.Sprintf("  %d. %-40s - %s", i+1, resource.URI, desc))
	}
	return strings.Join(output, "\n")
}

// FormatPromptsList formats prompts list for console output
func (f *Formatters) FormatPromptsList(prompts []mcp.Prompt) string {
	if len(prompts) == 0 {
		return "No prompts available."
	}

	var output []string
	output = append(output, fmt.Sprintf("Available prompts (%d):", len(prompts)))
	for i, prompt := range prompts {
		output = append(output, fmt.Sprintf("  %d. %-30s - %s", i+1, prompt.Name, prompt.Description))
	}
	return strings.Join(output, "\n")
}

// FormatToolDetail formats detailed tool information
func (f *Formatters) FormatToolDetail(tool mcp.Tool) string {
	var output []string
	output = append(output, fmt.Sprintf("Tool: %s", tool.Name))
	output = append(output, fmt.Sprintf("Description: %s", tool.Description))
	output = append(output, "Input Schema:")
	output = append(output, PrettyJSON(tool.InputSchema))
	return strings.Join(output, "\n")
}

// FormatResourceDetail formats detailed resource information
func (f *Formatters) FormatResourceDetail(resource mcp.Resource) string {
	var output []string
	output = append(output, fmt.Sprintf("Resource: %s", resource.URI))
	output = append(output, fmt.Sprintf("Name: %s", resource.Name))
	if resource.Description != "" {
		output = append(output, fmt.Sprintf("Description: %s", resource.Description))
	}
	if resource.MIMEType != "" {
		output = append(output, fmt.Sprintf("MIME Type: %s", resource.MIMEType))
	}
	return strings.Join(output, "\n")
}

// FormatPromptDetail formats detailed prompt information
func (f *Formatters) FormatPromptDetail(prompt mcp.Prompt) string {
	var output []string
	output = append(output, fmt.Sprintf("Prompt: %s", prompt.Name))
	output = append(output, fmt.Sprintf("Description: %s", prompt.Description))
	if len(prompt.Arguments) > 0 {
		output = append(output, "Arguments:")
		for _, arg := range prompt.Arguments {
			required := ""
			if arg.Required {
				required = " (required)"
			}
			output = append(output, fmt.Sprintf("  - %s%s: %s", arg.Name, required, arg.Description))
		}
	}
	return strings.Join(output, "\n")
}

// FormatToolsListJSON formats tools list as JSON
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

// FormatResourcesListJSON formats resources list as JSON
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

// FormatPromptsListJSON formats prompts list as JSON
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

// FormatToolDetailJSON formats detailed tool information as JSON
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

// FormatResourceDetailJSON formats detailed resource information as JSON
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

// FormatPromptDetailJSON formats detailed prompt information as JSON
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

// FindTool finds a tool by name in the cache
func (f *Formatters) FindTool(tools []mcp.Tool, name string) *mcp.Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return &tool
		}
	}
	return nil
}

// FindResource finds a resource by URI in the cache
func (f *Formatters) FindResource(resources []mcp.Resource, uri string) *mcp.Resource {
	for _, resource := range resources {
		if resource.URI == uri {
			return &resource
		}
	}
	return nil
}

// FindPrompt finds a prompt by name in the cache
func (f *Formatters) FindPrompt(prompts []mcp.Prompt, name string) *mcp.Prompt {
	for _, prompt := range prompts {
		if prompt.Name == name {
			return &prompt
		}
	}
	return nil
}
