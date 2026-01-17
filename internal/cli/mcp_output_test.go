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
