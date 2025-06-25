package formatting

import (
	"encoding/json"
	"fmt"
	"strings"

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
func (f *ConsoleFormatter) FormatToolsList(tools []mcp.Tool) string {
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
func (f *ConsoleFormatter) FormatResourcesList(resources []mcp.Resource) string {
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
func (f *ConsoleFormatter) FormatPromptsList(prompts []mcp.Prompt) string {
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
func (f *ConsoleFormatter) FormatToolDetail(tool mcp.Tool) string {
	var output []string
	output = append(output, fmt.Sprintf("Tool: %s", tool.Name))
	output = append(output, fmt.Sprintf("Description: %s", tool.Description))
	output = append(output, "Input Schema:")
	output = append(output, f.prettyJSON(tool.InputSchema))
	return strings.Join(output, "\n")
}

// FormatResourceDetail formats detailed resource information
func (f *ConsoleFormatter) FormatResourceDetail(resource mcp.Resource) string {
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
func (f *ConsoleFormatter) FormatPromptDetail(prompt mcp.Prompt) string {
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

// FormatData formats generic data (fallback to simple text representation)
func (f *ConsoleFormatter) FormatData(data interface{}) error {
	switch d := data.(type) {
	case map[string]interface{}:
		fmt.Println(f.prettyJSON(d))
	case []interface{}:
		fmt.Println(f.prettyJSON(d))
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

// prettyJSON formats JSON data with indentation
func (f *ConsoleFormatter) prettyJSON(v interface{}) string {
	jsonBytes, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error formatting JSON: %v", err)
	}
	return string(jsonBytes)
}
