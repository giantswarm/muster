package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "short string unchanged",
			input:    "hello",
			maxLen:   10,
			expected: "hello",
		},
		{
			name:     "exact length unchanged",
			input:    "hello",
			maxLen:   5,
			expected: "hello",
		},
		{
			name:     "long string truncated",
			input:    "hello world",
			maxLen:   8,
			expected: "hello...",
		},
		{
			name:     "empty string",
			input:    "",
			maxLen:   10,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOutputJSON(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	data := map[string]string{"name": "test", "value": "123"}
	err := outputJSON(data)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, `"name": "test"`)
	assert.Contains(t, output, `"value": "123"`)
}

func TestOutputYAML(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	data := map[string]string{"name": "test", "value": "123"}
	err := outputYAML(data)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, "name: test")
	assert.Contains(t, output, "value: \"123\"")
}

func TestFormatMCPTools_Empty(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := FormatMCPTools([]MCPTool{}, OutputFormatTable)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	assert.Contains(t, buf.String(), "No tools found")
}

func TestFormatMCPTools_JSON(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	tools := []MCPTool{
		{Name: "tool_a", Description: "Tool A description"},
		{Name: "tool_b", Description: "Tool B description"},
	}
	err := FormatMCPTools(tools, OutputFormatJSON)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	// Should be sorted by name
	assert.True(t, strings.Index(output, "tool_a") < strings.Index(output, "tool_b"))
	assert.Contains(t, output, `"name": "tool_a"`)
	assert.Contains(t, output, `"description": "Tool A description"`)
}

func TestFormatMCPTools_YAML(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	tools := []MCPTool{
		{Name: "tool_a", Description: "Tool A"},
	}
	err := FormatMCPTools(tools, OutputFormatYAML)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, "name: tool_a")
	assert.Contains(t, output, "description: Tool A")
}

func TestFormatMCPResources_Empty(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := FormatMCPResources([]MCPResource{}, OutputFormatTable)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	assert.Contains(t, buf.String(), "No resources found")
}

func TestFormatMCPResources_JSON(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	resources := []MCPResource{
		{URI: "file://test.txt", Name: "test.txt", Description: "A test file", MIMEType: "text/plain"},
	}
	err := FormatMCPResources(resources, OutputFormatJSON)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, `"uri": "file://test.txt"`)
	assert.Contains(t, output, `"mimeType": "text/plain"`)
}

func TestFormatMCPPrompts_Empty(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := FormatMCPPrompts([]MCPPrompt{}, OutputFormatTable)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	assert.Contains(t, buf.String(), "No prompts found")
}

func TestFormatMCPPrompts_JSON(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	prompts := []MCPPrompt{
		{Name: "prompt_a", Description: "Prompt A description"},
	}
	err := FormatMCPPrompts(prompts, OutputFormatJSON)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, `"name": "prompt_a"`)
	assert.Contains(t, output, `"description": "Prompt A description"`)
}

func TestFormatMCPToolDetail_JSON(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	tool := MCPTool{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: mcp.ToolInputSchema{
			Properties: map[string]interface{}{
				"param1": map[string]string{"type": "string"},
			},
		},
	}
	err := FormatMCPToolDetail(tool, OutputFormatJSON)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, `"name": "test_tool"`)
	assert.Contains(t, output, `"inputSchema"`)
}

func TestFormatMCPToolDetail_YAML(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	tool := MCPTool{
		Name:        "test_tool",
		Description: "A test tool",
	}
	err := FormatMCPToolDetail(tool, OutputFormatYAML)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, "name: test_tool")
	assert.Contains(t, output, "description: A test tool")
}

func TestFormatMCPResourceDetail_JSON(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	resource := MCPResource{
		URI:         "file://test.txt",
		Name:        "test.txt",
		Description: "A test file",
		MIMEType:    "text/plain",
	}
	err := FormatMCPResourceDetail(resource, OutputFormatJSON)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, `"uri": "file://test.txt"`)
	assert.Contains(t, output, `"mimeType": "text/plain"`)
}

func TestFormatMCPPromptDetail_JSON(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	prompt := MCPPrompt{
		Name:        "test_prompt",
		Description: "A test prompt",
		Arguments: []mcp.PromptArgument{
			{Name: "arg1", Description: "First argument", Required: true},
			{Name: "arg2", Description: "Second argument", Required: false},
		},
	}
	err := FormatMCPPromptDetail(prompt, OutputFormatJSON)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, `"name": "test_prompt"`)
	assert.Contains(t, output, `"arguments"`)
	assert.Contains(t, output, `"required": true`)
}

func TestFormatMCPPromptDetail_Table(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	prompt := MCPPrompt{
		Name:        "test_prompt",
		Description: "A test prompt",
		Arguments: []mcp.PromptArgument{
			{Name: "arg1", Description: "First argument", Required: true},
		},
	}
	err := FormatMCPPromptDetail(prompt, OutputFormatTable)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	// Check kubectl-like format
	assert.Contains(t, output, "Name:")
	assert.Contains(t, output, "test_prompt")
	assert.Contains(t, output, "Arguments:")
	assert.Contains(t, output, "arg1 (string, required)")
}

func TestFormatMCPToolDetail_Table(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	tool := MCPTool{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: mcp.ToolInputSchema{
			Properties: map[string]interface{}{
				"param1": map[string]interface{}{
					"type":        "string",
					"description": "First parameter",
				},
				"param2": map[string]interface{}{
					"type": "integer",
				},
			},
			Required: []string{"param1"},
		},
	}
	err := FormatMCPToolDetail(tool, OutputFormatTable)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	// Check kubectl-like format
	assert.Contains(t, output, "Name:")
	assert.Contains(t, output, "test_tool")
	assert.Contains(t, output, "Description:")
	assert.Contains(t, output, "A test tool")
	assert.Contains(t, output, "Arguments:")
	assert.Contains(t, output, "param1 (string, required)")
	assert.Contains(t, output, "param2 (integer)")
	assert.Contains(t, output, "Description: First parameter")
}

func TestFormatMCPResourceDetail_Table(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	resource := MCPResource{
		URI:         "file://test.txt",
		Name:        "test.txt",
		Description: "A test file",
		MIMEType:    "text/plain",
	}
	err := FormatMCPResourceDetail(resource, OutputFormatTable)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	// Check kubectl-like format
	assert.Contains(t, output, "URI:")
	assert.Contains(t, output, "file://test.txt")
	assert.Contains(t, output, "Name:")
	assert.Contains(t, output, "test.txt")
	assert.Contains(t, output, "Description:")
	assert.Contains(t, output, "A test file")
	assert.Contains(t, output, "MIME Type:")
	assert.Contains(t, output, "text/plain")
}

func TestWrapText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		width    int
		expected []string
	}{
		{
			name:     "short text unchanged",
			input:    "hello world",
			width:    20,
			expected: []string{"hello world"},
		},
		{
			name:     "text wrapped at word boundary",
			input:    "hello world this is a test",
			width:    12,
			expected: []string{"hello world", "this is a", "test"},
		},
		{
			name:     "empty text",
			input:    "",
			width:    10,
			expected: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapText(tt.input, tt.width)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRenderSchemaProperties(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	properties := map[string]interface{}{
		"name": map[string]interface{}{
			"type":        "string",
			"description": "The name of the item",
		},
		"count": map[string]interface{}{
			"type":    "integer",
			"default": 10,
		},
		"type": map[string]interface{}{
			"type": "string",
			"enum": []interface{}{"a", "b", "c"},
		},
	}
	required := []string{"name"}

	renderSchemaProperties(properties, required, "  ")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	output := buf.String()
	assert.Contains(t, output, "name (string, required)")
	assert.Contains(t, output, "Description: The name of the item")
	assert.Contains(t, output, "count (integer)")
	assert.Contains(t, output, "Default: 10")
	assert.Contains(t, output, "type (string)")
	assert.Contains(t, output, "Values: a, b, c")
}

func TestRenderNestedSchema(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	properties := map[string]interface{}{
		"config": map[string]interface{}{
			"type":        "object",
			"description": "Configuration object",
			"properties": map[string]interface{}{
				"enabled": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether feature is enabled",
				},
			},
			"required": []interface{}{"enabled"},
		},
	}

	renderSchemaProperties(properties, nil, "  ")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	output := buf.String()
	assert.Contains(t, output, "config (object)")
	assert.Contains(t, output, "Properties:")
	assert.Contains(t, output, "enabled (boolean, required)")
}

func TestFormatMCPTools_Wide(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	tools := []MCPTool{
		{
			Name:        "core_service_list",
			Description: "List all services",
			InputSchema: mcp.ToolInputSchema{
				Properties: map[string]interface{}{
					"filter": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			Name:        "github_create_issue",
			Description: "Create a GitHub issue",
			InputSchema: mcp.ToolInputSchema{
				Properties: map[string]interface{}{
					"title": map[string]interface{}{"type": "string"},
					"body":  map[string]interface{}{"type": "string"},
				},
				Required: []string{"title"},
			},
		},
	}
	err := FormatMCPToolsWithOptions(tools, OutputFormatWide, false)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	// Wide format should have SERVER and ARGS columns
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "DESCRIPTION")
	assert.Contains(t, output, "SERVER")
	assert.Contains(t, output, "ARGS")
	// Check server extraction
	assert.Contains(t, output, "core")
	assert.Contains(t, output, "github")
	// Check arg counts
	assert.Contains(t, output, "1")         // core_service_list has 1 arg
	assert.Contains(t, output, "2 (1 req)") // github_create_issue has 2 args, 1 required
}

func TestFormatMCPResources_Wide(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	resources := []MCPResource{
		{
			URI:         "file://test.txt",
			Name:        "test.txt",
			Description: "A test file",
			MIMEType:    "text/plain",
		},
	}
	err := FormatMCPResourcesWithOptions(resources, OutputFormatWide, false)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	// Wide format should have NAME column
	assert.Contains(t, output, "URI")
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "DESCRIPTION")
	assert.Contains(t, output, "MIME TYPE")
	assert.Contains(t, output, "test.txt")
}

func TestFormatMCPResources_Wide_LongNameTruncation(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	longName := "This is a very long name that exceeds the normal display width and should be truncated to prevent layout issues"
	resources := []MCPResource{
		{
			URI:         "auth://status",
			Name:        longName,
			Description: "A short description",
			MIMEType:    "application/json",
		},
	}
	err := FormatMCPResourcesWithOptions(resources, OutputFormatWide, false)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	// Wide format should truncate long NAME values
	assert.Contains(t, output, "...")
	// Should NOT contain the full long name
	assert.NotContains(t, output, longName)
}

func TestFormatMCPPrompts_Wide(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	prompts := []MCPPrompt{
		{
			Name:        "test_prompt",
			Description: "A test prompt",
			Arguments: []mcp.PromptArgument{
				{Name: "arg1", Description: "First argument", Required: true},
				{Name: "arg2", Description: "Second argument", Required: false},
			},
		},
	}
	err := FormatMCPPromptsWithOptions(prompts, OutputFormatWide, false)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	assert.NoError(t, err)
	output := buf.String()
	// Wide format should have ARGS column
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "DESCRIPTION")
	assert.Contains(t, output, "ARGS")
	assert.Contains(t, output, "2 (1 req)") // 2 args, 1 required
}

func TestExtractServerFromToolName(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected string
	}{
		{
			name:     "core prefix",
			toolName: "core_service_list",
			expected: "core",
		},
		{
			name:     "mcp prefix",
			toolName: "mcp_github_create_issue",
			expected: "mcp",
		},
		{
			name:     "workflow prefix",
			toolName: "workflow_deploy_app",
			expected: "workflow",
		},
		{
			name:     "action prefix",
			toolName: "action_run_test",
			expected: "action",
		},
		{
			name:     "custom server prefix",
			toolName: "github_create_issue",
			expected: "github",
		},
		{
			name:     "no underscore",
			toolName: "simpletool",
			expected: "-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractServerFromToolName(tt.toolName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCountToolArgs(t *testing.T) {
	tests := []struct {
		name     string
		tool     MCPTool
		expected string
	}{
		{
			name: "no properties",
			tool: MCPTool{
				Name:        "test",
				InputSchema: mcp.ToolInputSchema{},
			},
			expected: "-",
		},
		{
			name: "empty properties",
			tool: MCPTool{
				Name: "test",
				InputSchema: mcp.ToolInputSchema{
					Properties: map[string]interface{}{},
				},
			},
			expected: "-",
		},
		{
			name: "properties with no required",
			tool: MCPTool{
				Name: "test",
				InputSchema: mcp.ToolInputSchema{
					Properties: map[string]interface{}{
						"arg1": map[string]string{"type": "string"},
						"arg2": map[string]string{"type": "string"},
					},
				},
			},
			expected: "2",
		},
		{
			name: "properties with required",
			tool: MCPTool{
				Name: "test",
				InputSchema: mcp.ToolInputSchema{
					Properties: map[string]interface{}{
						"arg1": map[string]string{"type": "string"},
						"arg2": map[string]string{"type": "string"},
						"arg3": map[string]string{"type": "string"},
					},
					Required: []string{"arg1", "arg2"},
				},
			},
			expected: "3 (2 req)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := countToolArgs(tt.tool)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCountPromptArgs(t *testing.T) {
	tests := []struct {
		name     string
		prompt   MCPPrompt
		expected string
	}{
		{
			name: "no arguments",
			prompt: MCPPrompt{
				Name: "test",
			},
			expected: "-",
		},
		{
			name: "arguments with no required",
			prompt: MCPPrompt{
				Name: "test",
				Arguments: []mcp.PromptArgument{
					{Name: "arg1", Required: false},
					{Name: "arg2", Required: false},
				},
			},
			expected: "2",
		},
		{
			name: "arguments with required",
			prompt: MCPPrompt{
				Name: "test",
				Arguments: []mcp.PromptArgument{
					{Name: "arg1", Required: true},
					{Name: "arg2", Required: false},
					{Name: "arg3", Required: true},
				},
			},
			expected: "3 (2 req)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := countPromptArgs(tt.prompt)
			assert.Equal(t, tt.expected, result)
		})
	}
}
