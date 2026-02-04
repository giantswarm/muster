package metatools

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFormatters(t *testing.T) {
	formatters := NewFormatters()
	require.NotNil(t, formatters)
}

func TestFormatters_FormatToolsListJSON(t *testing.T) {
	formatters := NewFormatters()

	t.Run("empty tools list", func(t *testing.T) {
		result, err := formatters.FormatToolsListJSON([]mcp.Tool{})
		require.NoError(t, err)
		assert.Equal(t, "No tools available", result)
	})

	t.Run("with tools", func(t *testing.T) {
		tools := []mcp.Tool{
			{Name: "tool1", Description: "First tool"},
			{Name: "tool2", Description: "Second tool"},
		}

		result, err := formatters.FormatToolsListJSON(tools)
		require.NoError(t, err)

		// Parse the result as JSON
		var parsed []map[string]string
		err = json.Unmarshal([]byte(result), &parsed)
		require.NoError(t, err)

		assert.Len(t, parsed, 2)
		assert.Equal(t, "tool1", parsed[0]["name"])
		assert.Equal(t, "First tool", parsed[0]["description"])
		assert.Equal(t, "tool2", parsed[1]["name"])
		assert.Equal(t, "Second tool", parsed[1]["description"])
	})
}

func TestFormatters_FormatResourcesListJSON(t *testing.T) {
	formatters := NewFormatters()

	t.Run("empty resources list", func(t *testing.T) {
		result, err := formatters.FormatResourcesListJSON([]mcp.Resource{})
		require.NoError(t, err)
		assert.Equal(t, "No resources available", result)
	})

	t.Run("with resources", func(t *testing.T) {
		resources := []mcp.Resource{
			{URI: "file://test.txt", Name: "test.txt", Description: "Test file", MIMEType: "text/plain"},
		}

		result, err := formatters.FormatResourcesListJSON(resources)
		require.NoError(t, err)

		var parsed []map[string]string
		err = json.Unmarshal([]byte(result), &parsed)
		require.NoError(t, err)

		assert.Len(t, parsed, 1)
		assert.Equal(t, "file://test.txt", parsed[0]["uri"])
		assert.Equal(t, "test.txt", parsed[0]["name"])
		assert.Equal(t, "Test file", parsed[0]["description"])
		assert.Equal(t, "text/plain", parsed[0]["mimeType"])
	})

	t.Run("uses name as description fallback", func(t *testing.T) {
		resources := []mcp.Resource{
			{URI: "file://test.txt", Name: "test.txt", Description: ""},
		}

		result, err := formatters.FormatResourcesListJSON(resources)
		require.NoError(t, err)

		var parsed []map[string]string
		err = json.Unmarshal([]byte(result), &parsed)
		require.NoError(t, err)

		assert.Equal(t, "test.txt", parsed[0]["description"])
	})
}

func TestFormatters_FormatPromptsListJSON(t *testing.T) {
	formatters := NewFormatters()

	t.Run("empty prompts list", func(t *testing.T) {
		result, err := formatters.FormatPromptsListJSON([]mcp.Prompt{})
		require.NoError(t, err)
		assert.Equal(t, "No prompts available", result)
	})

	t.Run("with prompts", func(t *testing.T) {
		prompts := []mcp.Prompt{
			{Name: "prompt1", Description: "First prompt"},
			{Name: "prompt2", Description: "Second prompt"},
		}

		result, err := formatters.FormatPromptsListJSON(prompts)
		require.NoError(t, err)

		var parsed []map[string]string
		err = json.Unmarshal([]byte(result), &parsed)
		require.NoError(t, err)

		assert.Len(t, parsed, 2)
		assert.Equal(t, "prompt1", parsed[0]["name"])
		assert.Equal(t, "First prompt", parsed[0]["description"])
	})
}

func TestFormatters_FormatToolDetailJSON(t *testing.T) {
	formatters := NewFormatters()

	tool := mcp.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"arg1": map[string]interface{}{
					"type":        "string",
					"description": "First argument",
				},
			},
		},
	}

	result, err := formatters.FormatToolDetailJSON(tool)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal([]byte(result), &parsed)
	require.NoError(t, err)

	assert.Equal(t, "test_tool", parsed["name"])
	assert.Equal(t, "A test tool", parsed["description"])
	assert.NotNil(t, parsed["inputSchema"])
}

func TestFormatters_FormatResourceDetailJSON(t *testing.T) {
	formatters := NewFormatters()

	resource := mcp.Resource{
		URI:         "file://test.txt",
		Name:        "test.txt",
		Description: "Test file",
		MIMEType:    "text/plain",
	}

	result, err := formatters.FormatResourceDetailJSON(resource)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal([]byte(result), &parsed)
	require.NoError(t, err)

	assert.Equal(t, "file://test.txt", parsed["uri"])
	assert.Equal(t, "test.txt", parsed["name"])
	assert.Equal(t, "Test file", parsed["description"])
	assert.Equal(t, "text/plain", parsed["mimeType"])
}

func TestFormatters_FormatPromptDetailJSON(t *testing.T) {
	formatters := NewFormatters()

	t.Run("without arguments", func(t *testing.T) {
		prompt := mcp.Prompt{
			Name:        "test_prompt",
			Description: "A test prompt",
		}

		result, err := formatters.FormatPromptDetailJSON(prompt)
		require.NoError(t, err)

		var parsed map[string]interface{}
		err = json.Unmarshal([]byte(result), &parsed)
		require.NoError(t, err)

		assert.Equal(t, "test_prompt", parsed["name"])
		assert.Equal(t, "A test prompt", parsed["description"])
		assert.Nil(t, parsed["arguments"])
	})

	t.Run("with arguments", func(t *testing.T) {
		prompt := mcp.Prompt{
			Name:        "test_prompt",
			Description: "A test prompt",
			Arguments: []mcp.PromptArgument{
				{Name: "arg1", Description: "First argument", Required: true},
				{Name: "arg2", Description: "Second argument", Required: false},
			},
		}

		result, err := formatters.FormatPromptDetailJSON(prompt)
		require.NoError(t, err)

		var parsed map[string]interface{}
		err = json.Unmarshal([]byte(result), &parsed)
		require.NoError(t, err)

		args := parsed["arguments"].([]interface{})
		assert.Len(t, args, 2)

		arg1 := args[0].(map[string]interface{})
		assert.Equal(t, "arg1", arg1["name"])
		assert.Equal(t, true, arg1["required"])
	})
}

func TestFormatters_FindTool(t *testing.T) {
	formatters := NewFormatters()

	tools := []mcp.Tool{
		{Name: "tool1", Description: "First tool"},
		{Name: "tool2", Description: "Second tool"},
		{Name: "tool3", Description: "Third tool"},
	}

	t.Run("finds existing tool", func(t *testing.T) {
		tool := formatters.FindTool(tools, "tool2")
		require.NotNil(t, tool)
		assert.Equal(t, "tool2", tool.Name)
		assert.Equal(t, "Second tool", tool.Description)
	})

	t.Run("returns nil for non-existent tool", func(t *testing.T) {
		tool := formatters.FindTool(tools, "nonexistent")
		assert.Nil(t, tool)
	})

	t.Run("case sensitive search", func(t *testing.T) {
		tool := formatters.FindTool(tools, "Tool1")
		assert.Nil(t, tool) // Should not find due to case difference
	})
}

func TestFormatters_FindResource(t *testing.T) {
	formatters := NewFormatters()

	resources := []mcp.Resource{
		{URI: "file://a.txt", Name: "a.txt"},
		{URI: "file://b.txt", Name: "b.txt"},
	}

	t.Run("finds existing resource", func(t *testing.T) {
		resource := formatters.FindResource(resources, "file://a.txt")
		require.NotNil(t, resource)
		assert.Equal(t, "file://a.txt", resource.URI)
	})

	t.Run("returns nil for non-existent resource", func(t *testing.T) {
		resource := formatters.FindResource(resources, "file://c.txt")
		assert.Nil(t, resource)
	})
}

func TestFormatters_FindPrompt(t *testing.T) {
	formatters := NewFormatters()

	prompts := []mcp.Prompt{
		{Name: "prompt1", Description: "First prompt"},
		{Name: "prompt2", Description: "Second prompt"},
	}

	t.Run("finds existing prompt", func(t *testing.T) {
		prompt := formatters.FindPrompt(prompts, "prompt1")
		require.NotNil(t, prompt)
		assert.Equal(t, "prompt1", prompt.Name)
	})

	t.Run("returns nil for non-existent prompt", func(t *testing.T) {
		prompt := formatters.FindPrompt(prompts, "nonexistent")
		assert.Nil(t, prompt)
	})
}

func TestSerializeContent(t *testing.T) {
	t.Run("serializes text content", func(t *testing.T) {
		content := []mcp.Content{
			mcp.TextContent{Type: "text", Text: "Hello world"},
		}

		result := SerializeContent(content)
		require.Len(t, result, 1)

		item := result[0].(map[string]interface{})
		assert.Equal(t, "text", item["type"])
		assert.Equal(t, "Hello world", item["text"])
	})

	t.Run("serializes image content", func(t *testing.T) {
		content := []mcp.Content{
			mcp.ImageContent{Type: "image", MIMEType: "image/png", Data: "base64data"},
		}

		result := SerializeContent(content)
		require.Len(t, result, 1)

		item := result[0].(map[string]interface{})
		assert.Equal(t, "image", item["type"])
		assert.Equal(t, "image/png", item["mimeType"])
		assert.Equal(t, 10, item["dataSize"]) // len("base64data") = 10
	})

	t.Run("serializes audio content", func(t *testing.T) {
		content := []mcp.Content{
			mcp.AudioContent{Type: "audio", MIMEType: "audio/mp3", Data: "audiodata"},
		}

		result := SerializeContent(content)
		require.Len(t, result, 1)

		item := result[0].(map[string]interface{})
		assert.Equal(t, "audio", item["type"])
		assert.Equal(t, "audio/mp3", item["mimeType"])
		assert.Equal(t, 9, item["dataSize"]) // len("audiodata") = 9
	})

	t.Run("handles empty content", func(t *testing.T) {
		result := SerializeContent([]mcp.Content{})
		assert.Empty(t, result)
	})

	t.Run("handles mixed content types", func(t *testing.T) {
		content := []mcp.Content{
			mcp.TextContent{Type: "text", Text: "Hello"},
			mcp.ImageContent{Type: "image", MIMEType: "image/jpeg", Data: "imgdata"},
		}

		result := SerializeContent(content)
		require.Len(t, result, 2)

		textItem := result[0].(map[string]interface{})
		assert.Equal(t, "text", textItem["type"])

		imageItem := result[1].(map[string]interface{})
		assert.Equal(t, "image", imageItem["type"])
	})
}
