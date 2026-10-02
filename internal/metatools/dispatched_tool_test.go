package metatools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/observability"
)

// dispatchingHandler resolves every call the way the aggregator does: it
// annotates the dispatch to server under the backend-native name.
type dispatchingHandler struct {
	*mockMetaToolsHandler
	server, serverTool string
}

func (h *dispatchingHandler) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	observability.AnnotateDownstreamCall(ctx, h.server, name, h.serverTool)
	return h.mockMetaToolsHandler.CallTool(ctx, name, args)
}

func TestProvider_HandleCallTool_DispatchedTool(t *testing.T) {
	want := DispatchedTool{Name: "x_kubernetes_list_pods", Server: "kubernetes", ServerTool: "list_pods"}

	tests := []struct {
		name    string
		handler *dispatchingHandler
		call    string
		want    DispatchedTool
	}{
		{
			name: "a backend tool names its server and backend-native name",
			handler: &dispatchingHandler{
				mockMetaToolsHandler: &mockMetaToolsHandler{callToolResult: mcp.NewToolResultText("pods")},
				server:               "kubernetes", serverTool: "list_pods",
			},
			call: "x_kubernetes_list_pods",
			want: want,
		},
		{
			name: "a failing backend tool still names what ran",
			handler: &dispatchingHandler{
				mockMetaToolsHandler: &mockMetaToolsHandler{callToolResult: mcp.NewToolResultError("forbidden")},
				server:               "kubernetes", serverTool: "list_pods",
			},
			call: "x_kubernetes_list_pods",
			want: want,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api.RegisterMetaTools(tt.handler)
			defer api.RegisterMetaTools(nil)

			result, err := NewProvider().ExecuteTool(context.Background(), "call_tool", map[string]any{"name": tt.call})
			require.NoError(t, err)

			var envelope struct {
				Tool DispatchedTool `json:"tool"`
			}
			require.NoError(t, json.Unmarshal([]byte(result.Content[0].(string)), &envelope))
			assert.Equal(t, tt.want, envelope.Tool, "envelope")
			assert.Equal(t, tt.want, result.Meta[MetaKeyDispatchedTool], "call_tool result _meta")
		})
	}
}

func TestProvider_HandleCallTool_DispatchedToolCore(t *testing.T) {
	cleanup := registerMockHandler(&mockMetaToolsHandler{callToolResult: mcp.NewToolResultText("ok")})
	defer cleanup()

	result, err := NewProvider().ExecuteTool(context.Background(), "call_tool", map[string]any{"name": "core_service_list"})
	require.NoError(t, err)
	assert.Equal(t, DispatchedTool{Name: "core_service_list"}, result.Meta[MetaKeyDispatchedTool], "a tool muster serves itself names no server")
}

func TestDispatchedToolFromMeta(t *testing.T) {
	tool := DispatchedTool{Name: "x_kubernetes_list_pods", Server: "kubernetes", ServerTool: "list_pods"}

	got, ok := DispatchedToolFromMeta(&mcp.Meta{AdditionalFields: map[string]any{MetaKeyDispatchedTool: tool}})
	require.True(t, ok)
	assert.Equal(t, tool, got, "in process")

	wire := map[string]any{"name": "x_kubernetes_list_pods", "server": "kubernetes", "serverTool": "list_pods"}
	got, ok = DispatchedToolFromMeta(&mcp.Meta{AdditionalFields: map[string]any{MetaKeyDispatchedTool: wire}})
	require.True(t, ok)
	assert.Equal(t, tool, got, "decoded from the wire")

	for name, meta := range map[string]*mcp.Meta{
		"nil _meta":   nil,
		"absent key":  {AdditionalFields: map[string]any{"traceId": "abc"}},
		"no name":     {AdditionalFields: map[string]any{MetaKeyDispatchedTool: map[string]any{"server": "kubernetes"}}},
		"wrong shape": {AdditionalFields: map[string]any{MetaKeyDispatchedTool: "kubernetes"}},
	} {
		_, ok := DispatchedToolFromMeta(meta)
		assert.False(t, ok, name)
	}
}
