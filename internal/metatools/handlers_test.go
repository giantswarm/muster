package metatools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/giantswarm/muster/v5/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMetaToolsHandler implements api.MetaToolsHandler for testing
type mockMetaToolsHandler struct {
	tools                []mcp.Tool
	resources            []api.ResourceOrigin
	prompts              []api.PromptOrigin
	serversRequiringAuth []api.ServerAuthInfo

	callToolResult *mcp.CallToolResult
	callToolError  error

	getResourceResult *mcp.ReadResourceResult
	getResourceError  error

	getPromptResult *mcp.GetPromptResult
	getPromptError  error
}

func (m *mockMetaToolsHandler) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	return m.tools, nil
}

func (m *mockMetaToolsHandler) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if m.callToolError != nil {
		return nil, m.callToolError
	}
	return m.callToolResult, nil
}

func (m *mockMetaToolsHandler) ListResources(ctx context.Context) ([]api.ResourceOrigin, error) {
	return m.resources, nil
}

func (m *mockMetaToolsHandler) GetResource(ctx context.Context, uri, serverName string) (*mcp.ReadResourceResult, error) {
	if m.getResourceError != nil {
		return nil, m.getResourceError
	}
	return m.getResourceResult, nil
}

func (m *mockMetaToolsHandler) ListPrompts(ctx context.Context) ([]api.PromptOrigin, error) {
	return m.prompts, nil
}

func (m *mockMetaToolsHandler) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	if m.getPromptError != nil {
		return nil, m.getPromptError
	}
	return m.getPromptResult, nil
}

func (m *mockMetaToolsHandler) ListServersRequiringAuth(ctx context.Context) []api.ServerAuthInfo {
	if m.serversRequiringAuth == nil {
		return []api.ServerAuthInfo{}
	}
	return m.serversRequiringAuth
}

// registerMockHandler registers a mock handler for testing
func registerMockHandler(mock *mockMetaToolsHandler) func() {
	api.RegisterMetaTools(mock)
	return func() {
		api.RegisterMetaTools(nil)
	}
}

func TestProvider_ExecuteTool_UnknownTool(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	result, err := provider.ExecuteTool(ctx, "unknown_tool", nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "unknown meta-tool")
}

func TestProvider_HandleListTools(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		tools: []mcp.Tool{
			{Name: "tool1", Description: "First tool"},
			{Name: "tool2", Description: "Second tool"},
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	result, err := provider.ExecuteTool(ctx, "list_tools", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	// Parse the JSON result - new format with tools and servers_requiring_auth
	content := result.Content[0].(string)
	var parsed struct {
		Tools                []map[string]string  `json:"tools"`
		ServersRequiringAuth []api.ServerAuthInfo `json:"servers_requiring_auth,omitempty"`
	}
	err = json.Unmarshal([]byte(content), &parsed)
	require.NoError(t, err)
	assert.Len(t, parsed.Tools, 2)
	assert.Empty(t, parsed.ServersRequiringAuth) // Empty since mock returns empty list
}

func TestProvider_HandleDescribeTool(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		tools: []mcp.Tool{
			{
				Name:        "test_tool",
				Description: "A test tool",
				InputSchema: mcp.ToolInputSchema{Type: "object"},
			},
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	t.Run("describes existing tool", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "describe_tool", map[string]any{
			"name": "test_tool",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)

		content := result.Content[0].(string)
		var parsed map[string]any
		err = json.Unmarshal([]byte(content), &parsed)
		require.NoError(t, err)
		assert.Equal(t, "test_tool", parsed["name"])
		assert.Contains(t, parsed["invocation"], "call_tool")
	})

	t.Run("error for missing name", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "describe_tool", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "name argument is required")
	})

	t.Run("error for non-existent tool", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "describe_tool", map[string]any{
			"name": "nonexistent",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "Tool not found")
	})
}

func TestProvider_HandleListCoreTools(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		tools: []mcp.Tool{
			{Name: "core_service_list", Description: "List services"},
			{Name: "core_workflow_get", Description: "Get workflow"},
			{Name: "x_kubernetes_pods", Description: "List pods"},
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	result, err := provider.ExecuteTool(ctx, "list_core_tools", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	content := result.Content[0].(string)
	var parsed map[string]any
	err = json.Unmarshal([]byte(content), &parsed)
	require.NoError(t, err)

	// Should only include core_ tools
	assert.Equal(t, float64(3), parsed["total_tools"])
	assert.Equal(t, float64(2), parsed["filtered_count"])

	tools := parsed["tools"].([]any)
	assert.Len(t, tools, 2)
}

func TestProvider_HandleFilterTools(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		tools: []mcp.Tool{
			{Name: "core_service_list", Description: "List services"},
			{Name: "core_workflow_get", Description: "Get workflow"},
			{Name: "x_kubernetes_pods", Description: "List pods"},
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	t.Run("filter by pattern", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "filter_tools", map[string]any{
			"pattern": "x_*",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)

		content := result.Content[0].(string)
		var parsed map[string]any
		err = json.Unmarshal([]byte(content), &parsed)
		require.NoError(t, err)

		assert.Equal(t, float64(1), parsed["filtered_count"])
	})

	t.Run("filter by description", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "filter_tools", map[string]any{
			"description_filter": "workflow",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)

		content := result.Content[0].(string)
		var parsed map[string]any
		err = json.Unmarshal([]byte(content), &parsed)
		require.NoError(t, err)

		assert.Equal(t, float64(1), parsed["filtered_count"])
	})

	t.Run("error for invalid pattern", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "filter_tools", map[string]any{
			"pattern": "[invalid",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "Invalid pattern")
	})
}

func TestProvider_HandleCallTool(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		callToolResult: &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "Success!"},
			},
			IsError: false,
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	t.Run("calls tool successfully", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "call_tool", map[string]any{
			"name":      "some_tool",
			"arguments": map[string]any{"arg1": "value1"},
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)

		// The result should be JSON preserving CallToolResult structure
		content := result.Content[0].(string)
		var parsed struct {
			IsError bool  `json:"isError"`
			Content []any `json:"content"`
		}
		err = json.Unmarshal([]byte(content), &parsed)
		require.NoError(t, err)
		assert.False(t, parsed.IsError)
	})

	t.Run("forwards underlying tool error to outer IsError", func(t *testing.T) {
		previous := mock.callToolResult
		mock.callToolResult = &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "underlying failure"},
			},
			IsError: true,
		}
		defer func() { mock.callToolResult = previous }()

		result, err := provider.ExecuteTool(ctx, "call_tool", map[string]any{
			"name": "some_tool",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError, "outer IsError must mirror the underlying tool's IsError")

		content := result.Content[0].(string)
		var parsed struct {
			IsError bool  `json:"isError"`
			Content []any `json:"content"`
		}
		err = json.Unmarshal([]byte(content), &parsed)
		require.NoError(t, err)
		assert.True(t, parsed.IsError)
	})

	t.Run("error for missing name", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "call_tool", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "name argument is required")
	})

	t.Run("error for invalid arguments type", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "call_tool", map[string]any{
			"name":      "some_tool",
			"arguments": "not-an-object",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "arguments must be a JSON object")
	})

	t.Run("propagates structured content natively and in the envelope", func(t *testing.T) {
		previous := mock.callToolResult
		mock.callToolResult = &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "Authentication Required"},
			},
			StructuredContent: map[string]any{
				"status":   "auth_required",
				"auth_url": "https://idp.example.com/authorize",
			},
		}
		defer func() { mock.callToolResult = previous }()

		result, err := provider.ExecuteTool(ctx, "call_tool", map[string]any{
			"name": "core_auth_login",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.False(t, result.IsError)

		require.Equal(t, mock.callToolResult.StructuredContent, result.StructuredContent)

		content := result.Content[0].(string)
		var parsed struct {
			IsError           bool           `json:"isError"`
			Content           []any          `json:"content"`
			StructuredContent map[string]any `json:"structuredContent"`
		}
		require.NoError(t, json.Unmarshal([]byte(content), &parsed))
		assert.Equal(t, "auth_required", parsed.StructuredContent["status"])
		assert.Equal(t, "https://idp.example.com/authorize", parsed.StructuredContent["auth_url"])
	})

	t.Run("envelope omits structuredContent when unset", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "call_tool", map[string]any{
			"name": "some_tool",
		})
		require.NoError(t, err)
		require.Nil(t, result.StructuredContent)
		assert.NotContains(t, result.Content[0].(string), "structuredContent")
	})
}

func TestProvider_HandleListResources(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		resources: []api.ResourceOrigin{
			{Resource: mcp.Resource{URI: "file://test.txt", Name: "test.txt", Description: "Test file"}, Server: "files"},
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	result, err := provider.ExecuteTool(ctx, "list_resources", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	content := result.Content[0].(string)
	var parsed []map[string]string
	err = json.Unmarshal([]byte(content), &parsed)
	require.NoError(t, err)
	assert.Len(t, parsed, 1)
	assert.Equal(t, "file://test.txt", parsed[0]["uri"])
	assert.Equal(t, "files", parsed[0]["server"], "listing must attribute each resource to its source server")
}

func TestProvider_HandleDescribeResource(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		resources: []api.ResourceOrigin{
			{Resource: mcp.Resource{URI: "file://test.txt", Name: "test.txt", Description: "Test file"}, Server: "files"},
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	t.Run("describes existing resource", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "describe_resource", map[string]any{
			"uri": "file://test.txt",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
	})

	t.Run("error for missing uri", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "describe_resource", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "uri argument is required")
	})
}

func TestProvider_HandleGetResource(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	t.Run("retrieves text resource", func(t *testing.T) {
		mock := &mockMetaToolsHandler{
			getResourceResult: &mcp.ReadResourceResult{
				Contents: []mcp.ResourceContents{
					mcp.TextResourceContents{
						URI:      "file://test.txt",
						MIMEType: "text/plain",
						Text:     "Hello, World!",
					},
				},
			},
		}
		cleanup := registerMockHandler(mock)
		defer cleanup()

		result, err := provider.ExecuteTool(ctx, "get_resource", map[string]any{
			"uri": "file://test.txt",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "Hello, World!")
	})

	t.Run("retrieves blob resource", func(t *testing.T) {
		mock := &mockMetaToolsHandler{
			getResourceResult: &mcp.ReadResourceResult{
				Contents: []mcp.ResourceContents{
					mcp.BlobResourceContents{
						URI:      "file://binary.dat",
						MIMEType: "application/octet-stream",
						Blob:     "YmluYXJ5ZGF0YQ==", // base64 encoded "binarydata"
					},
				},
			},
		}
		cleanup := registerMockHandler(mock)
		defer cleanup()

		result, err := provider.ExecuteTool(ctx, "get_resource", map[string]any{
			"uri": "file://binary.dat",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "Binary data")
	})

	t.Run("error for missing uri", func(t *testing.T) {
		mock := &mockMetaToolsHandler{}
		cleanup := registerMockHandler(mock)
		defer cleanup()

		result, err := provider.ExecuteTool(ctx, "get_resource", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "uri argument is required")
	})
}

func TestProvider_HandleListPrompts(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		prompts: []api.PromptOrigin{
			{Prompt: mcp.Prompt{Name: "prompt1", Description: "First prompt"}, Server: "alpha"},
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	result, err := provider.ExecuteTool(ctx, "list_prompts", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	content := result.Content[0].(string)
	var parsed []map[string]string
	err = json.Unmarshal([]byte(content), &parsed)
	require.NoError(t, err)
	assert.Len(t, parsed, 1)
	assert.Equal(t, "alpha", parsed[0]["server"], "listing must attribute each prompt to its source server")
}

func TestProvider_HandleDescribePrompt(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		prompts: []api.PromptOrigin{
			{Prompt: mcp.Prompt{Name: "test_prompt", Description: "Test prompt"}, Server: "alpha"},
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	t.Run("describes existing prompt", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "describe_prompt", map[string]any{
			"name": "test_prompt",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
	})

	t.Run("error for missing name", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "describe_prompt", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "name argument is required")
	})
}

func TestProvider_HandleGetPrompt(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		getPromptResult: &mcp.GetPromptResult{
			Messages: []mcp.PromptMessage{
				{
					Role:    mcp.RoleUser,
					Content: mcp.TextContent{Type: "text", Text: "Hello"},
				},
			},
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	t.Run("gets prompt successfully", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "get_prompt", map[string]any{
			"name": "test_prompt",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
	})

	t.Run("error for missing name", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "get_prompt", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "name argument is required")
	})
}

func TestTextResult(t *testing.T) {
	result := textResult("test message")
	assert.False(t, result.IsError)
	assert.Len(t, result.Content, 1)
	assert.Equal(t, "test message", result.Content[0])
}

func TestErrorResult(t *testing.T) {
	result := errorResult("error message")
	assert.True(t, result.IsError)
	assert.Len(t, result.Content, 1)
	assert.Equal(t, "error message", result.Content[0])
}

// richResult is a downstream tool result that uses every part of the MCP
// result the call_tool envelope has to carry besides text.
func richResult() *mcp.CallToolResult {
	annotations := &mcp.Annotations{Audience: []mcp.Role{mcp.RoleUser}, Priority: new(0.9)}
	text := mcp.NewTextContent("rendered graph")
	text.Annotations = annotations
	image := mcp.NewImageContent("aW1hZ2UtYnl0ZXM=", "image/png")
	image.Annotations = annotations
	return &mcp.CallToolResult{
		Result: mcp.Result{Meta: mcp.NewMetaFromMap(map[string]any{"traceId": "abc123"})},
		Content: []mcp.Content{
			text,
			image,
			mcp.NewAudioContent("YXVkaW8tYnl0ZXM=", "audio/wav"),
			mcp.NewEmbeddedResource(mcp.TextResourceContents{URI: "file:///report.md", MIMEType: "text/markdown", Text: "# Report"}),
			mcp.NewResourceLink("file:///graph.png", "graph", "the full graph", "image/png"),
		},
	}
}

func TestProvider_HandleCallTool_RichResult(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{callToolResult: richResult()}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	result, err := provider.ExecuteTool(ctx, "call_tool", map[string]any{"name": "render_graph"})
	require.NoError(t, err)
	require.False(t, result.IsError)

	var envelope struct {
		Content []map[string]any `json:"content"`
		Meta    map[string]any   `json:"_meta"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(string)), &envelope))
	require.Len(t, envelope.Content, 5)

	t.Run("result _meta is in the envelope", func(t *testing.T) {
		assert.Equal(t, "abc123", envelope.Meta["traceId"])
	})

	t.Run("content annotations are in the envelope", func(t *testing.T) {
		for _, i := range []int{0, 1} {
			annotations, ok := envelope.Content[i]["annotations"].(map[string]any)
			require.True(t, ok, "item %d lost its annotations", i)
			assert.Equal(t, []any{"user"}, annotations["audience"])
			assert.Equal(t, 0.9, annotations["priority"])
		}
	})

	t.Run("image and audio stay size-only in the envelope", func(t *testing.T) {
		assert.Equal(t, float64(len("aW1hZ2UtYnl0ZXM=")), envelope.Content[1]["dataSize"])
		assert.NotContains(t, envelope.Content[1], "data")
		assert.NotContains(t, envelope.Content[2], "data")
	})

	t.Run("image and audio payloads follow the envelope natively", func(t *testing.T) {
		require.Len(t, result.Content, 3)
		image, ok := result.Content[1].(mcp.ImageContent)
		require.True(t, ok, "expected native image content, got %T", result.Content[1])
		assert.Equal(t, "aW1hZ2UtYnl0ZXM=", image.Data)
		assert.Equal(t, "image/png", image.MIMEType)
		require.NotNil(t, image.Annotations)
		audio, ok := result.Content[2].(mcp.AudioContent)
		require.True(t, ok, "expected native audio content, got %T", result.Content[2])
		assert.Equal(t, "YXVkaW8tYnl0ZXM=", audio.Data)
	})

	t.Run("embedded resources and resource links are in the envelope", func(t *testing.T) {
		assert.Equal(t, "resource", envelope.Content[3]["type"])
		assert.Equal(t, "# Report", envelope.Content[3]["resource"].(map[string]any)["text"])
		assert.Equal(t, "resource_link", envelope.Content[4]["type"])
		assert.Equal(t, "file:///graph.png", envelope.Content[4]["uri"])
	})
}

func TestProvider_HandleCallTool_PlainEnvelopeUnchanged(t *testing.T) {
	provider := NewProvider()
	mock := &mockMetaToolsHandler{callToolResult: &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent("Success!")},
	}}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	result, err := provider.ExecuteTool(context.Background(), "call_tool", map[string]any{"name": "some_tool"})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	// The wrapped result's bytes are unchanged; the dispatched tool's
	// identity follows them as the envelope's last field.
	assert.Equal(t, `{"isError":false,"content":[{"text":"Success!","type":"text"}],"tool":{"name":"some_tool"}}`, result.Content[0].(string))
}
