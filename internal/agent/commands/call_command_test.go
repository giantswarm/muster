package commands

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

// mockClientForCall implements ClientInterface for testing
type mockClientForCall struct {
	tools          []mcp.Tool
	resources      []mcp.Resource
	prompts        []mcp.Prompt
	callToolResult *mcp.CallToolResult
	callToolError  error
}

func (m *mockClientForCall) GetToolCache() []mcp.Tool {
	return m.tools
}

func (m *mockClientForCall) GetResourceCache() []mcp.Resource {
	return m.resources
}

func (m *mockClientForCall) GetPromptCache() []mcp.Prompt {
	return m.prompts
}

func (m *mockClientForCall) RefreshToolCache(ctx context.Context) error {
	return nil
}

func (m *mockClientForCall) RefreshResourceCache(ctx context.Context) error {
	return nil
}

func (m *mockClientForCall) RefreshPromptCache(ctx context.Context) error {
	return nil
}

func (m *mockClientForCall) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if m.callToolError != nil {
		return nil, m.callToolError
	}
	if m.callToolResult != nil {
		return m.callToolResult, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: `{"status": "ok"}`,
			},
		},
	}, nil
}

func (m *mockClientForCall) GetResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	return nil, nil
}

func (m *mockClientForCall) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	return nil, nil
}

func (m *mockClientForCall) GetFormatters() interface{} {
	return &mockFormatter{}
}

// mockFormatter implements FormatterInterface
type mockFormatter struct{}

func (m *mockFormatter) FormatToolsList(tools []mcp.Tool) string             { return "" }
func (m *mockFormatter) FormatResourcesList(resources []mcp.Resource) string { return "" }
func (m *mockFormatter) FormatPromptsList(prompts []mcp.Prompt) string       { return "" }
func (m *mockFormatter) FormatToolDetail(tool mcp.Tool) string               { return "" }
func (m *mockFormatter) FormatResourceDetail(resource mcp.Resource) string   { return "" }
func (m *mockFormatter) FormatPromptDetail(prompt mcp.Prompt) string         { return "" }
func (m *mockFormatter) FindTool(tools []mcp.Tool, name string) *mcp.Tool {
	for _, t := range tools {
		if t.Name == name {
			return &t
		}
	}
	return nil
}
func (m *mockFormatter) FindResource(resources []mcp.Resource, uri string) *mcp.Resource { return nil }
func (m *mockFormatter) FindPrompt(prompts []mcp.Prompt, name string) *mcp.Prompt        { return nil }

// mockOutput implements OutputLogger
type mockOutput struct {
	messages []string
}

func (m *mockOutput) Output(format string, args ...interface{}) {
	m.messages = append(m.messages, format)
}

func (m *mockOutput) OutputLine(format string, args ...interface{}) {
	m.messages = append(m.messages, format)
}

func (m *mockOutput) Info(format string, args ...interface{}) {
	m.messages = append(m.messages, "INFO: "+format)
}

func (m *mockOutput) Debug(format string, args ...interface{}) {
	m.messages = append(m.messages, "DEBUG: "+format)
}

func (m *mockOutput) Error(format string, args ...interface{}) {
	m.messages = append(m.messages, "ERROR: "+format)
}

func (m *mockOutput) Success(format string, args ...interface{}) {
	m.messages = append(m.messages, "SUCCESS: "+format)
}

func (m *mockOutput) SetVerbose(verbose bool) {
	// no-op for mock
}

// mockTransport implements TransportInterface
type mockTransport struct{}

func (m *mockTransport) SupportsNotifications() bool {
	return false
}

func TestCallCommand_ParseKeyValueArgs(t *testing.T) {
	client := &mockClientForCall{}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	tests := []struct {
		name     string
		args     []string
		expected map[string]interface{}
	}{
		{
			name:     "simple string values",
			args:     []string{"name=value", "key=test"},
			expected: map[string]interface{}{"name": "value", "key": "test"},
		},
		{
			name:     "numeric value",
			args:     []string{"count=42"},
			expected: map[string]interface{}{"count": float64(42)},
		},
		{
			name:     "boolean values",
			args:     []string{"enabled=true", "disabled=false"},
			expected: map[string]interface{}{"enabled": true, "disabled": false},
		},
		{
			name:     "mixed values",
			args:     []string{"name=test", "count=5", "active=true"},
			expected: map[string]interface{}{"name": "test", "count": float64(5), "active": true},
		},
		{
			name:     "value with equals sign",
			args:     []string{"expr=a=b"},
			expected: map[string]interface{}{"expr": "a=b"},
		},
		{
			name:     "empty args",
			args:     []string{},
			expected: map[string]interface{}{},
		},
		{
			name:     "arg without equals",
			args:     []string{"noequals"},
			expected: map[string]interface{}{},
		},
		{
			name:     "JSON array value",
			args:     []string{`items=["a","b","c"]`},
			expected: map[string]interface{}{"items": []interface{}{"a", "b", "c"}},
		},
		{
			name:     "quoted string value with double quotes",
			args:     []string{`name="John Doe"`},
			expected: map[string]interface{}{"name": "John Doe"},
		},
		{
			name:     "quoted string value with single quotes",
			args:     []string{`name='Jane Doe'`},
			expected: map[string]interface{}{"name": "Jane Doe"},
		},
		{
			name:     "JSON map value",
			args:     []string{`config={"key":"value","nested":{"a":1}}`},
			expected: map[string]interface{}{"config": map[string]interface{}{"key": "value", "nested": map[string]interface{}{"a": float64(1)}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cmd.parseKeyValueArgs(tt.args)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCallCommand_FindTool(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "tool_one", Description: "First tool"},
		{Name: "tool_two", Description: "Second tool"},
	}
	client := &mockClientForCall{tools: tools}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	// Test finding existing tool
	tool := cmd.findTool("tool_one")
	assert.NotNil(t, tool)
	assert.Equal(t, "tool_one", tool.Name)

	// Test finding non-existing tool
	tool = cmd.findTool("nonexistent")
	assert.Nil(t, tool)
}

func TestCallCommand_GetRequiredParams(t *testing.T) {
	tool := &mcp.Tool{
		Name: "test_tool",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"required_param": map[string]interface{}{"type": "string"},
				"optional_param": map[string]interface{}{"type": "string"},
			},
			Required: []string{"required_param"},
		},
	}

	client := &mockClientForCall{}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	required := cmd.getRequiredParams(tool)
	assert.Equal(t, []string{"required_param"}, required)

	// Test with nil tool
	required = cmd.getRequiredParams(nil)
	assert.Nil(t, required)
}

func TestCallCommand_GetToolParams(t *testing.T) {
	tool := &mcp.Tool{
		Name: "test_tool",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"alpha": map[string]interface{}{"type": "string"},
				"beta":  map[string]interface{}{"type": "number"},
				"gamma": map[string]interface{}{"type": "boolean"},
			},
		},
	}

	client := &mockClientForCall{}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	params := cmd.getToolParams(tool)
	// Should be sorted alphabetically
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, params)

	// Test with nil tool
	params = cmd.getToolParams(nil)
	assert.Nil(t, params)

	// Test with nil properties
	toolNoProps := &mcp.Tool{Name: "empty"}
	params = cmd.getToolParams(toolNoProps)
	assert.Nil(t, params)
}

func TestCallCommand_Execute_WithKeyValueArgs(t *testing.T) {
	tools := []mcp.Tool{
		{
			Name:        "kubernetes_list",
			Description: "List kubernetes resources",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"resourceType": map[string]interface{}{
						"type":        "string",
						"description": "Type of resource to list",
					},
					"namespace": map[string]interface{}{
						"type":        "string",
						"description": "Namespace to list from",
					},
				},
				Required: []string{"resourceType"},
			},
		},
	}

	client := &mockClientForCall{
		tools: tools,
		callToolResult: &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: `{"pods": ["pod1", "pod2"]}`},
			},
		},
	}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	// Execute with key=value syntax
	err := cmd.Execute(context.Background(), []string{"kubernetes_list", "resourceType=pod", "namespace=default"})
	assert.NoError(t, err)

	// Should show "Executing tool" message
	foundExecution := false
	for _, msg := range output.messages {
		if msg == "INFO: Executing tool: %s..." {
			foundExecution = true
			break
		}
	}
	assert.True(t, foundExecution, "Should show execution message")
}

func TestCallCommand_Execute_WithJSONArgs(t *testing.T) {
	tools := []mcp.Tool{
		{
			Name:        "test_tool",
			Description: "Test tool",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"param1": map[string]interface{}{"type": "string"},
				},
			},
		},
	}

	client := &mockClientForCall{
		tools: tools,
		callToolResult: &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: `{"result": "success"}`},
			},
		},
	}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	// Execute with JSON syntax
	err := cmd.Execute(context.Background(), []string{"test_tool", `{"param1": "value1"}`})
	assert.NoError(t, err)
}

func TestCallCommand_Execute_ShowsHelpForRequiredParams(t *testing.T) {
	tools := []mcp.Tool{
		{
			Name:        "test_tool",
			Description: "Test tool with required params",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"required_param": map[string]interface{}{
						"type":        "string",
						"description": "A required parameter",
					},
				},
				Required: []string{"required_param"},
			},
		},
	}

	client := &mockClientForCall{tools: tools}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	// Execute without arguments - should show help
	err := cmd.Execute(context.Background(), []string{"test_tool"})
	assert.NoError(t, err)

	// Should show tool info, not execute
	foundParameters := false
	foundRequiredMarker := false
	for _, msg := range output.messages {
		if msg == "Parameters:" {
			foundParameters = true
		}
		if msg == "  * = required parameter" {
			foundRequiredMarker = true
		}
	}
	assert.True(t, foundParameters, "Should show parameter help when required params missing")
	assert.True(t, foundRequiredMarker, "Should show required parameter legend")
}

func TestCallCommand_Execute_ShowsHintForInvalidJSON(t *testing.T) {
	tools := []mcp.Tool{
		{
			Name:        "test_tool",
			Description: "Test tool",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"param1": map[string]interface{}{"type": "string"},
				},
			},
		},
	}

	client := &mockClientForCall{tools: tools}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	// Execute with invalid JSON syntax
	err := cmd.Execute(context.Background(), []string{"test_tool", `{"param1": "value1",}`})
	assert.NoError(t, err)

	// Should show error and hint
	foundHint := false
	for _, msg := range output.messages {
		if msg == "Hint: Did you mean to use key=value syntax instead?" {
			foundHint = true
			break
		}
	}
	assert.True(t, foundHint, "Should show hint about key=value syntax")
}

func TestCallCommand_Execute_ShowsNoOutputMessage(t *testing.T) {
	tools := []mcp.Tool{
		{
			Name: "empty_tool",
			InputSchema: mcp.ToolInputSchema{
				Type:       "object",
				Properties: map[string]interface{}{},
			},
		},
	}

	client := &mockClientForCall{
		tools: tools,
		callToolResult: &mcp.CallToolResult{
			Content: []mcp.Content{}, // Empty result
		},
	}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	// Execute tool that returns no content
	err := cmd.Execute(context.Background(), []string{"empty_tool"})
	assert.NoError(t, err)

	// Should show "no output returned" message
	foundNoOutput := false
	for _, msg := range output.messages {
		if msg == "  (no output returned)" {
			foundNoOutput = true
			break
		}
	}
	assert.True(t, foundNoOutput, "Should show no output message when result is empty")
}

func TestCallCommand_Completions(t *testing.T) {
	tools := []mcp.Tool{
		{
			Name: "tool_alpha",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"param1": map[string]interface{}{"type": "string"},
					"param2": map[string]interface{}{"type": "number"},
				},
			},
		},
		{
			Name: "tool_beta",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"option": map[string]interface{}{"type": "boolean"},
				},
			},
		},
	}

	client := &mockClientForCall{tools: tools}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	// Test tool name completions
	completions := cmd.Completions("call")
	assert.Contains(t, completions, "tool_alpha")
	assert.Contains(t, completions, "tool_beta")

	// Test parameter completions for a specific tool
	completions = cmd.Completions("call tool_alpha")
	assert.Contains(t, completions, "param1=")
	assert.Contains(t, completions, "param2=")

	// Test parameter completions for another tool
	completions = cmd.Completions("call tool_beta")
	assert.Contains(t, completions, "option=")
}

func TestCallCommand_Usage(t *testing.T) {
	client := &mockClientForCall{}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	usage := cmd.Usage()
	assert.Contains(t, usage, "key=value")
	assert.Contains(t, usage, "JSON")
}

func TestCallCommand_Aliases(t *testing.T) {
	client := &mockClientForCall{}
	output := &mockOutput{}
	transport := &mockTransport{}
	cmd := NewCallCommand(client, output, transport)

	aliases := cmd.Aliases()
	assert.Contains(t, aliases, "run")
	assert.Contains(t, aliases, "exec")
}
