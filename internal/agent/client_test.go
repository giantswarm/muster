package agent

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

func TestNewClient(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/mcp", logger, TransportStreamableHTTP)

	assert.NotNil(t, client)
	assert.Equal(t, "http://localhost:8090/mcp", client.endpoint)
	assert.NotNil(t, client.logger)
	assert.NotNil(t, client.toolCache)
	assert.Equal(t, 0, len(client.toolCache))
}

func TestNewLogger(t *testing.T) {
	// Test logger creation with colors
	logger := NewLogger(true, true, false)
	assert.NotNil(t, logger)
	assert.True(t, logger.verbose)
	assert.True(t, logger.useColor)
	assert.False(t, logger.jsonRPCMode)

	// Test logger creation without colors
	logger2 := NewLogger(false, false, true)
	assert.NotNil(t, logger2)
	assert.False(t, logger2.verbose)
	assert.False(t, logger2.useColor)
	assert.True(t, logger2.jsonRPCMode)
}

func TestColorize(t *testing.T) {
	// Test with colors enabled
	logger := NewLogger(false, true, false)
	result := logger.colorize("test", colorRed)
	assert.Equal(t, colorRed+"test"+colorReset, result)

	// Test with colors disabled
	logger2 := NewLogger(false, false, false)
	result2 := logger2.colorize("test", colorRed)
	assert.Equal(t, "test", result2)
}

func TestShowToolDiff(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/mcp", logger, TransportStreamableHTTP)

	oldTools := []mcp.Tool{
		{Name: "tool1", Description: "Tool 1"},
		{Name: "tool2", Description: "Tool 2"},
	}

	newTools := []mcp.Tool{
		{Name: "tool1", Description: "Tool 1"},
		{Name: "tool3", Description: "Tool 3"},
	}

	// This test mainly ensures the function doesn't panic
	// Actual output verification would require capturing stdout
	client.showToolDiff(oldTools, newTools)
}

func TestCountTools(t *testing.T) {
	logger := NewLogger(false, false, false)

	// Test with map structure
	result1 := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{"name": "tool1"},
			map[string]interface{}{"name": "tool2"},
			map[string]interface{}{"name": "tool3"},
		},
	}
	assert.Equal(t, 3, logger.countTools(result1))

	// Test with empty tools
	result2 := map[string]interface{}{
		"tools": []interface{}{},
	}
	assert.Equal(t, 0, logger.countTools(result2))

	// Test with invalid structure
	result3 := map[string]interface{}{
		"nottools": "something",
	}
	assert.Equal(t, -1, logger.countTools(result3))
}

func TestGetToolByName(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/mcp", logger, TransportStreamableHTTP)

	// Populate tool cache
	client.toolCache = []mcp.Tool{
		{Name: "tool1", Description: "Tool 1"},
		{Name: "tool2", Description: "Tool 2"},
		{Name: "core_service_list", Description: "List services"},
	}

	// Test finding existing tool
	tool := client.GetToolByName("tool1")
	assert.NotNil(t, tool)
	assert.Equal(t, "tool1", tool.Name)
	assert.Equal(t, "Tool 1", tool.Description)

	// Test finding another existing tool
	tool2 := client.GetToolByName("core_service_list")
	assert.NotNil(t, tool2)
	assert.Equal(t, "core_service_list", tool2.Name)

	// Test finding non-existent tool
	toolNil := client.GetToolByName("nonexistent")
	assert.Nil(t, toolNil)
}

func TestGetResourceByURI(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/mcp", logger, TransportStreamableHTTP)

	// Populate resource cache
	client.resourceCache = []mcp.Resource{
		{URI: "file://config.yaml", Name: "config.yaml", Description: "Configuration file", MIMEType: "application/yaml"},
		{URI: "muster://auth/status", Name: "auth_status", Description: "Authentication status"},
	}

	// Test finding existing resource
	resource := client.GetResourceByURI("file://config.yaml")
	assert.NotNil(t, resource)
	assert.Equal(t, "file://config.yaml", resource.URI)
	assert.Equal(t, "config.yaml", resource.Name)
	assert.Equal(t, "application/yaml", resource.MIMEType)

	// Test finding non-existent resource
	resourceNil := client.GetResourceByURI("nonexistent://uri")
	assert.Nil(t, resourceNil)
}

func TestGetPromptByName(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/mcp", logger, TransportStreamableHTTP)

	// Populate prompt cache
	client.promptCache = []mcp.Prompt{
		{
			Name:        "code_review",
			Description: "Review code for quality",
			Arguments: []mcp.PromptArgument{
				{Name: "language", Description: "Programming language", Required: true},
				{Name: "style", Description: "Code style", Required: false},
			},
		},
		{
			Name:        "documentation",
			Description: "Generate documentation",
		},
	}

	// Test finding existing prompt
	prompt := client.GetPromptByName("code_review")
	assert.NotNil(t, prompt)
	assert.Equal(t, "code_review", prompt.Name)
	assert.Equal(t, "Review code for quality", prompt.Description)
	assert.Len(t, prompt.Arguments, 2)
	assert.True(t, prompt.Arguments[0].Required)

	// Test finding another existing prompt
	prompt2 := client.GetPromptByName("documentation")
	assert.NotNil(t, prompt2)
	assert.Equal(t, "documentation", prompt2.Name)

	// Test finding non-existent prompt
	promptNil := client.GetPromptByName("nonexistent")
	assert.Nil(t, promptNil)
}

func TestShowResourceDiff(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/mcp", logger, TransportStreamableHTTP)

	oldResources := []mcp.Resource{
		{URI: "file://resource1", Name: "Resource 1"},
		{URI: "file://resource2", Name: "Resource 2"},
	}

	newResources := []mcp.Resource{
		{URI: "file://resource1", Name: "Resource 1"},
		{URI: "file://resource3", Name: "Resource 3"},
	}

	// This test mainly ensures the function doesn't panic
	client.showResourceDiff(oldResources, newResources)
}

func TestShowPromptDiff(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/mcp", logger, TransportStreamableHTTP)

	oldPrompts := []mcp.Prompt{
		{Name: "prompt1", Description: "Prompt 1"},
		{Name: "prompt2", Description: "Prompt 2"},
	}

	newPrompts := []mcp.Prompt{
		{Name: "prompt1", Description: "Prompt 1"},
		{Name: "prompt3", Description: "Prompt 3"},
	}

	// This test mainly ensures the function doesn't panic
	client.showPromptDiff(oldPrompts, newPrompts)
}
