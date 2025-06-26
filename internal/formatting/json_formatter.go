package formatting

import (
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// JSONFormatter provides structured JSON output formatting
type JSONFormatter struct {
	options Options
}

// NewJSONFormatter creates a new JSON formatter
func NewJSONFormatter(options Options) Formatter {
	return &JSONFormatter{
		options: options,
	}
}

// FormatToolsList formats tools list as JSON
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *JSONFormatter) FormatToolsList(tools []mcp.Tool) string {
	if len(tools) == 0 {
		return `{"tools": [], "count": 0}`
	}
	return fmt.Sprintf(`{"message": "Use agent formatters for MCP data formatting", "count": %d}`, len(tools))
}

// FormatResourcesList formats resources list as JSON
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *JSONFormatter) FormatResourcesList(resources []mcp.Resource) string {
	if len(resources) == 0 {
		return `{"resources": [], "count": 0}`
	}
	return fmt.Sprintf(`{"message": "Use agent formatters for MCP data formatting", "count": %d}`, len(resources))
}

// FormatPromptsList formats prompts list as JSON
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *JSONFormatter) FormatPromptsList(prompts []mcp.Prompt) string {
	if len(prompts) == 0 {
		return `{"prompts": [], "count": 0}`
	}
	return fmt.Sprintf(`{"message": "Use agent formatters for MCP data formatting", "count": %d}`, len(prompts))
}

// FormatToolDetail formats detailed tool information as JSON
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *JSONFormatter) FormatToolDetail(tool mcp.Tool) string {
	return fmt.Sprintf(`{"name": "%s", "message": "Use agent formatters for detailed MCP formatting"}`, tool.Name)
}

// FormatResourceDetail formats detailed resource information as JSON
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *JSONFormatter) FormatResourceDetail(resource mcp.Resource) string {
	return fmt.Sprintf(`{"uri": "%s", "message": "Use agent formatters for detailed MCP formatting"}`, resource.URI)
}

// FormatPromptDetail formats detailed prompt information as JSON
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *JSONFormatter) FormatPromptDetail(prompt mcp.Prompt) string {
	return fmt.Sprintf(`{"name": "%s", "message": "Use agent formatters for detailed MCP formatting"}`, prompt.Name)
}

// FormatData formats generic data as JSON (non-MCP specific)
func (f *JSONFormatter) FormatData(data interface{}) error {
	fmt.Println(f.marshal(data))
	return nil
}

// FindTool finds a tool by name in the cache
func (f *JSONFormatter) FindTool(tools []mcp.Tool, name string) *mcp.Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return &tool
		}
	}
	return nil
}

// FindResource finds a resource by URI in the cache
func (f *JSONFormatter) FindResource(resources []mcp.Resource, uri string) *mcp.Resource {
	for _, resource := range resources {
		if resource.URI == uri {
			return &resource
		}
	}
	return nil
}

// FindPrompt finds a prompt by name in the cache
func (f *JSONFormatter) FindPrompt(prompts []mcp.Prompt, name string) *mcp.Prompt {
	for _, prompt := range prompts {
		if prompt.Name == name {
			return &prompt
		}
	}
	return nil
}

// SetOptions updates the formatter options
func (f *JSONFormatter) SetOptions(options Options) {
	f.options = options
}

// GetOptions returns the current formatter options
func (f *JSONFormatter) GetOptions() Options {
	return f.options
}

// marshal converts data to JSON string with appropriate formatting
func (f *JSONFormatter) marshal(data interface{}) string {
	var jsonBytes []byte
	var err error

	if f.options.Quiet {
		// Compact JSON for quiet mode
		jsonBytes, err = json.Marshal(data)
	} else {
		// Use consolidated PrettyJSON for normal mode
		return PrettyJSON(data)
	}

	if err != nil {
		return fmt.Sprintf(`{"error": "Failed to format JSON: %v"}`, err)
	}

	return string(jsonBytes)
}
