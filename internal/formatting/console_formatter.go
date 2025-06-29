package formatting

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// ConsoleFormatter provides simple console output formatting
type ConsoleFormatter struct {
	options Options
}

// NewConsoleFormatter creates a new console formatter
func NewConsoleFormatter(options Options) Formatter {
	return &ConsoleFormatter{
		options: options,
	}
}

// FormatToolsList formats tools list for console output
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *ConsoleFormatter) FormatToolsList(tools []mcp.Tool) string {
	// This functionality is provided by agent.Formatters which is actively used
	// We keep this stub for interface compatibility
	if len(tools) == 0 {
		return "No tools available."
	}
	return fmt.Sprintf("Use agent formatters for MCP data formatting (%d tools)", len(tools))
}

// FormatResourcesList formats resources list for console output
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *ConsoleFormatter) FormatResourcesList(resources []mcp.Resource) string {
	// This functionality is provided by agent.Formatters which is actively used
	// We keep this stub for interface compatibility
	if len(resources) == 0 {
		return "No resources available."
	}
	return fmt.Sprintf("Use agent formatters for MCP data formatting (%d resources)", len(resources))
}

// FormatPromptsList formats prompts list for console output
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *ConsoleFormatter) FormatPromptsList(prompts []mcp.Prompt) string {
	// This functionality is provided by agent.Formatters which is actively used
	// We keep this stub for interface compatibility
	if len(prompts) == 0 {
		return "No prompts available."
	}
	return fmt.Sprintf("Use agent formatters for MCP data formatting (%d prompts)", len(prompts))
}

// FormatToolDetail formats detailed tool information
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *ConsoleFormatter) FormatToolDetail(tool mcp.Tool) string {
	return fmt.Sprintf("Tool: %s\nUse agent formatters for detailed MCP formatting", tool.Name)
}

// FormatResourceDetail formats detailed resource information
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *ConsoleFormatter) FormatResourceDetail(resource mcp.Resource) string {
	return fmt.Sprintf("Resource: %s\nUse agent formatters for detailed MCP formatting", resource.URI)
}

// FormatPromptDetail formats detailed prompt information
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *ConsoleFormatter) FormatPromptDetail(prompt mcp.Prompt) string {
	return fmt.Sprintf("Prompt: %s\nUse agent formatters for detailed MCP formatting", prompt.Name)
}

// FormatData formats generic data (non-MCP specific)
func (f *ConsoleFormatter) FormatData(data interface{}) error {
	switch d := data.(type) {
	case map[string]interface{}:
		fmt.Println(PrettyJSON(d))
	case []interface{}:
		fmt.Println(PrettyJSON(d))
	case string:
		fmt.Println(d)
	default:
		fmt.Printf("%v\n", d)
	}
	return nil
}

// FindTool finds a tool by name in the cache
func (f *ConsoleFormatter) FindTool(tools []mcp.Tool, name string) *mcp.Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return &tool
		}
	}
	return nil
}

// FindResource finds a resource by URI in the cache
func (f *ConsoleFormatter) FindResource(resources []mcp.Resource, uri string) *mcp.Resource {
	for _, resource := range resources {
		if resource.URI == uri {
			return &resource
		}
	}
	return nil
}

// FindPrompt finds a prompt by name in the cache
func (f *ConsoleFormatter) FindPrompt(prompts []mcp.Prompt, name string) *mcp.Prompt {
	for _, prompt := range prompts {
		if prompt.Name == name {
			return &prompt
		}
	}
	return nil
}

// SetOptions updates the formatter options
func (f *ConsoleFormatter) SetOptions(options Options) {
	f.options = options
}

// GetOptions returns the current formatter options
func (f *ConsoleFormatter) GetOptions() Options {
	return f.options
}
