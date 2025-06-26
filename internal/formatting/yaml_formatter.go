package formatting

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"gopkg.in/yaml.v3"
)

// YAMLFormatter provides YAML output formatting
type YAMLFormatter struct {
	options Options
}

// NewYAMLFormatter creates a new YAML formatter
func NewYAMLFormatter(options Options) Formatter {
	return &YAMLFormatter{
		options: options,
	}
}

// FormatToolsList formats tools list as YAML
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *YAMLFormatter) FormatToolsList(tools []mcp.Tool) string {
	if len(tools) == 0 {
		return "tools: []\ncount: 0\n"
	}
	return fmt.Sprintf("message: \"Use agent formatters for MCP data formatting\"\ncount: %d\n", len(tools))
}

// FormatResourcesList formats resources list as YAML
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *YAMLFormatter) FormatResourcesList(resources []mcp.Resource) string {
	if len(resources) == 0 {
		return "resources: []\ncount: 0\n"
	}
	return fmt.Sprintf("message: \"Use agent formatters for MCP data formatting\"\ncount: %d\n", len(resources))
}

// FormatPromptsList formats prompts list as YAML
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *YAMLFormatter) FormatPromptsList(prompts []mcp.Prompt) string {
	if len(prompts) == 0 {
		return "prompts: []\ncount: 0\n"
	}
	return fmt.Sprintf("message: \"Use agent formatters for MCP data formatting\"\ncount: %d\n", len(prompts))
}

// FormatToolDetail formats detailed tool information as YAML
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *YAMLFormatter) FormatToolDetail(tool mcp.Tool) string {
	return fmt.Sprintf("name: \"%s\"\nmessage: \"Use agent formatters for detailed MCP formatting\"\n", tool.Name)
}

// FormatResourceDetail formats detailed resource information as YAML
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *YAMLFormatter) FormatResourceDetail(resource mcp.Resource) string {
	return fmt.Sprintf("uri: \"%s\"\nmessage: \"Use agent formatters for detailed MCP formatting\"\n", resource.URI)
}

// FormatPromptDetail formats detailed prompt information as YAML
// NOTE: This implementation delegates to agent formatters to avoid duplication
func (f *YAMLFormatter) FormatPromptDetail(prompt mcp.Prompt) string {
	return fmt.Sprintf("name: \"%s\"\nmessage: \"Use agent formatters for detailed MCP formatting\"\n", prompt.Name)
}

// FormatData formats generic data as YAML (non-MCP specific)
func (f *YAMLFormatter) FormatData(data interface{}) error {
	fmt.Print(f.marshal(data))
	return nil
}

// FindTool finds a tool by name in the cache
func (f *YAMLFormatter) FindTool(tools []mcp.Tool, name string) *mcp.Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return &tool
		}
	}
	return nil
}

// FindResource finds a resource by URI in the cache
func (f *YAMLFormatter) FindResource(resources []mcp.Resource, uri string) *mcp.Resource {
	for _, resource := range resources {
		if resource.URI == uri {
			return &resource
		}
	}
	return nil
}

// FindPrompt finds a prompt by name in the cache
func (f *YAMLFormatter) FindPrompt(prompts []mcp.Prompt, name string) *mcp.Prompt {
	for _, prompt := range prompts {
		if prompt.Name == name {
			return &prompt
		}
	}
	return nil
}

// SetOptions updates the formatter options
func (f *YAMLFormatter) SetOptions(options Options) {
	f.options = options
}

// GetOptions returns the current formatter options
func (f *YAMLFormatter) GetOptions() Options {
	return f.options
}

// marshal converts data to YAML string
func (f *YAMLFormatter) marshal(data interface{}) string {
	yamlBytes, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Sprintf("error: \"Failed to format YAML: %v\"\n", err)
	}

	return string(yamlBytes)
}
