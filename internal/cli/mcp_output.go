package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
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
	if len(tools) == 0 {
		fmt.Printf("%s %s\n",
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("📋"),
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("No tools found"))
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

	// Table format
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("NAME"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("DESCRIPTION"),
	})

	for _, tool := range tools {
		t.AppendRow(table.Row{
			text.Colors{text.FgHiBlue, text.Bold}.Sprint(tool.Name),
			truncateString(tool.Description, 60),
		})
	}

	t.Render()
	fmt.Printf("\n%s %s %s %s\n",
		text.Colors{text.FgHiMagenta, text.Bold}.Sprint("🔧"),
		text.FgHiBlue.Sprint("Total:"),
		text.Bold.Sprint(len(tools)),
		text.FgHiBlue.Sprint("tools"))
	return nil
}

// FormatMCPResources formats and displays MCP resources in the specified format.
func FormatMCPResources(resources []MCPResource, format OutputFormat) error {
	if len(resources) == 0 {
		fmt.Printf("%s %s\n",
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("📋"),
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("No resources found"))
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

	// Table format
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("URI"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("NAME"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("DESCRIPTION"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("MIME TYPE"),
	})

	for _, resource := range resources {
		desc := resource.Description
		if desc == "" {
			desc = resource.Name
		}
		t.AppendRow(table.Row{
			text.Colors{text.FgHiCyan, text.Bold}.Sprint(truncateString(resource.URI, 40)),
			resource.Name,
			truncateString(desc, 40),
			resource.MIMEType,
		})
	}

	t.Render()
	fmt.Printf("\n%s %s %s %s\n",
		text.Colors{text.FgHiCyan, text.Bold}.Sprint("📦"),
		text.FgHiBlue.Sprint("Total:"),
		text.Bold.Sprint(len(resources)),
		text.FgHiBlue.Sprint("resources"))
	return nil
}

// FormatMCPPrompts formats and displays MCP prompts in the specified format.
func FormatMCPPrompts(prompts []MCPPrompt, format OutputFormat) error {
	if len(prompts) == 0 {
		fmt.Printf("%s %s\n",
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("📋"),
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("No prompts found"))
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

	// Table format
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("NAME"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("DESCRIPTION"),
	})

	for _, prompt := range prompts {
		t.AppendRow(table.Row{
			text.Colors{text.FgHiBlue, text.Bold}.Sprint(prompt.Name),
			truncateString(prompt.Description, 60),
		})
	}

	t.Render()
	fmt.Printf("\n%s %s %s %s\n",
		text.Colors{text.FgHiYellow, text.Bold}.Sprint("📝"),
		text.FgHiBlue.Sprint("Total:"),
		text.Bold.Sprint(len(prompts)),
		text.FgHiBlue.Sprint("prompts"))
	return nil
}

// FormatMCPToolDetail formats and displays detailed MCP tool info.
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

	// Table format
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("PROPERTY"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("VALUE"),
	})

	t.AppendRow(table.Row{
		text.Colors{text.FgHiYellow, text.Bold}.Sprint("name"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint(tool.Name),
	})
	t.AppendRow(table.Row{
		text.Colors{text.FgHiYellow, text.Bold}.Sprint("description"),
		tool.Description,
	})

	t.Render()

	// Print input schema separately for better readability
	if tool.InputSchema.Properties != nil {
		fmt.Printf("\n%s\n", text.Colors{text.FgHiBlue, text.Bold}.Sprint("📝 Input Schema:"))
		if schemaJSON, err := json.MarshalIndent(tool.InputSchema, "", "  "); err == nil {
			fmt.Println(string(schemaJSON))
		}
	}

	return nil
}

// FormatMCPResourceDetail formats and displays detailed MCP resource info.
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

	// Table format
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("PROPERTY"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("VALUE"),
	})

	t.AppendRow(table.Row{
		text.Colors{text.FgHiYellow, text.Bold}.Sprint("uri"),
		text.Colors{text.FgHiCyan, text.Bold}.Sprint(resource.URI),
	})
	t.AppendRow(table.Row{
		text.Colors{text.FgHiYellow, text.Bold}.Sprint("name"),
		resource.Name,
	})
	if resource.Description != "" {
		t.AppendRow(table.Row{
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("description"),
			resource.Description,
		})
	}
	if resource.MIMEType != "" {
		t.AppendRow(table.Row{
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("mimeType"),
			resource.MIMEType,
		})
	}

	t.Render()
	return nil
}

// FormatMCPPromptDetail formats and displays detailed MCP prompt info.
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

	// Table format
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("PROPERTY"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint("VALUE"),
	})

	t.AppendRow(table.Row{
		text.Colors{text.FgHiYellow, text.Bold}.Sprint("name"),
		text.Colors{text.FgHiBlue, text.Bold}.Sprint(prompt.Name),
	})
	t.AppendRow(table.Row{
		text.Colors{text.FgHiYellow, text.Bold}.Sprint("description"),
		prompt.Description,
	})

	t.Render()

	// Print arguments separately for better readability
	if len(prompt.Arguments) > 0 {
		fmt.Printf("\n%s\n", text.Colors{text.FgHiBlue, text.Bold}.Sprint("📝 Arguments:"))
		argsTable := table.NewWriter()
		argsTable.SetOutputMirror(os.Stdout)
		argsTable.SetStyle(table.StyleRounded)
		argsTable.AppendHeader(table.Row{
			text.Colors{text.FgHiBlue, text.Bold}.Sprint("NAME"),
			text.Colors{text.FgHiBlue, text.Bold}.Sprint("DESCRIPTION"),
			text.Colors{text.FgHiBlue, text.Bold}.Sprint("REQUIRED"),
		})

		for _, arg := range prompt.Arguments {
			required := "No"
			if arg.Required {
				required = text.Colors{text.FgHiYellow, text.Bold}.Sprint("Yes")
			}
			argsTable.AppendRow(table.Row{
				text.Bold.Sprint(arg.Name),
				arg.Description,
				required,
			})
		}
		argsTable.Render()
	}

	return nil
}
