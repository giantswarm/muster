package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	pkgstrings "github.com/giantswarm/muster/pkg/strings"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
)

// Formatters provides utilities for formatting MCP data consistently.
// It supports both human-readable console output and structured JSON responses
// for tools, resources, and prompts. The formatters ensure consistent
// presentation across different output modes (REPL, CLI, MCP server).
//
// Key features:
//   - Console-friendly formatting with numbering and alignment
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

// FormatToolsList formats a list of tools for human-readable console output.
// The output includes numbering, aligned columns, and descriptive text to
// make it easy to browse available tools interactively.
//
// Args:
//   - tools: Slice of tools to format
//
// Returns:
//   - Formatted string with numbered list of tools and descriptions
//
// Output format:
//
//	Available tools (N):
//	  1. tool_name                    - Tool description
//	  2. another_tool                 - Another description
//
// If no tools are available, returns a user-friendly message.
func (f *Formatters) FormatToolsList(tools []mcp.Tool) string {
	if len(tools) == 0 {
		return "No tools available."
	}

	var output []string
	output = append(output, fmt.Sprintf("Available tools (%d):", len(tools)))
	for i, tool := range tools {
		desc := pkgstrings.TruncateDescription(tool.Description, descriptionMaxLen)
		output = append(output, fmt.Sprintf("  %d. %-30s - %s", i+1, tool.Name, desc))
	}
	return strings.Join(output, "\n")
}

// FormatResourcesList formats a list of resources for human-readable console output.
// The output includes numbering, aligned columns for URIs, and descriptions to
// make it easy to browse available resources interactively.
//
// Args:
//   - resources: Slice of resources to format
//
// Returns:
//   - Formatted string with numbered list of resources and descriptions
//
// Output format:
//
//	Available resources (N):
//	  1. file://config.yaml                    - Configuration file
//	  2. memory://cache/data                   - Cached data
//
// If a resource has no description, the name field is used as a fallback.
// If no resources are available, returns a user-friendly message.
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
		desc = pkgstrings.TruncateDescription(desc, descriptionMaxLen)
		output = append(output, fmt.Sprintf("  %d. %-40s - %s", i+1, resource.URI, desc))
	}
	return strings.Join(output, "\n")
}

// FormatPromptsList formats a list of prompts for human-readable console output.
// The output includes numbering, aligned columns, and descriptions to
// make it easy to browse available prompts interactively.
//
// Args:
//   - prompts: Slice of prompts to format
//
// Returns:
//   - Formatted string with numbered list of prompts and descriptions
//
// Output format:
//
//	Available prompts (N):
//	  1. code_review                  - Review code for quality
//	  2. documentation                - Generate documentation
//
// If no prompts are available, returns a user-friendly message.
func (f *Formatters) FormatPromptsList(prompts []mcp.Prompt) string {
	if len(prompts) == 0 {
		return "No prompts available."
	}

	var output []string
	output = append(output, fmt.Sprintf("Available prompts (%d):", len(prompts)))
	for i, prompt := range prompts {
		desc := pkgstrings.TruncateDescription(prompt.Description, descriptionMaxLen)
		output = append(output, fmt.Sprintf("  %d. %-30s - %s", i+1, prompt.Name, desc))
	}
	return strings.Join(output, "\n")
}

// FormatToolDetail formats detailed information about a single tool for console display.
// This provides comprehensive information including the tool's input schema,
// which is essential for understanding how to use the tool.
//
// Args:
//   - tool: The tool to format detailed information for
//
// Returns:
//   - Multi-line formatted string with tool details and schema
//
// Output format:
//
//	Tool: tool_name
//	Description: Tool description
//	Input Schema:
//	{
//	  "type": "object",
//	  "properties": { ... }
//	}
func (f *Formatters) FormatToolDetail(tool mcp.Tool) string {
	jsonData, err := json.MarshalIndent(tool.InputSchema, "", "    ")
	if err != nil {
		return ""
	}

	var output []string
	output = append(output, fmt.Sprintf("Tool: %s", tool.Name))
	output = append(output, fmt.Sprintf("Description: %s", tool.Description))
	output = append(output, "Input Schema:")
	output = append(output, string(jsonData))
	return strings.Join(output, "\n")
}

// FormatResourceDetail formats detailed information about a single resource for console display.
// This provides comprehensive metadata about the resource including URI, name, description,
// and MIME type when available.
//
// Args:
//   - resource: The resource to format detailed information for
//
// Returns:
//   - Multi-line formatted string with resource details
//
// Output format:
//
//	Resource: file://config.yaml
//	Name: config.yaml
//	Description: Configuration file
//	MIME Type: application/yaml
//
// Optional fields (description, MIME type) are only included if present.
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

// FormatPromptDetail formats detailed information about a single prompt for console display.
// This provides comprehensive information including the prompt's arguments and their
// requirements, which is essential for understanding how to use the prompt.
//
// Args:
//   - prompt: The prompt to format detailed information for
//
// Returns:
//   - Multi-line formatted string with prompt details and arguments
//
// Output format:
//
//	Prompt: code_review
//	Description: Review code for quality
//	Arguments:
//	  - language (required): Programming language
//	  - style: Code style to enforce
//
// Arguments are listed with their names, requirements, and descriptions.
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
// Output format:
//
//	[
//	  {
//	    "uri": "file://config.yaml",
//	    "name": "config.yaml",
//	    "description": "Configuration file",
//	    "mimeType": "application/yaml"
//	  }
//	]
//
// If a resource has no description, the name field is used as a fallback.
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
// Output format:
//
//	[
//	  {
//	    "name": "code_review",
//	    "description": "Review code for quality"
//	  }
//	]
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
//
// Output format:
//
//	{
//	  "uri": "file://config.yaml",
//	  "name": "config.yaml",
//	  "description": "Configuration file",
//	  "mimeType": "application/yaml"
//	}
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
//
// Output format:
//
//	{
//	  "name": "code_review",
//	  "description": "Review code for quality",
//	  "arguments": [
//	    {
//	      "name": "language",
//	      "description": "Programming language",
//	      "required": true
//	    }
//	  ]
//	}
//
// The arguments field is only included if the prompt has arguments.
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

// IsAuthChallenge checks if a tool result contains an authentication challenge.
// It parses the first text content as JSON and checks for the "auth_required" status.
//
// Args:
//   - result: The tool result to check
//
// Returns:
//   - *api.AuthChallenge: The parsed challenge if found, nil otherwise
func (f *Formatters) IsAuthChallenge(result *mcp.CallToolResult) *api.AuthChallenge {
	if result == nil || len(result.Content) == 0 {
		return nil
	}

	// Look for text content that might be an auth challenge
	for _, content := range result.Content {
		textContent, ok := mcp.AsTextContent(content)
		if !ok {
			continue
		}

		// Try to parse as JSON
		var challenge api.AuthChallenge
		if err := json.Unmarshal([]byte(textContent.Text), &challenge); err != nil {
			continue
		}

		// Check if it's an auth challenge
		if challenge.Status == "auth_required" && challenge.AuthURL != "" {
			return &challenge
		}
	}

	return nil
}

// FormatAuthChallenge formats an authentication challenge for user display.
// This produces a user-friendly message with clear instructions on how to
// authenticate.
//
// Args:
//   - challenge: The authentication challenge to format
//
// Returns:
//   - Formatted string with authentication instructions
//
// Output format:
//
//	[Authentication Required]
//	Server: mcp-kubernetes
//
//	Authentication is required to access this resource.
//	Please visit the following URL to authenticate:
//
//	https://auth.example.com/authorize?...
//
//	After authenticating, return here and retry your request.
func (f *Formatters) FormatAuthChallenge(challenge *api.AuthChallenge) string {
	return f.FormatAuthChallengeWithBrowserStatus(challenge, false)
}

// FormatAuthChallengeWithBrowserStatus formats an authentication challenge for user display.
// This produces a user-friendly message with clear instructions on how to
// authenticate, including information about whether the browser was opened.
//
// Args:
//   - challenge: The authentication challenge to format
//   - browserOpened: Whether the browser was successfully opened
//
// Returns:
//   - Formatted string with authentication instructions
func (f *Formatters) FormatAuthChallengeWithBrowserStatus(challenge *api.AuthChallenge, browserOpened bool) string {
	var output []string

	output = append(output, "[Authentication Required]")

	if challenge.ServerName != "" {
		output = append(output, fmt.Sprintf("Server: %s", challenge.ServerName))
	}

	output = append(output, "")

	if browserOpened {
		output = append(output, "Your browser has been opened for authentication.")
		output = append(output, "")
		output = append(output, "If it didn't open, click here:")
	} else {
		if challenge.Message != "" {
			output = append(output, challenge.Message)
		} else {
			output = append(output, "Authentication is required to access this resource.")
		}
		output = append(output, "Please visit the following URL to authenticate:")
	}

	output = append(output, "")
	output = append(output, challenge.AuthURL)
	output = append(output, "")
	output = append(output, "After authenticating, return here and retry your request.")

	return strings.Join(output, "\n")
}
