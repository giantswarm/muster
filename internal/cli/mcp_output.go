package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	pkgstrings "muster/pkg/strings"

	"gopkg.in/yaml.v3"
)

// mcpToolListItem represents a tool in list output format.
// Uses both json and yaml tags to avoid duplication across format cases.
type mcpToolListItem struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
}

// mcpResourceListItem represents a resource in list output format.
type mcpResourceListItem struct {
	URI         string `json:"uri" yaml:"uri"`
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty" yaml:"mimeType,omitempty"`
}

// mcpPromptListItem represents a prompt in list output format.
type mcpPromptListItem struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
}

// outputJSON marshals data to JSON and prints it to stdout.
func outputJSON(data interface{}) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to format as JSON: %w", err)
	}
	fmt.Println(string(jsonData))
	return nil
}

// outputYAML marshals data to YAML and prints it to stdout.
func outputYAML(data interface{}) error {
	yamlData, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to format as YAML: %w", err)
	}
	fmt.Print(string(yamlData))
	return nil
}

// Description truncation lengths for table output.
// Different lengths are used based on the number of columns displayed.
const (
	// descLengthNormal is used for standard table output with fewer columns.
	// Uses the shared constant from pkg/strings for consistency across packages.
	descLengthNormal = pkgstrings.DefaultDescriptionMaxLen
	// descLengthWide is used for wide output where more columns are displayed.
	descLengthWide = 50
	// descLengthCompact is used when space is very limited (e.g., resources with many columns).
	descLengthCompact = 40
)

// truncateString truncates a string to maxLen characters, adding "..." if truncated.
// It also replaces newlines with spaces to ensure single-line output.
// This is a convenience wrapper around pkgstrings.TruncateDescription.
func truncateString(s string, maxLen int) string {
	return pkgstrings.TruncateDescription(s, maxLen)
}

// pluralize returns a formatted string with count and properly pluralized word.
// Example: pluralize(1, "tool") -> "1 tool", pluralize(5, "tool") -> "5 tools"
func pluralize(count int, singular string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

// FormatMCPToolsWithOptions formats and displays MCP tools with additional options.
func FormatMCPToolsWithOptions(tools []MCPTool, format OutputFormat, noHeaders bool) error {
	if len(tools) == 0 {
		fmt.Println("No tools found")
		return nil
	}

	// Sort tools by name for consistent output
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	// Convert to list items for JSON/YAML output
	if format == OutputFormatJSON || format == OutputFormatYAML {
		items := make([]mcpToolListItem, len(tools))
		for i, tool := range tools {
			items[i] = mcpToolListItem{
				Name:        tool.Name,
				Description: tool.Description,
			}
		}
		if format == OutputFormatJSON {
			return outputJSON(items)
		}
		return outputYAML(items)
	}

	// kubectl-style plain table format
	tw := NewPlainTableWriter(os.Stdout)

	// Wide mode: add SERVER column and show input schema summary
	isWide := format == OutputFormatWide
	if isWide {
		tw.SetHeaders([]string{"NAME", "DESCRIPTION", "SERVER", "ARGS"})
	} else {
		tw.SetHeaders([]string{"NAME", "DESCRIPTION"})
	}
	tw.SetNoHeaders(noHeaders)

	for _, tool := range tools {
		if isWide {
			server := extractServerFromToolName(tool.Name)
			argCount := countToolArgs(tool)
			tw.AppendRow([]string{
				tool.Name,
				truncateString(tool.Description, descLengthWide),
				server,
				argCount,
			})
		} else {
			tw.AppendRow([]string{tool.Name, truncateString(tool.Description, descLengthNormal)})
		}
	}

	tw.Render()

	// Print summary unless headers are suppressed (implies scripting mode)
	if !noHeaders {
		fmt.Printf("\n%s\n", pluralize(len(tools), "tool"))
	}
	return nil
}

// extractServerFromToolName extracts the server name from a tool name.
// Tool names follow the pattern "server_toolname" (e.g., "github_create_issue").
func extractServerFromToolName(name string) string {
	// Handle well-known prefixes
	knownPrefixes := []string{"core_", "mcp_", "workflow_", "action_"}
	for _, prefix := range knownPrefixes {
		if strings.HasPrefix(name, prefix) {
			return strings.TrimSuffix(prefix, "_")
		}
	}

	// Extract the first segment before underscore
	if idx := strings.Index(name, "_"); idx > 0 {
		return name[:idx]
	}
	return "-"
}

// countToolArgs returns a string representation of the number of arguments.
func countToolArgs(tool MCPTool) string {
	if tool.InputSchema.Properties == nil {
		return "-"
	}
	count := len(tool.InputSchema.Properties)
	if count == 0 {
		return "-"
	}
	reqCount := len(tool.InputSchema.Required)
	if reqCount > 0 {
		return fmt.Sprintf("%d (%d req)", count, reqCount)
	}
	return fmt.Sprintf("%d", count)
}

// FormatMCPResourcesWithOptions formats and displays MCP resources with additional options.
func FormatMCPResourcesWithOptions(resources []MCPResource, format OutputFormat, noHeaders bool) error {
	if len(resources) == 0 {
		fmt.Println("No resources found")
		return nil
	}

	// Sort resources by URI for consistent output
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].URI < resources[j].URI
	})

	// Convert to list items for JSON/YAML output
	if format == OutputFormatJSON || format == OutputFormatYAML {
		items := make([]mcpResourceListItem, len(resources))
		for i, resource := range resources {
			items[i] = mcpResourceListItem{
				URI:         resource.URI,
				Name:        resource.Name,
				Description: resource.Description,
				MIMEType:    resource.MIMEType,
			}
		}
		if format == OutputFormatJSON {
			return outputJSON(items)
		}
		return outputYAML(items)
	}

	// kubectl-style plain table format
	tw := NewPlainTableWriter(os.Stdout)

	// Wide mode: add NAME column
	isWide := format == OutputFormatWide
	if isWide {
		tw.SetHeaders([]string{"URI", "NAME", "DESCRIPTION", "MIME TYPE"})
	} else {
		tw.SetHeaders([]string{"URI", "DESCRIPTION", "MIME TYPE"})
	}
	tw.SetNoHeaders(noHeaders)

	for _, resource := range resources {
		// Use description if available, otherwise use name (some MCP resources
		// store description in the name field)
		desc := resource.Description
		if desc == "" && !isWide {
			desc = resource.Name
		}
		if isWide {
			// In wide mode, truncate both NAME and DESCRIPTION columns
			// to prevent excessively wide output when Name contains long text
			name := resource.Name
			// If Name looks like a description (long sentence), truncate it
			if len(name) > descLengthWide {
				name = truncateString(name, descLengthWide)
			}
			tw.AppendRow([]string{
				resource.URI,
				name,
				truncateString(desc, descLengthCompact),
				resource.MIMEType,
			})
		} else {
			tw.AppendRow([]string{
				resource.URI,
				truncateString(desc, descLengthNormal),
				resource.MIMEType,
			})
		}
	}

	tw.Render()

	// Print summary unless headers are suppressed (implies scripting mode)
	if !noHeaders {
		fmt.Printf("\n%s\n", pluralize(len(resources), "resource"))
	}
	return nil
}

// FormatMCPPromptsWithOptions formats and displays MCP prompts with additional options.
func FormatMCPPromptsWithOptions(prompts []MCPPrompt, format OutputFormat, noHeaders bool) error {
	if len(prompts) == 0 {
		fmt.Println("No prompts found")
		return nil
	}

	// Sort prompts by name for consistent output
	sort.Slice(prompts, func(i, j int) bool {
		return prompts[i].Name < prompts[j].Name
	})

	// Convert to list items for JSON/YAML output
	if format == OutputFormatJSON || format == OutputFormatYAML {
		items := make([]mcpPromptListItem, len(prompts))
		for i, prompt := range prompts {
			items[i] = mcpPromptListItem{
				Name:        prompt.Name,
				Description: prompt.Description,
			}
		}
		if format == OutputFormatJSON {
			return outputJSON(items)
		}
		return outputYAML(items)
	}

	// kubectl-style plain table format
	tw := NewPlainTableWriter(os.Stdout)

	// Wide mode: add ARGS column
	isWide := format == OutputFormatWide
	if isWide {
		tw.SetHeaders([]string{"NAME", "DESCRIPTION", "ARGS"})
	} else {
		tw.SetHeaders([]string{"NAME", "DESCRIPTION"})
	}
	tw.SetNoHeaders(noHeaders)

	for _, prompt := range prompts {
		if isWide {
			argCount := countPromptArgs(prompt)
			tw.AppendRow([]string{
				prompt.Name,
				truncateString(prompt.Description, descLengthWide),
				argCount,
			})
		} else {
			tw.AppendRow([]string{prompt.Name, truncateString(prompt.Description, descLengthNormal)})
		}
	}

	tw.Render()

	// Print summary unless headers are suppressed (implies scripting mode)
	if !noHeaders {
		fmt.Printf("\n%s\n", pluralize(len(prompts), "prompt"))
	}
	return nil
}

// countPromptArgs returns a string representation of the number of arguments.
func countPromptArgs(prompt MCPPrompt) string {
	if len(prompt.Arguments) == 0 {
		return "-"
	}
	reqCount := 0
	for _, arg := range prompt.Arguments {
		if arg.Required {
			reqCount++
		}
	}
	if reqCount > 0 {
		return fmt.Sprintf("%d (%d req)", len(prompt.Arguments), reqCount)
	}
	return fmt.Sprintf("%d", len(prompt.Arguments))
}

// FormatMCPToolDetail formats and displays detailed MCP tool info.
// For table format, it uses a kubectl-describe-like plain text output.
func FormatMCPToolDetail(tool MCPTool, format OutputFormat) error {
	if format == OutputFormatJSON || format == OutputFormatYAML {
		toolInfo := map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
		}
		if format == OutputFormatJSON {
			return outputJSON(toolInfo)
		}
		return outputYAML(toolInfo)
	}

	// kubectl-describe-like plain text format
	fmt.Printf("Name:         %s\n", tool.Name)
	fmt.Printf("Description:  %s\n", tool.Description)

	// Render input schema as readable arguments
	if tool.InputSchema.Properties != nil {
		fmt.Println("\nArguments:")
		renderSchemaProperties(tool.InputSchema.Properties, tool.InputSchema.Required, "  ")
	}

	return nil
}

// renderSchemaProperties renders JSON schema properties in a readable format.
// It recursively handles nested objects and arrays.
func renderSchemaProperties(properties map[string]interface{}, required []string, indent string) {
	// Build a set of required properties for fast lookup
	requiredSet := make(map[string]bool)
	for _, r := range required {
		requiredSet[r] = true
	}

	// Get sorted property names for consistent output
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		prop := properties[name]
		propMap, ok := prop.(map[string]interface{})
		if !ok {
			continue
		}

		// Get type
		propType := "any"
		if t, ok := propMap["type"].(string); ok {
			propType = t
		}

		// Check if required
		isRequired := requiredSet[name]
		requiredStr := ""
		if isRequired {
			requiredStr = ", required"
		}

		fmt.Printf("%s%s (%s%s)\n", indent, name, propType, requiredStr)

		// Description
		if desc, ok := propMap["description"].(string); ok && desc != "" {
			// Wrap long descriptions
			wrapped := wrapText(desc, 60)
			for i, line := range wrapped {
				if i == 0 {
					fmt.Printf("%s  Description: %s\n", indent, line)
				} else {
					fmt.Printf("%s               %s\n", indent, line)
				}
			}
		}

		// Default value
		if def, ok := propMap["default"]; ok {
			fmt.Printf("%s  Default: %v\n", indent, def)
		}

		// Enum values
		if enum, ok := propMap["enum"].([]interface{}); ok && len(enum) > 0 {
			enumStrs := make([]string, len(enum))
			for i, e := range enum {
				enumStrs[i] = fmt.Sprintf("%v", e)
			}
			fmt.Printf("%s  Values: %s\n", indent, strings.Join(enumStrs, ", "))
		}

		// Handle nested object properties
		if propType == "object" {
			if nestedProps, ok := propMap["properties"].(map[string]interface{}); ok {
				var nestedRequired []string
				if req, ok := propMap["required"].([]interface{}); ok {
					for _, r := range req {
						if s, ok := r.(string); ok {
							nestedRequired = append(nestedRequired, s)
						}
					}
				}
				fmt.Printf("%s  Properties:\n", indent)
				renderSchemaProperties(nestedProps, nestedRequired, indent+"    ")
			}
			// Handle additionalProperties for map-like objects
			if addProps, ok := propMap["additionalProperties"].(map[string]interface{}); ok {
				fmt.Printf("%s  Value Schema:\n", indent)
				renderAdditionalProperties(addProps, indent+"    ")
			}
		}

		// Handle array items
		if propType == "array" {
			if items, ok := propMap["items"].(map[string]interface{}); ok {
				fmt.Printf("%s  Items:\n", indent)
				renderArrayItems(items, indent+"    ")
			}
		}
	}
}

// renderAdditionalProperties renders the schema for additionalProperties.
func renderAdditionalProperties(props map[string]interface{}, indent string) {
	propType := "any"
	if t, ok := props["type"].(string); ok {
		propType = t
	}
	fmt.Printf("%sType: %s\n", indent, propType)

	if desc, ok := props["description"].(string); ok && desc != "" {
		fmt.Printf("%sDescription: %s\n", indent, desc)
	}

	if propType == "object" {
		if nestedProps, ok := props["properties"].(map[string]interface{}); ok {
			var nestedRequired []string
			if req, ok := props["required"].([]interface{}); ok {
				for _, r := range req {
					if s, ok := r.(string); ok {
						nestedRequired = append(nestedRequired, s)
					}
				}
			}
			fmt.Printf("%sProperties:\n", indent)
			renderSchemaProperties(nestedProps, nestedRequired, indent+"  ")
		}
	}
}

// renderArrayItems renders the schema for array items.
func renderArrayItems(items map[string]interface{}, indent string) {
	itemType := "any"
	if t, ok := items["type"].(string); ok {
		itemType = t
	}
	fmt.Printf("%sType: %s\n", indent, itemType)

	if desc, ok := items["description"].(string); ok && desc != "" {
		fmt.Printf("%sDescription: %s\n", indent, desc)
	}

	if itemType == "object" {
		if nestedProps, ok := items["properties"].(map[string]interface{}); ok {
			var nestedRequired []string
			if req, ok := items["required"].([]interface{}); ok {
				for _, r := range req {
					if s, ok := r.(string); ok {
						nestedRequired = append(nestedRequired, s)
					}
				}
			}
			fmt.Printf("%sProperties:\n", indent)
			renderSchemaProperties(nestedProps, nestedRequired, indent+"  ")
		}
	}
}

// wrapText wraps text to the specified width at word boundaries.
func wrapText(text string, width int) []string {
	if len(text) <= width {
		return []string{text}
	}

	var lines []string
	words := strings.Fields(text)
	currentLine := ""

	for _, word := range words {
		if currentLine == "" {
			currentLine = word
		} else if len(currentLine)+1+len(word) <= width {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}

	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}

// FormatMCPResourceDetail formats and displays detailed MCP resource info.
// For table format, it uses a kubectl-describe-like plain text output.
func FormatMCPResourceDetail(resource MCPResource, format OutputFormat) error {
	if format == OutputFormatJSON || format == OutputFormatYAML {
		resourceInfo := map[string]interface{}{
			"uri":         resource.URI,
			"name":        resource.Name,
			"description": resource.Description,
			"mimeType":    resource.MIMEType,
		}
		if format == OutputFormatJSON {
			return outputJSON(resourceInfo)
		}
		return outputYAML(resourceInfo)
	}

	// kubectl-describe-like plain text format
	fmt.Printf("URI:          %s\n", resource.URI)
	fmt.Printf("Name:         %s\n", resource.Name)
	if resource.Description != "" {
		fmt.Printf("Description:  %s\n", resource.Description)
	}
	if resource.MIMEType != "" {
		fmt.Printf("MIME Type:    %s\n", resource.MIMEType)
	}

	return nil
}

// FormatMCPPromptDetail formats and displays detailed MCP prompt info.
// For table format, it uses a kubectl-describe-like plain text output.
func FormatMCPPromptDetail(prompt MCPPrompt, format OutputFormat) error {
	if format == OutputFormatJSON || format == OutputFormatYAML {
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
		if format == OutputFormatJSON {
			return outputJSON(promptInfo)
		}
		return outputYAML(promptInfo)
	}

	// kubectl-describe-like plain text format
	fmt.Printf("Name:         %s\n", prompt.Name)
	fmt.Printf("Description:  %s\n", prompt.Description)

	// Print arguments
	if len(prompt.Arguments) > 0 {
		fmt.Println("\nArguments:")
		for _, arg := range prompt.Arguments {
			requiredStr := ""
			if arg.Required {
				requiredStr = ", required"
			}
			fmt.Printf("  %s (string%s)\n", arg.Name, requiredStr)
			if arg.Description != "" {
				wrapped := wrapText(arg.Description, 60)
				for i, line := range wrapped {
					if i == 0 {
						fmt.Printf("    Description: %s\n", line)
					} else {
						fmt.Printf("                 %s\n", line)
					}
				}
			}
		}
	}

	return nil
}
