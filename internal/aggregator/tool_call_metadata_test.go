package aggregator

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/internal/workflow"
)

const (
	testSessionKey = "com.example/session"
	testOtherKey   = "com.example/other"
)

// metaBackend serves a tool "probe" over streamable HTTP on the given
// revisions, declaring extensions, and records the `_meta` entries of each
// call, protocol keys left out.
type metaBackend struct {
	url      string
	revision string
	meta     atomic.Pointer[map[string]any]
}

func newMetaBackend(t *testing.T, extensions map[string]any, versions ...string) *metaBackend {
	t.Helper()
	b := &metaBackend{revision: mcp.ProtocolVersion20260728}
	if len(versions) > 0 {
		b.revision = versions[0]
	}
	opts := []mcpserver.ServerOption{mcpserver.WithToolCapabilities(true)}
	if extensions != nil {
		opts = append(opts, mcpserver.WithExtensions(extensions))
	}
	srv := mcpserver.NewMCPServer("meta-backend", "test", opts...)
	srv.AddTool(mcp.NewTool("probe"), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		meta := map[string]any{}
		if req.Params.Meta != nil {
			for key, value := range req.Params.Meta.AdditionalFields {
				if !strings.HasPrefix(key, "io.modelcontextprotocol/") {
					meta[key] = value
				}
			}
		}
		b.meta.Store(&meta)
		return mcp.NewToolResultText("ok"), nil
	})
	ts := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv, mcpserver.WithStreamableHTTPProtocolVersions(versions...)))
	t.Cleanup(ts.Close)
	b.url = ts.URL
	return b
}

func (b *metaBackend) received() map[string]any {
	if meta := b.meta.Load(); meta != nil {
		return *meta
	}
	return nil
}

func acceptingKeys(keys ...any) map[string]any {
	return map[string]any{api.ToolCallMetadataExtension: map[string]any{"keys": keys}}
}

// newMetaMuster registers each backend in the aggregator's registry and
// serves, over streamable HTTP with the aggregator's tool-call metadata hooks
// and relay, a tool "<key>/probe" per backend and a tool "workflow" whose
// steps call every backend's probe in key order.
func newMetaMuster(t *testing.T, backends map[string]*metaBackend, order []string) string {
	t.Helper()
	a, err := NewAggregatorServer(t.Context(), AggregatorConfig{Host: "localhost", Port: 0}, nil)
	require.NoError(t, err)
	hooks := &mcpserver.Hooks{}
	a.addToolCallMetadataHooks(hooks)
	srv := mcpserver.NewMCPServer("muster", "test",
		mcpserver.WithHooks(hooks),
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithToolHandlerMiddleware(ToolCallMetadataRelay()))
	steps := make([]api.WorkflowStep, 0, len(order))
	for _, key := range order {
		client := internalmcp.NewStreamableHTTPClientWithHeaders(backends[key].url, nil)
		t.Cleanup(func() { _ = client.Close() })
		require.NoError(t, a.registry.Register(t.Context(), ServerRegistration{Name: key}, client))
		require.Equal(t, backends[key].revision, client.NegotiatedProtocolVersion())
		exposed := a.registry.ExposedToolName(key, "probe")
		srv.AddTool(mcp.NewTool(key+"/probe"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return a.CallToolInternal(ctx, exposed, req.GetArguments())
		})
		steps = append(steps, api.WorkflowStep{ID: key, Tool: exposed})
	}
	executor := workflow.NewWorkflowExecutor(a, nil)
	srv.AddTool(mcp.NewTool("workflow"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return executor.ExecuteWorkflow(ctx, &api.Workflow{Name: "probe-all", Steps: steps}, nil)
	})
	ts := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv))
	t.Cleanup(ts.Close)
	return ts.URL
}

func callWithMeta(t *testing.T, c *mcpclient.Client, tool string, meta map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: tool, Meta: &mcp.Meta{AdditionalFields: meta}}})
	require.NoError(t, err)
	require.False(t, result.IsError, textOf(t, result))
}

func callerToolCallMeta() map[string]any {
	return map[string]any{
		api.ToolCallMetadataExtension: map[string]any{
			testSessionKey:        "a1",
			testOtherKey:          "x",
			"com.example/unknown": "y",
		},
		testSessionKey: "a top-level entry is not passed on",
		"tenant":       "plain",
	}
}

// TestToolCallMetadata_ThroughMuster runs a client on each revision against a
// muster whose backends speak 2026-07-28 ("modern"), 2025-11-25 ("legacy"),
// or 2026-07-28 and do not declare the extension ("silent").
func TestToolCallMetadata_ThroughMuster(t *testing.T) {
	modern := newMetaBackend(t, acceptingKeys(testSessionKey))
	legacy := newMetaBackend(t, acceptingKeys(testSessionKey, testOtherKey), mcp.LATEST_LEGACY_PROTOCOL_VERSION)
	silent := newMetaBackend(t, nil)
	backends := map[string]*metaBackend{"modern": modern, "legacy": legacy, "silent": silent}
	url := newMetaMuster(t, backends, []string{"modern", "legacy", "silent"})

	wantModern := map[string]any{api.ToolCallMetadataExtension: map[string]any{testSessionKey: "a1"}}
	wantLegacy := map[string]any{api.ToolCallMetadataExtension: map[string]any{testSessionKey: "a1", testOtherKey: "x"}}

	for _, version := range []string{mcp.ProtocolVersion20260728, mcp.LATEST_LEGACY_PROTOCOL_VERSION} {
		t.Run(version+" client", func(t *testing.T) {
			c := connectToMuster(t, url, version, mcp.ClientCapabilities{}, nil)

			t.Run("muster declares the union of its backends' keys", func(t *testing.T) {
				require.Equal(t, map[string]any{
					api.ToolCallMetadataExtension: map[string]any{"keys": []any{testOtherKey, testSessionKey}},
				}, c.GetServerCapabilities().Extensions)
			})

			t.Run("each backend gets exactly the keys it accepts", func(t *testing.T) {
				for _, b := range backends {
					b.meta.Store(nil)
				}
				callWithMeta(t, c, "modern/probe", callerToolCallMeta())
				callWithMeta(t, c, "legacy/probe", callerToolCallMeta())
				callWithMeta(t, c, "silent/probe", callerToolCallMeta())

				require.Equal(t, wantModern, modern.received())
				require.Equal(t, wantLegacy, legacy.received())
				require.Empty(t, silent.received())
			})

			t.Run("a workflow's steps forward the same way", func(t *testing.T) {
				for _, b := range backends {
					b.meta.Store(nil)
				}
				callWithMeta(t, c, "workflow", callerToolCallMeta())

				require.Equal(t, wantModern, modern.received())
				require.Equal(t, wantLegacy, legacy.received())
				require.NotNil(t, silent.meta.Load(), "the silent backend was called")
				require.Empty(t, silent.received())
			})

			t.Run("a call without values forwards nothing", func(t *testing.T) {
				callWithMeta(t, c, "modern/probe", nil)

				require.Empty(t, modern.received())
			})
		})
	}
}

func TestToolCallMetadata_NotDeclaredWithoutAnAcceptingBackend(t *testing.T) {
	silent := newMetaBackend(t, nil)
	url := newMetaMuster(t, map[string]*metaBackend{"silent": silent}, []string{"silent"})
	c := connectToMuster(t, url, mcp.ProtocolVersion20260728, mcp.ClientCapabilities{}, nil)
	require.NotContains(t, c.GetServerCapabilities().Extensions, api.ToolCallMetadataExtension)
}
