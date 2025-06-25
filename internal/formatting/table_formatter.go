package formatting

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/mark3labs/mcp-go/mcp"
)

// TableFormatter provides rich table output formatting
type TableFormatter struct {
	options Options
}

// NewTableFormatter creates a new table formatter
func NewTableFormatter(options Options) Formatter {
	return &TableFormatter{
		options: options,
	}
}

// FormatToolsList formats tools list as a table
func (f *TableFormatter) FormatToolsList(tools []mcp.Tool) string {
	if len(tools) == 0 {
		return f.formatEmptyMessage("📋", "No tools found")
	}

	t := f.createTable()

	// Set headers
	headers := []interface{}{
		text.FgHiCyan.Sprint("NAME"),
		text.FgHiCyan.Sprint("DESCRIPTION"),
	}
	t.AppendHeader(headers)

	// Add rows
	for _, tool := range tools {
		row := []interface{}{
			text.FgHiCyan.Sprint(tool.Name),
			f.formatDescription(tool.Description),
		}
		t.AppendRow(row)
	}

	// Render table
	var result strings.Builder
	t.SetOutputMirror(&result)
	t.Render()

	// Add summary
	result.WriteString(fmt.Sprintf("\n🔧 %s %s %s\n",
		text.FgHiBlue.Sprint("Total:"),
		text.FgHiWhite.Sprint(len(tools)),
		text.FgHiBlue.Sprint("tools")))

	return result.String()
}

// FormatResourcesList formats resources list as a table
func (f *TableFormatter) FormatResourcesList(resources []mcp.Resource) string {
	if len(resources) == 0 {
		return f.formatEmptyMessage("📋", "No resources found")
	}

	t := f.createTable()

	// Set headers
	headers := []interface{}{
		text.FgHiCyan.Sprint("URI"),
		text.FgHiCyan.Sprint("NAME"),
		text.FgHiCyan.Sprint("DESCRIPTION"),
		text.FgHiCyan.Sprint("MIME TYPE"),
	}
	t.AppendHeader(headers)

	// Add rows
	for _, resource := range resources {
		desc := resource.Description
		if desc == "" {
			desc = resource.Name
		}

		row := []interface{}{
			text.FgHiCyan.Sprint(resource.URI),
			resource.Name,
			f.formatDescription(desc),
			f.formatMimeType(resource.MIMEType),
		}
		t.AppendRow(row)
	}

	// Render table
	var result strings.Builder
	t.SetOutputMirror(&result)
	t.Render()

	// Add summary
	result.WriteString(fmt.Sprintf("\n📄 %s %s %s\n",
		text.FgHiBlue.Sprint("Total:"),
		text.FgHiWhite.Sprint(len(resources)),
		text.FgHiBlue.Sprint("resources")))

	return result.String()
}

// FormatPromptsList formats prompts list as a table
func (f *TableFormatter) FormatPromptsList(prompts []mcp.Prompt) string {
	if len(prompts) == 0 {
		return f.formatEmptyMessage("📋", "No prompts found")
	}

	t := f.createTable()

	// Set headers
	headers := []interface{}{
		text.FgHiCyan.Sprint("NAME"),
		text.FgHiCyan.Sprint("DESCRIPTION"),
		text.FgHiCyan.Sprint("ARGUMENTS"),
	}
	t.AppendHeader(headers)

	// Add rows
	for _, prompt := range prompts {
		row := []interface{}{
			text.FgHiCyan.Sprint(prompt.Name),
			f.formatDescription(prompt.Description),
			f.formatArgumentCount(len(prompt.Arguments)),
		}
		t.AppendRow(row)
	}

	// Render table
	var result strings.Builder
	t.SetOutputMirror(&result)
	t.Render()

	// Add summary
	result.WriteString(fmt.Sprintf("\n💬 %s %s %s\n",
		text.FgHiBlue.Sprint("Total:"),
		text.FgHiWhite.Sprint(len(prompts)),
		text.FgHiBlue.Sprint("prompts")))

	return result.String()
}

// FormatToolDetail formats detailed tool information
func (f *TableFormatter) FormatToolDetail(tool mcp.Tool) string {
	t := f.createTable()

	// Set headers
	headers := []interface{}{
		text.FgHiCyan.Sprint("FIELD"),
		text.FgHiCyan.Sprint("VALUE"),
	}
	t.AppendHeader(headers)

	// Add rows
	t.AppendRow([]interface{}{"Name", text.FgHiCyan.Sprint(tool.Name)})
	t.AppendRow([]interface{}{"Description", tool.Description})

	// Format schema as JSON
	schemaBytes, _ := json.MarshalIndent(tool.InputSchema, "", "  ")
	t.AppendRow([]interface{}{"Input Schema", string(schemaBytes)})

	// Render table
	var result strings.Builder
	t.SetOutputMirror(&result)
	t.Render()

	return result.String()
}

// FormatResourceDetail formats detailed resource information
func (f *TableFormatter) FormatResourceDetail(resource mcp.Resource) string {
	t := f.createTable()

	// Set headers
	headers := []interface{}{
		text.FgHiCyan.Sprint("FIELD"),
		text.FgHiCyan.Sprint("VALUE"),
	}
	t.AppendHeader(headers)

	// Add rows
	t.AppendRow([]interface{}{"URI", text.FgHiCyan.Sprint(resource.URI)})
	t.AppendRow([]interface{}{"Name", resource.Name})
	if resource.Description != "" {
		t.AppendRow([]interface{}{"Description", resource.Description})
	}
	if resource.MIMEType != "" {
		t.AppendRow([]interface{}{"MIME Type", f.formatMimeType(resource.MIMEType)})
	}

	// Render table
	var result strings.Builder
	t.SetOutputMirror(&result)
	t.Render()

	return result.String()
}

// FormatPromptDetail formats detailed prompt information
func (f *TableFormatter) FormatPromptDetail(prompt mcp.Prompt) string {
	var result strings.Builder

	// Main info table
	t := f.createTable()
	headers := []interface{}{
		text.FgHiCyan.Sprint("FIELD"),
		text.FgHiCyan.Sprint("VALUE"),
	}
	t.AppendHeader(headers)

	t.AppendRow([]interface{}{"Name", text.FgHiCyan.Sprint(prompt.Name)})
	t.AppendRow([]interface{}{"Description", prompt.Description})

	t.SetOutputMirror(&result)
	t.Render()

	// Arguments table if present
	if len(prompt.Arguments) > 0 {
		result.WriteString("\n" + text.FgHiCyan.Sprint("Arguments:") + "\n")

		argTable := f.createTable()
		argHeaders := []interface{}{
			text.FgHiCyan.Sprint("NAME"),
			text.FgHiCyan.Sprint("REQUIRED"),
			text.FgHiCyan.Sprint("DESCRIPTION"),
		}
		argTable.AppendHeader(argHeaders)

		for _, arg := range prompt.Arguments {
			required := text.FgRed.Sprint("❌ No")
			if arg.Required {
				required = text.FgGreen.Sprint("✅ Yes")
			}

			argTable.AppendRow([]interface{}{
				text.FgHiCyan.Sprint(arg.Name),
				required,
				arg.Description,
			})
		}

		argTable.SetOutputMirror(&result)
		argTable.Render()
	}

	return result.String()
}

// FormatData formats generic data using table logic from CLI
func (f *TableFormatter) FormatData(data interface{}) error {
	switch d := data.(type) {
	case map[string]interface{}:
		return f.formatObjectData(d)
	case []interface{}:
		return f.formatArrayData(d)
	case string:
		fmt.Println(d)
	default:
		fmt.Printf("%v\n", d)
	}
	return nil
}

// FindTool finds a tool by name in the cache
func (f *TableFormatter) FindTool(tools []mcp.Tool, name string) *mcp.Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return &tool
		}
	}
	return nil
}

// FindResource finds a resource by URI in the cache
func (f *TableFormatter) FindResource(resources []mcp.Resource, uri string) *mcp.Resource {
	for _, resource := range resources {
		if resource.URI == uri {
			return &resource
		}
	}
	return nil
}

// FindPrompt finds a prompt by name in the cache
func (f *TableFormatter) FindPrompt(prompts []mcp.Prompt, name string) *mcp.Prompt {
	for _, prompt := range prompts {
		if prompt.Name == name {
			return &prompt
		}
	}
	return nil
}

// SetOptions updates the formatter options
func (f *TableFormatter) SetOptions(options Options) {
	f.options = options
}

// GetOptions returns the current formatter options
func (f *TableFormatter) GetOptions() Options {
	return f.options
}

// Helper methods

// createTable creates a new table with standard styling
func (f *TableFormatter) createTable() table.Writer {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	return t
}

// formatDescription truncates long descriptions
func (f *TableFormatter) formatDescription(desc string) string {
	if len(desc) > 50 {
		return desc[:47] + text.FgHiBlack.Sprint("...")
	}
	return desc
}

// formatMimeType formats MIME type with appropriate styling
func (f *TableFormatter) formatMimeType(mimeType string) string {
	if mimeType == "" {
		return text.FgHiBlack.Sprint("-")
	}
	return text.FgYellow.Sprint(mimeType)
}

// formatArgumentCount formats argument count with appropriate styling
func (f *TableFormatter) formatArgumentCount(count int) string {
	if count == 0 {
		return text.FgHiBlack.Sprint("None")
	}
	return text.FgGreen.Sprint(fmt.Sprintf("%d args", count))
}

// formatEmptyMessage formats empty result messages
func (f *TableFormatter) formatEmptyMessage(icon, message string) string {
	return fmt.Sprintf("%s %s\n", text.FgYellow.Sprint(icon), text.FgYellow.Sprint(message))
}

// formatObjectData formats object data as key-value pairs
func (f *TableFormatter) formatObjectData(data map[string]interface{}) error {
	t := f.createTable()

	headers := []interface{}{
		text.FgHiCyan.Sprint("KEY"),
		text.FgHiCyan.Sprint("VALUE"),
	}
	t.AppendHeader(headers)

	for key, value := range data {
		valueStr := fmt.Sprintf("%v", value)
		if len(valueStr) > 100 {
			valueStr = valueStr[:97] + "..."
		}

		t.AppendRow([]interface{}{
			text.FgHiCyan.Sprint(key),
			valueStr,
		})
	}

	t.Render()
	return nil
}

// formatArrayData formats array data as a simple table
func (f *TableFormatter) formatArrayData(data []interface{}) error {
	if len(data) == 0 {
		fmt.Printf("%s %s\n", text.FgYellow.Sprint("📋"), text.FgYellow.Sprint("No items found"))
		return nil
	}

	// Simple array display
	for i, item := range data {
		fmt.Printf("  %d. %v\n", i+1, item)
	}

	fmt.Printf("\n%s %s %s\n",
		text.FgHiBlue.Sprint("Total:"),
		text.FgHiWhite.Sprint(len(data)),
		text.FgHiBlue.Sprint("items"))

	return nil
}
