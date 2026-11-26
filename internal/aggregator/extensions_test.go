package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
	testExtensionID      = "com.example/context"
	testOtherExtensionID = "com.example/export"
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

func extensionDeclarations(settings any) map[string]any {
	return map[string]any{testExtensionID: settings}
}

// newMetaMuster registers each backend in the aggregator's registry and
// serves, over streamable HTTP with the aggregator's tool-call metadata hooks
// and relay, a tool "<key>/probe" per backend and a tool "workflow" whose
// steps call every backend's probe in key order.
func newMetaMuster(t *testing.T, backends map[string]*metaBackend, order []string) string {
	t.Helper()
	a, err := NewAggregatorServer(t.Context(), AggregatorConfig{Host: "localhost", Port: 0}, nil)
	require.NoError(t, err)
	t.Cleanup(a.connPool.Stop)
	hooks := &mcpserver.Hooks{}
	a.addExtensionHooks(hooks)
	srv := mcpserver.NewMCPServer("muster", "test",
		mcpserver.WithHooks(hooks),
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithToolHandlerMiddleware(ExtensionMetaRelay()))
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
		testExtensionID:      map[string]any{"arbitrary": []any{"a1", float64(7)}},
		testOtherExtensionID: "not declared",
		"tenant":             "plain",
	}
}

// TestExtensionMeta_ThroughMuster runs a client on each revision against a
// muster whose backends speak 2026-07-28 ("modern"), 2025-11-25 ("legacy"),
// or 2026-07-28 and do not declare the extension ("silent").
func TestExtensionMeta_ThroughMuster(t *testing.T) {
	settings := map[string]any{"format": "opaque"}
	modern := newMetaBackend(t, extensionDeclarations(settings))
	legacy := newMetaBackend(t, extensionDeclarations(settings), mcp.LATEST_LEGACY_PROTOCOL_VERSION)
	silent := newMetaBackend(t, nil)
	backends := map[string]*metaBackend{"modern": modern, "legacy": legacy, "silent": silent}
	url := newMetaMuster(t, backends, []string{"modern", "legacy", "silent"})

	want := map[string]any{testExtensionID: callerToolCallMeta()[testExtensionID]}

	for _, version := range []string{mcp.ProtocolVersion20260728, mcp.LATEST_LEGACY_PROTOCOL_VERSION} {
		t.Run(version+" client", func(t *testing.T) {
			c := connectToMuster(t, url, version, mcp.ClientCapabilities{Extensions: extensionDeclarations(map[string]any{})}, nil)

			t.Run("muster preserves its backends' agreed settings", func(t *testing.T) {
				require.Equal(t, extensionDeclarations(settings), c.GetServerCapabilities().Extensions)
			})

			t.Run("each declaring backend receives the opaque payload", func(t *testing.T) {
				for _, b := range backends {
					b.meta.Store(nil)
				}
				callWithMeta(t, c, "modern/probe", callerToolCallMeta())
				callWithMeta(t, c, "legacy/probe", callerToolCallMeta())
				callWithMeta(t, c, "silent/probe", callerToolCallMeta())

				require.Equal(t, want, modern.received())
				require.Equal(t, want, legacy.received())
				require.Empty(t, silent.received())
			})

			t.Run("a workflow's steps forward the same way", func(t *testing.T) {
				for _, b := range backends {
					b.meta.Store(nil)
				}
				callWithMeta(t, c, "workflow", callerToolCallMeta())

				require.Equal(t, want, modern.received())
				require.Equal(t, want, legacy.received())
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

func TestExtensionMeta_NotDeclaredWithoutAnAcceptingBackend(t *testing.T) {
	silent := newMetaBackend(t, nil)
	url := newMetaMuster(t, map[string]*metaBackend{"silent": silent}, []string{"silent"})
	c := connectToMuster(t, url, mcp.ProtocolVersion20260728, mcp.ClientCapabilities{}, nil)
	require.NotContains(t, c.GetServerCapabilities().Extensions, testExtensionID)
}

func TestExtensionMeta_RequiresClientDeclaration(t *testing.T) {
	for _, revision := range []string{mcp.ProtocolVersion20260728, mcp.LATEST_LEGACY_PROTOCOL_VERSION} {
		for name, settings := range map[string]any{"missing": nil, "malformed": "not an object"} {
			t.Run(revision+"/"+name, func(t *testing.T) {
				backend := newMetaBackend(t, extensionDeclarations(map[string]any{}))
				url := newMetaMuster(t, map[string]*metaBackend{"backend": backend}, []string{"backend"})
				var extensions map[string]any
				if settings != nil {
					extensions = extensionDeclarations(settings)
				}
				caller := connectToMuster(t, url, revision, mcp.ClientCapabilities{Extensions: extensions}, nil)
				callWithMeta(t, caller, "backend/probe", callerToolCallMeta())
				require.Empty(t, backend.received())
			})
		}
	}
}

func TestExtensionSettingsRemainOpaque(t *testing.T) {
	for name, settings := range map[string][2]any{
		"identical": {map[string]any{"arbitrary": "a"}, map[string]any{"arbitrary": "a"}},
		"different": {map[string]any{"arbitrary": "a"}, map[string]any{"arbitrary": "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			first := newMetaBackend(t, extensionDeclarations(settings[0]))
			second := newMetaBackend(t, extensionDeclarations(settings[1]))
			url := newMetaMuster(t, map[string]*metaBackend{"first": first, "second": second}, []string{"first", "second"})
			caller := connectToMuster(t, url, mcp.ProtocolVersion20260728, mcp.ClientCapabilities{}, nil)
			want := settings[0]
			if name == "different" {
				want = map[string]any{}
			}
			require.Equal(t, extensionDeclarations(want), caller.GetServerCapabilities().Extensions)
		})
	}
}

func TestBackendExtensionsAreScopedToCaller(t *testing.T) {
	aggregator, err := NewAggregatorServer(t.Context(), AggregatorConfig{Host: "localhost", Port: 0}, nil)
	require.NoError(t, err)
	t.Cleanup(aggregator.connPool.Stop)
	client := func(identifier string) MCPClient {
		return &notifMockClient{capabilities: mcp.ServerCapabilities{Extensions: map[string]any{identifier: map[string]any{}}}}
	}
	require.NoError(t, aggregator.registry.Register(t.Context(), ServerRegistration{Name: "org-a/visible"}, client(testExtensionID)))
	require.NoError(t, aggregator.registry.Register(t.Context(), ServerRegistration{Name: "org-b/hidden"}, client("com.example/hidden")))
	aggregator.connPool.Put("alice", "org-a/session", client(testOtherExtensionID))
	aggregator.connPool.Put("bob", "org-a/private", client("com.example/private"))
	useVisibility(t, api.ServersOfNamespacesVisible("muster", []string{"org-a"}))
	require.Equal(t, map[string]any{
		testExtensionID:      map[string]any{},
		testOtherExtensionID: map[string]any{},
	}, aggregator.backendExtensions(api.WithSessionID(t.Context(), "alice")))
}

func TestExtensionDiscoveryWaitsForBootstrap(t *testing.T) {
	for _, revision := range []string{mcp.ProtocolVersion20260728, mcp.LATEST_LEGACY_PROTOCOL_VERSION} {
		t.Run(revision, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const sessionID = "bootstrap-session"
				aggregator := newBootstrapTestAggregator(t, newConnectGates(), "backend")
				release := make(chan struct{})
				aggregator.ssoConnect = func(ctx context.Context, _ *ServerInfo, _ string) ssoConnectOutcome {
					select {
					case <-release:
						aggregator.connPool.Put(sessionID, "backend", &notifMockClient{capabilities: mcp.ServerCapabilities{Extensions: extensionDeclarations(map[string]any{})}})
						return ssoConnected
					case <-ctx.Done():
						return ssoConnectFailed
					}
				}
				require.NotNil(t, aggregator.beginSessionBootstrap(ssoSession{userID: "alice", sessionID: sessionID}))
				hooks := &mcpserver.Hooks{}
				aggregator.addExtensionHooks(hooks)
				frontend := mcpserver.NewMCPServer("muster", "test", mcpserver.WithHooks(hooks))
				message := fmt.Appendf(nil, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"clientInfo":{"name":"test","version":"1"},"capabilities":{}}}`, revision)
				if mcp.IsModernProtocol(revision) {
					message = fmt.Appendf(nil, `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{%q:%q,%q:{},%q:{"name":"test","version":"1"}}}}`, mcp.MetaKeyProtocolVersion, revision, mcp.MetaKeyClientCapabilities, mcp.MetaKeyClientInfo)
				}
				result := make(chan mcp.JSONRPCMessage, 1)
				go func() {
					result <- frontend.HandleMessage(api.WithSessionID(t.Context(), sessionID), message)
				}()
				synctest.Wait()
				select {
				case <-result:
					t.Fatal("discovery returned before the backend connected")
				default:
				}
				close(release)
				encoded, err := json.Marshal(<-result)
				require.NoError(t, err)
				var response struct {
					Result struct {
						Capabilities mcp.ServerCapabilities `json:"capabilities"`
					} `json:"result"`
				}
				require.NoError(t, json.Unmarshal(encoded, &response))
				require.Equal(t, extensionDeclarations(map[string]any{}), response.Result.Capabilities.Extensions)
			})
		})
	}
}
