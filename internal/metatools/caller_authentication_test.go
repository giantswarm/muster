package metatools

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

func TestCallToolClassifiesCallerAuthenticationWithoutParsingText(t *testing.T) {
	for name, test := range map[string]struct {
		err    error
		status any
	}{
		"caller token refused": {
			err:    fmt.Errorf("tool not found: %w", &api.CallerAuthenticationError{Message: "the caller needs fresh credentials"}),
			status: map[string]any{"status": "unauthenticated"},
		},
		"ordinary error mentions re-login": {err: errors.New("A re-login is required")},
		"infrastructure failure":           {err: errors.New("Kubernetes API is unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			cleanup := registerMockHandler(&mockMetaToolsHandler{callToolError: test.err})
			t.Cleanup(cleanup)
			result, err := NewProvider().ExecuteTool(t.Context(), "call_tool", map[string]any{"name": "x_kubernetes_list_pods"})
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.Equal(t, []any{"Tool execution failed: " + test.err.Error()}, result.Content)
			require.Equal(t, test.status, result.StructuredContent)
			require.Empty(t, result.Meta)
		})
	}
}

func TestCallToolKeepsBackendAuthenticationStatusDispatched(t *testing.T) {
	cleanup := registerMockHandler(&mockMetaToolsHandler{callToolResult: &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent("backend credentials expired")},
		StructuredContent: map[string]any{"status": "unauthenticated"},
		IsError:           true,
	}})
	t.Cleanup(cleanup)
	result, err := NewProvider().ExecuteTool(t.Context(), "call_tool", map[string]any{"name": "x_kubernetes_list_pods"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, map[string]any{"status": "unauthenticated"}, result.StructuredContent)
	require.Equal(t, DispatchedTool{Name: "x_kubernetes_list_pods"}, result.Meta[MetaKeyDispatchedTool])
}
