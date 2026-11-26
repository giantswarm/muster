package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const extensionStdioServerEnv = "MUSTER_TEST_EXTENSION_STDIO_SERVER"

func TestCallerMeta_LegacyTransports(t *testing.T) {
	for _, transport := range []string{"sse", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			var client MCPClient
			if transport == "stdio" {
				stdioClient := NewStdioClientWithEnv(os.Args[0], []string{"-test.run=^TestExtensionStdioServer$"}, map[string]string{extensionStdioServerEnv: "1"})
				stdioClient.legacyBackend = true
				client = stdioClient
			} else {
				backend := server.NewMCPServer("backend", "test", server.WithToolCapabilities(true), server.WithExtensions(map[string]any{testExtensionID: map[string]any{}}))
				backend.AddTool(mcp.NewTool("probe"), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return mcp.NewToolResultStructuredOnly(map[string]any{"declared": declaredCapabilities(ctx).Extensions, "metadata": request.Params.Meta.AdditionalFields}), nil
				})
				httpServer := httptest.NewUnstartedServer(nil)
				httpServer.Config.Handler = server.NewSSEServer(backend,
					server.WithBaseURL("http://"+httpServer.Listener.Addr().String()),
					server.WithSSEContextFunc(func(ctx context.Context, _ *http.Request) context.Context {
						return server.WithSupportedProtocolVersions(ctx, []string{mcp.LATEST_LEGACY_PROTOCOL_VERSION})
					}))
				httpServer.Start()
				t.Cleanup(httpServer.Close)
				sseClient := NewSSEClientWithHeaders(httpServer.URL+"/sse", nil)
				sseClient.legacyBackend = true
				client = sseClient
			}
			t.Cleanup(func() { _ = client.Close() })
			require.NoError(t, client.Initialize(t.Context()))
			require.Equal(t, mcp.LATEST_LEGACY_PROTOCOL_VERSION, client.NegotiatedProtocolVersion())
			declarations := map[string]any{testExtensionID: map[string]any{}}
			ctx := api.WithCallerMeta(t.Context(), map[string]any{testExtensionID: "opaque"}, declarations)
			result, err := client.CallTool(ctx, "probe", nil)
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Equal(t, map[string]any{
				"declared": declarations,
				"metadata": map[string]any{testExtensionID: "opaque"},
			}, result.StructuredContent)
		})
	}
}

func TestExtensionStdioServer(t *testing.T) {
	if os.Getenv(extensionStdioServerEnv) == "" {
		t.Skip("stdio server subprocess")
	}
	backend := server.NewMCPServer("backend", "test", server.WithToolCapabilities(true), server.WithExtensions(map[string]any{testExtensionID: map[string]any{}}))
	backend.AddTool(mcp.NewTool("probe"), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultStructuredOnly(map[string]any{"declared": declaredCapabilities(ctx).Extensions, "metadata": request.Params.Meta.AdditionalFields}), nil
	})
	_ = server.ServeStdio(backend, server.WithStdioContextFunc(func(ctx context.Context) context.Context {
		return server.WithSupportedProtocolVersions(ctx, []string{mcp.LATEST_LEGACY_PROTOCOL_VERSION})
	}))
	os.Exit(0)
}
