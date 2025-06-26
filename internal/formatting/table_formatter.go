package formatting

import (
	"fmt"
	"os"

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
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *TableFormatter) FormatToolsList(tools []mcp.Tool) string {
	if len(tools) == 0 {
		return f.formatEmptyMessage("📋", "No tools found")
	}
	return f.formatEmptyMessage("🔧", fmt.Sprintf("Use agent formatters for MCP data formatting (%d tools)", len(tools)))
}

// FormatResourcesList formats resources list as a table
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *TableFormatter) FormatResourcesList(resources []mcp.Resource) string {
	if len(resources) == 0 {
		return f.formatEmptyMessage("📋", "No resources found")
	}
	return f.formatEmptyMessage("📄", fmt.Sprintf("Use agent formatters for MCP data formatting (%d resources)", len(resources)))
}

// FormatPromptsList formats prompts list as a table
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *TableFormatter) FormatPromptsList(prompts []mcp.Prompt) string {
	if len(prompts) == 0 {
		return f.formatEmptyMessage("📋", "No prompts found")
	}
	return f.formatEmptyMessage("💬", fmt.Sprintf("Use agent formatters for MCP data formatting (%d prompts)", len(prompts)))
}

// FormatToolDetail formats detailed tool information
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *TableFormatter) FormatToolDetail(tool mcp.Tool) string {
	return fmt.Sprintf("Tool: %s\nUse agent formatters for detailed MCP formatting", tool.Name)
}

// FormatResourceDetail formats detailed resource information
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *TableFormatter) FormatResourceDetail(resource mcp.Resource) string {
	return fmt.Sprintf("Resource: %s\nUse agent formatters for detailed MCP formatting", resource.URI)
}

// FormatPromptDetail formats detailed prompt information
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *TableFormatter) FormatPromptDetail(prompt mcp.Prompt) string {
	return fmt.Sprintf("Prompt: %s\nUse agent formatters for detailed MCP formatting", prompt.Name)
}

// FormatData formats generic data using table logic (non-MCP specific)
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
