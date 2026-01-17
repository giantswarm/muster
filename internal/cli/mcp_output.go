package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

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

// truncateString truncates a string to maxLen characters, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

// FormatMCPTools formats and displays MCP tools in the specified format.
func FormatMCPTools(tools []MCPTool, format OutputFormat) error {
	return FormatMCPToolsWithOptions(tools, format, false)
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
	tw.SetHeaders([]string{"NAME", "DESCRIPTION"})
	tw.SetNoHeaders(noHeaders)

	for _, tool := range tools {
		tw.AppendRow([]string{tool.Name, truncateString(tool.Description, 60)})
	}

	tw.Render()

	// Print summary unless headers are suppressed (implies scripting mode)
	if !noHeaders {
		fmt.Printf("\n%d tools\n", len(tools))
	}
	return nil
}

// FormatMCPResources formats and displays MCP resources in the specified format.
func FormatMCPResources(resources []MCPResource, format OutputFormat) error {
	return FormatMCPResourcesWithOptions(resources, format, false)
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
	tw.SetHeaders([]string{"URI", "NAME", "DESCRIPTION", "MIME TYPE"})
	tw.SetNoHeaders(noHeaders)

	for _, resource := range resources {
		desc := resource.Description
		if desc == "" {
			desc = resource.Name
		}
		tw.AppendRow([]string{
			truncateString(resource.URI, 40),
			resource.Name,
			truncateString(desc, 40),
			resource.MIMEType,
		})
	}

	tw.Render()

	// Print summary unless headers are suppressed (implies scripting mode)
	if !noHeaders {
		fmt.Printf("\n%d resources\n", len(resources))
	}
	return nil
}

// FormatMCPPrompts formats and displays MCP prompts in the specified format.
func FormatMCPPrompts(prompts []MCPPrompt, format OutputFormat) error {
	return FormatMCPPromptsWithOptions(prompts, format, false)
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
	tw.SetHeaders([]string{"NAME", "DESCRIPTION"})
	tw.SetNoHeaders(noHeaders)

	for _, prompt := range prompts {
		tw.AppendRow([]string{prompt.Name, truncateString(prompt.Description, 60)})
	}

	tw.Render()

	// Print summary unless headers are suppressed (implies scripting mode)
	if !noHeaders {
		fmt.Printf("\n%d prompts\n", len(prompts))
	}
	return nil
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
