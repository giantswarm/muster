package agent

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/metatools"
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

func TestUnwrapMetaToolResponse_StructuredContent(t *testing.T) {
	client := NewClient("http://localhost:8090/mcp", nil, TransportStreamableHTTP)

	envelope := `{
		"isError": false,
		"content": [{"type": "text", "text": "Authentication Required"}],
		"structuredContent": {"status": "auth_required", "auth_url": "https://idp.example.com/authorize"}
	}`
	wrapped := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(envelope)},
	}

	unwrapped, err := client.unwrapMetaToolResponse(wrapped, "core_auth_login")
	require.NoError(t, err)
	require.Len(t, unwrapped.Content, 1)

	structured, ok := unwrapped.StructuredContent.(map[string]any)
	require.True(t, ok, "structuredContent must survive unwrapping, got %T", unwrapped.StructuredContent)
	require.Equal(t, "auth_required", structured["status"])
	require.Equal(t, "https://idp.example.com/authorize", structured["auth_url"])
}

func TestUnwrapMetaToolResponse_NoStructuredContent(t *testing.T) {
	client := NewClient("http://localhost:8090/mcp", nil, TransportStreamableHTTP)

	envelope := `{"isError": false, "content": [{"type": "text", "text": "plain result"}]}`
	wrapped := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(envelope)},
	}

	unwrapped, err := client.unwrapMetaToolResponse(wrapped, "some_tool")
	require.NoError(t, err)
	require.Nil(t, unwrapped.StructuredContent)
}

func TestUnwrapMetaToolResponse_RichResult(t *testing.T) {
	client := NewClient("http://localhost:8090/mcp", nil, TransportStreamableHTTP)

	envelope := `{
		"isError": false,
		"content": [
			{"type": "text", "text": "rendered graph", "annotations": {"audience": ["user"], "priority": 0.9}},
			{"type": "image", "mimeType": "image/png", "dataSize": 16, "annotations": {"audience": ["user"], "priority": 0.9}},
			{"type": "audio", "mimeType": "audio/wav", "dataSize": 16},
			{"type": "resource", "resource": {"uri": "file:///report.md", "mimeType": "text/markdown", "text": "# Report"}},
			{"type": "resource_link", "uri": "file:///graph.png", "name": "graph", "mimeType": "image/png"}
		],
		"_meta": {"traceId": "abc123"}
	}`
	image := mcp.NewImageContent("aW1hZ2UtYnl0ZXM=", "image/png")
	image.Annotations = &mcp.Annotations{Audience: []mcp.Role{mcp.RoleUser}, Priority: new(0.9)}
	audio := mcp.NewAudioContent("YXVkaW8tYnl0ZXM=", "audio/wav")
	wrapped := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(envelope), image, audio},
	}

	unwrapped, err := client.unwrapMetaToolResponse(wrapped, "render_graph")
	require.NoError(t, err)

	t.Run("text annotations survive", func(t *testing.T) {
		text, ok := contentOfType[mcp.TextContent](unwrapped.Content)
		require.True(t, ok)
		assert.Equal(t, "rendered graph", text.Text)
		require.NotNil(t, text.Annotations)
		assert.Equal(t, []mcp.Role{mcp.RoleUser}, text.Annotations.Audience)
	})

	t.Run("image and audio payloads are restored", func(t *testing.T) {
		restoredImage, ok := contentOfType[mcp.ImageContent](unwrapped.Content)
		require.True(t, ok)
		assert.Equal(t, image, restoredImage)
		restoredAudio, ok := contentOfType[mcp.AudioContent](unwrapped.Content)
		require.True(t, ok)
		assert.Equal(t, audio, restoredAudio)
	})

	t.Run("embedded resources and resource links survive", func(t *testing.T) {
		resource, ok := contentOfType[mcp.EmbeddedResource](unwrapped.Content)
		require.True(t, ok)
		contents, ok := resource.Resource.(mcp.TextResourceContents)
		require.True(t, ok, "got %T", resource.Resource)
		assert.Equal(t, "# Report", contents.Text)
		link, ok := contentOfType[mcp.ResourceLink](unwrapped.Content)
		require.True(t, ok)
		assert.Equal(t, "file:///graph.png", link.URI)
	})

	t.Run("content order is kept", func(t *testing.T) {
		require.Len(t, unwrapped.Content, 5)
		for i, want := range []string{"text", "image", "audio", "resource", "resource_link"} {
			wire, err := json.Marshal(unwrapped.Content[i])
			require.NoError(t, err)
			assert.Contains(t, string(wire), `"type":"`+want+`"`)
		}
	})

	t.Run("result _meta survives", func(t *testing.T) {
		require.NotNil(t, unwrapped.Meta)
		assert.Equal(t, "abc123", unwrapped.Meta.AdditionalFields["traceId"])
	})
}

// contentOfType returns the first content item of type T.
func contentOfType[T mcp.Content](contents []mcp.Content) (T, bool) {
	for _, c := range contents {
		if typed, ok := c.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}

func TestUnwrapMetaToolResponse_SizeOnlyImageWithoutPayload(t *testing.T) {
	client := NewClient("http://localhost:8090/mcp", nil, TransportStreamableHTTP)

	// A server that sends the envelope without native items: the image has
	// nothing to restore from and is left out, the text is kept.
	envelope := `{"isError": false, "content": [{"type": "image", "mimeType": "image/png", "dataSize": 16}, {"type": "text", "text": "caption"}]}`
	wrapped := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(envelope)}}

	unwrapped, err := client.unwrapMetaToolResponse(wrapped, "render_graph")
	require.NoError(t, err)
	require.Equal(t, []mcp.Content{mcp.NewTextContent("caption")}, unwrapped.Content)
	require.Nil(t, unwrapped.Meta)
}

func TestUnwrapMetaToolResponse_DispatchedTool(t *testing.T) {
	client := NewClient("http://localhost:8090/mcp", nil, TransportStreamableHTTP)
	want := metatools.DispatchedTool{Name: "x_kubernetes_list_pods", Server: "kubernetes", ServerTool: "list_pods"}

	t.Run("joins the wrapped result's _meta", func(t *testing.T) {
		envelope := `{"isError":false,"content":[{"text":"pods","type":"text"}],"_meta":{"traceId":"abc123"},"tool":{"name":"x_kubernetes_list_pods","server":"kubernetes","serverTool":"list_pods"}}`
		unwrapped, err := client.unwrapMetaToolResponse(&mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(envelope)}}, "x_kubernetes_list_pods")
		require.NoError(t, err)
		got, ok := metatools.DispatchedToolFromMeta(unwrapped.Meta)
		require.True(t, ok)
		assert.Equal(t, want, got)
		assert.Equal(t, "abc123", unwrapped.Meta.AdditionalFields["traceId"], "the wrapped tool's _meta stays")
	})

	t.Run("without _meta of its own", func(t *testing.T) {
		envelope := `{"isError":false,"content":[{"text":"pods","type":"text"}],"tool":{"name":"x_kubernetes_list_pods","server":"kubernetes","serverTool":"list_pods"}}`
		unwrapped, err := client.unwrapMetaToolResponse(&mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(envelope)}}, "x_kubernetes_list_pods")
		require.NoError(t, err)
		got, ok := metatools.DispatchedToolFromMeta(unwrapped.Meta)
		require.True(t, ok)
		assert.Equal(t, want, got)
	})

	t.Run("a server that does not send it", func(t *testing.T) {
		envelope := `{"isError":false,"content":[{"text":"pods","type":"text"}]}`
		unwrapped, err := client.unwrapMetaToolResponse(&mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(envelope)}}, "x_kubernetes_list_pods")
		require.NoError(t, err)
		assert.Nil(t, unwrapped.Meta)
	})
}
