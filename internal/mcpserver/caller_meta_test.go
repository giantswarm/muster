package mcpserver

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const testExtensionID = "com.example/context"

func TestCallerMeta_Protocol(t *testing.T) {
	for _, revision := range []string{mcp.ProtocolVersion20260728, mcp.LATEST_LEGACY_PROTOCOL_VERSION} {
		for name, extensions := range map[string]map[string]any{
			"declared":  {testExtensionID: map[string]any{"arbitrary": []any{"opaque"}}},
			"silent":    nil,
			"malformed": {testExtensionID: "not settings"},
		} {
			t.Run(revision+"/"+name, func(t *testing.T) {
				var received atomic.Pointer[mcp.Meta]
				var declared atomic.Pointer[mcp.ClientCapabilities]
				backend := server.NewMCPServer("backend", "test", server.WithExtensions(extensions), server.WithToolCapabilities(true))
				backend.AddTool(mcp.NewTool("probe"), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					received.Store(request.Params.Meta)
					capabilities := declaredCapabilities(ctx)
					declared.Store(&capabilities)
					return mcp.NewToolResultText("ok"), nil
				})
				transport := httptest.NewServer(server.NewStreamableHTTPServer(backend, server.WithStreamableHTTPProtocolVersions(revision)))
				t.Cleanup(transport.Close)
				client := connect(t, t.Context(), transport.URL)
				capabilities := mcp.ClientCapabilities{Extensions: map[string]any{testExtensionID: map[string]any{"format": "client-opaque"}}}
				payload := map[string]any{"any-shape": []any{"a1", float64(7)}}
				ctx := WithCallerCapabilities(t.Context(), capabilities)
				ctx, err := api.WithCallerMeta(ctx, map[string]any{testExtensionID: payload})
				require.NoError(t, err)
				_, err = client.CallTool(ctx, "probe", nil)
				require.NoError(t, err)
				require.NotNil(t, received.Load())
				require.Equal(t, payload, received.Load().AdditionalFields[testExtensionID])
				if name == "declared" {
					if mcp.IsModernProtocol(revision) {
						require.Equal(t, capabilities.Extensions, declared.Load().Extensions)
					} else {
						require.Equal(t, map[string]any{testExtensionID: map[string]any{}}, declared.Load().Extensions)
					}
				}
				_, err = client.CallTool(t.Context(), "probe", nil)
				require.NoError(t, err)
				if received.Load() != nil {
					require.NotContains(t, received.Load().AdditionalFields, testExtensionID)
				}
			})
		}
	}
}

func TestCallerMeta_PreservesExistingMetadata(t *testing.T) {
	ctx, err := api.WithCallerMeta(t.Context(), map[string]any{testExtensionID: "caller"})
	require.NoError(t, err)
	for name, meta := range map[string]*mcp.Meta{
		"empty":          nil,
		"progress":       {ProgressToken: "hop-owned"},
		"existing entry": {AdditionalFields: map[string]any{testExtensionID: "existing"}},
	} {
		t.Run(name, func(t *testing.T) {
			result := withCallerMeta(ctx, meta)
			require.NotNil(t, result)
			if name == "existing entry" {
				require.Equal(t, "existing", result.AdditionalFields[testExtensionID])
			} else {
				require.Equal(t, "caller", result.AdditionalFields[testExtensionID])
			}
			if name == "progress" {
				require.Equal(t, "hop-owned", result.ProgressToken)
			}
		})
	}
}

func TestCallerMeta_LegacyMetadataDoesNotDependOnDeclarations(t *testing.T) {
	const newExtensionID = "com.example/new-context"
	hooks := &server.Hooks{}
	var initialized atomic.Int32
	hooks.AddAfterInitialize(func(_ context.Context, _ any, _ *mcp.InitializeRequest, result *mcp.InitializeResult) {
		identifier := testExtensionID
		if initialized.Add(1) > 1 {
			identifier = newExtensionID
		}
		result.Capabilities.Extensions = map[string]any{identifier: map[string]any{}}
	})
	backend := server.NewMCPServer("backend", "test", server.WithHooks(hooks), server.WithToolCapabilities(true))
	var received atomic.Pointer[mcp.Meta]
	backend.AddTool(mcp.NewTool("probe"), func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		received.Store(request.Params.Meta)
		return mcp.NewToolResultText("ok"), nil
	})
	transport := httptest.NewServer(server.NewStreamableHTTPServer(backend, server.WithStreamableHTTPProtocolVersions(mcp.LATEST_LEGACY_PROTOCOL_VERSION)))
	t.Cleanup(transport.Close)
	client := connect(t, t.Context(), transport.URL)
	require.Equal(t, int32(2), initialized.Load())
	require.Empty(t, client.ServerCapabilities().Extensions)
	ctx, err := api.WithCallerMeta(t.Context(), map[string]any{newExtensionID: "opaque"})
	require.NoError(t, err)
	_, err = client.CallTool(ctx, "probe", nil)
	require.NoError(t, err)
	require.NotNil(t, received.Load())
	require.Equal(t, "opaque", received.Load().AdditionalFields[newExtensionID])
}

func TestCallerMetaDoesNotMutateSharedOutboundMetadata(t *testing.T) {
	metadata := &mcp.Meta{AdditionalFields: map[string]any{"server/setting": true}}
	firstContext, err := api.WithCallerMeta(t.Context(), map[string]any{testExtensionID: "first"})
	require.NoError(t, err)
	secondContext, err := api.WithCallerMeta(t.Context(), map[string]any{testExtensionID: "second"})
	require.NoError(t, err)
	first := withCallerMeta(firstContext, metadata)
	second := withCallerMeta(secondContext, metadata)
	require.Equal(t, "first", first.AdditionalFields[testExtensionID])
	require.Equal(t, "second", second.AdditionalFields[testExtensionID])
	require.Equal(t, map[string]any{"server/setting": true}, metadata.AdditionalFields)
}
