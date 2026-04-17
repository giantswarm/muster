package metatools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMetaToolsHandler implements api.MetaToolsHandler for testing
type mockMetaToolsHandler struct {
	tools     []mcp.Tool
	resources []mcp.Resource
	prompts   []mcp.Prompt

	callToolResult *mcp.CallToolResult
	callToolError  error

	getResourceResult *mcp.ReadResourceResult
	getResourceError  error

	getPromptResult *mcp.GetPromptResult
	getPromptError  error

	// resolveToolName, when set, provides the reverse-lookup used by
	// handleListTools to group duplicates. Nil means "unresolvable" — every
	// tool is treated as a standalone entry.
	resolveToolName func(exposedName string) (serverName, originalName string, ok bool)
}

func (m *mockMetaToolsHandler) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	return m.tools, nil
}

func (m *mockMetaToolsHandler) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if m.callToolError != nil {
		return nil, m.callToolError
	}
	return m.callToolResult, nil
}

func (m *mockMetaToolsHandler) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	return m.resources, nil
}

func (m *mockMetaToolsHandler) GetResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	if m.getResourceError != nil {
		return nil, m.getResourceError
	}
	return m.getResourceResult, nil
}

func (m *mockMetaToolsHandler) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	return m.prompts, nil
}

func (m *mockMetaToolsHandler) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	if m.getPromptError != nil {
		return nil, m.getPromptError
	}
	return m.getPromptResult, nil
}

func (m *mockMetaToolsHandler) ListServersRequiringAuth(ctx context.Context) []api.ServerAuthInfo {
	return []api.ServerAuthInfo{}
}

func (m *mockMetaToolsHandler) ResolveToolName(exposedName string) (serverName, originalName string, ok bool) {
	if m.resolveToolName == nil {
		return "", "", false
	}
	return m.resolveToolName(exposedName)
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
		Tools []struct {
			Name          string   `json:"name"`
			Description   string   `json:"description"`
			Installations []string `json:"installations,omitempty"`
		} `json:"tools"`
		ServersRequiringAuth []api.ServerAuthInfo `json:"servers_requiring_auth,omitempty"`
	}
	err = json.Unmarshal([]byte(content), &parsed)
	require.NoError(t, err)
	assert.Len(t, parsed.Tools, 2)
	// With no resolver configured, tools are emitted as concrete standalone
	// entries (no installations field).
	for _, tool := range parsed.Tools {
		assert.Empty(t, tool.Installations)
	}
	assert.Empty(t, parsed.ServersRequiringAuth) // Empty since mock returns empty list
}

func TestProvider_HandleListTools_DeduplicatesByInstallation(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	// Two installations expose identical prometheus tools; one-off runbooks
	// tool has a single provider; core tools never resolve.
	backends := map[string]struct{ serverName, originalName string }{
		"x_gazelle-mcp-prometheus_check_ready":   {"gazelle-mcp-prometheus", "check_ready"},
		"x_galaxy-mcp-prometheus_check_ready":    {"galaxy-mcp-prometheus", "check_ready"},
		"x_gazelle-mcp-prometheus_execute_query": {"gazelle-mcp-prometheus", "execute_query"},
		"x_galaxy-mcp-prometheus_execute_query":  {"galaxy-mcp-prometheus", "execute_query"},
		"x_gazelle-mcp-runbooks_list_runbooks":   {"gazelle-mcp-runbooks", "list_runbooks"},
	}

	mock := &mockMetaToolsHandler{
		tools: []mcp.Tool{
			{Name: "x_gazelle-mcp-prometheus_check_ready", Description: "Check readiness"},
			{Name: "x_galaxy-mcp-prometheus_check_ready", Description: "Check readiness"},
			{Name: "x_gazelle-mcp-prometheus_execute_query", Description: "Run a PromQL query"},
			{Name: "x_galaxy-mcp-prometheus_execute_query", Description: "Run a PromQL query"},
			{Name: "x_gazelle-mcp-runbooks_list_runbooks", Description: "List runbooks"},
			{Name: "core_auth_login", Description: "Login to a server"},
		},
		resolveToolName: func(exposedName string) (serverName, originalName string, ok bool) {
			if b, present := backends[exposedName]; present {
				return b.serverName, b.originalName, true
			}
			return "", "", false
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	result, err := provider.ExecuteTool(ctx, "list_tools", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	content := result.Content[0].(string)
	var parsed struct {
		Tools []struct {
			Name          string   `json:"name"`
			Description   string   `json:"description"`
			Installations []string `json:"installations,omitempty"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal([]byte(content), &parsed))

	byName := make(map[string]struct {
		description   string
		installations []string
	})
	for _, tool := range parsed.Tools {
		byName[tool.Name] = struct {
			description   string
			installations []string
		}{tool.Description, tool.Installations}
	}

	// 2 prometheus patterns + 1 runbooks (single installation) + 1 core = 4 entries.
	assert.Len(t, parsed.Tools, 4, "expected dedup to collapse 4 prometheus entries into 2 patterns")

	checkReady, ok := byName["x_<installation>-mcp-prometheus_check_ready"]
	require.True(t, ok, "dedup pattern for check_ready missing")
	assert.Equal(t, []string{"galaxy", "gazelle"}, checkReady.installations)

	execQuery, ok := byName["x_<installation>-mcp-prometheus_execute_query"]
	require.True(t, ok, "dedup pattern for execute_query missing")
	assert.Equal(t, []string{"galaxy", "gazelle"}, execQuery.installations)

	runbooks, ok := byName["x_gazelle-mcp-runbooks_list_runbooks"]
	require.True(t, ok, "single-installation tool should retain its concrete name")
	assert.Empty(t, runbooks.installations)

	core, ok := byName["core_auth_login"]
	require.True(t, ok, "core tool should retain its concrete name")
	assert.Empty(t, core.installations)
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
		result, err := provider.ExecuteTool(ctx, "describe_tool", map[string]interface{}{
			"name": "test_tool",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)

		content := result.Content[0].(string)
		var parsed map[string]interface{}
		err = json.Unmarshal([]byte(content), &parsed)
		require.NoError(t, err)
		assert.Equal(t, "test_tool", parsed["name"])
	})

	t.Run("error for missing name", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "describe_tool", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "name argument is required")
	})

	t.Run("error for non-existent tool", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "describe_tool", map[string]interface{}{
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
	var parsed map[string]interface{}
	err = json.Unmarshal([]byte(content), &parsed)
	require.NoError(t, err)

	// Should only include core_ tools
	assert.Equal(t, float64(3), parsed["total_tools"])
	assert.Equal(t, float64(2), parsed["filtered_count"])

	tools := parsed["tools"].([]interface{})
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
		result, err := provider.ExecuteTool(ctx, "filter_tools", map[string]interface{}{
			"pattern": "x_*",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)

		content := result.Content[0].(string)
		var parsed map[string]interface{}
		err = json.Unmarshal([]byte(content), &parsed)
		require.NoError(t, err)

		assert.Equal(t, float64(1), parsed["filtered_count"])
	})

	t.Run("filter by description", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "filter_tools", map[string]interface{}{
			"description_filter": "workflow",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)

		content := result.Content[0].(string)
		var parsed map[string]interface{}
		err = json.Unmarshal([]byte(content), &parsed)
		require.NoError(t, err)

		assert.Equal(t, float64(1), parsed["filtered_count"])
	})

	t.Run("error for invalid pattern", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "filter_tools", map[string]interface{}{
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
		result, err := provider.ExecuteTool(ctx, "call_tool", map[string]interface{}{
			"name":      "some_tool",
			"arguments": map[string]interface{}{"arg1": "value1"},
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)

		// The result should be JSON preserving CallToolResult structure
		content := result.Content[0].(string)
		var parsed struct {
			IsError bool          `json:"isError"`
			Content []interface{} `json:"content"`
		}
		err = json.Unmarshal([]byte(content), &parsed)
		require.NoError(t, err)
		assert.False(t, parsed.IsError)
	})

	t.Run("error for missing name", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "call_tool", nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "name argument is required")
	})

	t.Run("error for invalid arguments type", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "call_tool", map[string]interface{}{
			"name":      "some_tool",
			"arguments": "not-an-object",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(string), "arguments must be a JSON object")
	})
}

func TestProvider_HandleListResources(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		resources: []mcp.Resource{
			{URI: "file://test.txt", Name: "test.txt", Description: "Test file"},
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
}

func TestProvider_HandleDescribeResource(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		resources: []mcp.Resource{
			{URI: "file://test.txt", Name: "test.txt", Description: "Test file"},
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	t.Run("describes existing resource", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "describe_resource", map[string]interface{}{
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

		result, err := provider.ExecuteTool(ctx, "get_resource", map[string]interface{}{
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

		result, err := provider.ExecuteTool(ctx, "get_resource", map[string]interface{}{
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
		prompts: []mcp.Prompt{
			{Name: "prompt1", Description: "First prompt"},
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
}

func TestProvider_HandleDescribePrompt(t *testing.T) {
	provider := NewProvider()
	ctx := context.Background()

	mock := &mockMetaToolsHandler{
		prompts: []mcp.Prompt{
			{Name: "test_prompt", Description: "Test prompt"},
		},
	}
	cleanup := registerMockHandler(mock)
	defer cleanup()

	t.Run("describes existing prompt", func(t *testing.T) {
		result, err := provider.ExecuteTool(ctx, "describe_prompt", map[string]interface{}{
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
		result, err := provider.ExecuteTool(ctx, "get_prompt", map[string]interface{}{
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
