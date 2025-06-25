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
func (f *YAMLFormatter) FormatToolsList(tools []mcp.Tool) string {
	if len(tools) == 0 {
		return "tools: []\ncount: 0\n"
	}

	type ToolInfo struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}

	toolList := make([]ToolInfo, len(tools))
	for i, tool := range tools {
		toolList[i] = ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
		}
	}

	result := map[string]interface{}{
		"tools": toolList,
		"count": len(tools),
	}

	return f.marshal(result)
}

// FormatResourcesList formats resources list as YAML
func (f *YAMLFormatter) FormatResourcesList(resources []mcp.Resource) string {
	if len(resources) == 0 {
		return "resources: []\ncount: 0\n"
	}

	type ResourceInfo struct {
		URI         string `yaml:"uri"`
		Name        string `yaml:"name"`
		Description string `yaml:"description,omitempty"`
		MIMEType    string `yaml:"mimeType,omitempty"`
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

	result := map[string]interface{}{
		"resources": resourceList,
		"count":     len(resources),
	}

	return f.marshal(result)
}

// FormatPromptsList formats prompts list as YAML
func (f *YAMLFormatter) FormatPromptsList(prompts []mcp.Prompt) string {
	if len(prompts) == 0 {
		return "prompts: []\ncount: 0\n"
	}

	type PromptInfo struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}

	promptList := make([]PromptInfo, len(prompts))
	for i, prompt := range prompts {
		promptList[i] = PromptInfo{
			Name:        prompt.Name,
			Description: prompt.Description,
		}
	}

	result := map[string]interface{}{
		"prompts": promptList,
		"count":   len(prompts),
	}

	return f.marshal(result)
}

// FormatToolDetail formats detailed tool information as YAML
func (f *YAMLFormatter) FormatToolDetail(tool mcp.Tool) string {
	toolInfo := map[string]interface{}{
		"name":        tool.Name,
		"description": tool.Description,
		"inputSchema": tool.InputSchema,
	}

	return f.marshal(toolInfo)
}

// FormatResourceDetail formats detailed resource information as YAML
func (f *YAMLFormatter) FormatResourceDetail(resource mcp.Resource) string {
	resourceInfo := map[string]interface{}{
		"uri":         resource.URI,
		"name":        resource.Name,
		"description": resource.Description,
		"mimeType":    resource.MIMEType,
	}

	return f.marshal(resourceInfo)
}

// FormatPromptDetail formats detailed prompt information as YAML
func (f *YAMLFormatter) FormatPromptDetail(prompt mcp.Prompt) string {
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

	return f.marshal(promptInfo)
}

// FormatData formats generic data as YAML
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
