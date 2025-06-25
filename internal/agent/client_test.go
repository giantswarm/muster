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

func TestPrettyJSON(t *testing.T) {
	input := map[string]interface{}{
		"test":   "value",
		"number": 123,
	}

	result := prettyJSON(input)
	assert.Contains(t, result, "\"test\": \"value\"")
	assert.Contains(t, result, "\"number\": 123")
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
