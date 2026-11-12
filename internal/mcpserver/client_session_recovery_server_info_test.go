package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamableHTTPClient_RecoveredSessionExposesTheNewServerInfo: the
// OnSessionRecovered handler reads the server info of the new process's
// handshake, so a holder of the client can record a changed icon.
func TestStreamableHTTPClient_RecoveredSessionExposesTheNewServerInfo(t *testing.T) {
	newBackend := func(version string, opts ...server.ServerOption) *server.StreamableHTTPServer {
		s := server.NewMCPServer("icon-test", version, opts...)
		s.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("alive"), nil
		})
		return server.NewStreamableHTTPServer(s, server.WithStateful(true), server.WithStreamableHTTPProtocolVersions(mcp.LATEST_LEGACY_PROTOCOL_VERSION))
	}
	icon := mcp.Icon{Src: "https://example.com/icon.svg"}

	var backend atomic.Pointer[server.StreamableHTTPServer]
	backend.Store(newBackend("0.9.0"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend.Load().ServeHTTP(w, r)
	}))
	defer ts.Close()

	c := NewStreamableHTTPClientWithHeaders(ts.URL, nil)
	require.NoError(t, c.Initialize(t.Context()))
	defer func() { _ = c.Close() }()
	require.Empty(t, c.ServerImplementation().Icons)

	seen := make(chan mcp.Implementation, 1)
	c.OnSessionRecovered(func() { seen <- c.ServerImplementation() })
	backend.Store(newBackend("1.0.0", server.WithIcons(icon)))

	_, err := c.CallTool(t.Context(), "echo", nil)
	require.NoError(t, err)

	select {
	case info := <-seen:
		assert.Equal(t, "1.0.0", info.Version)
		assert.Equal(t, []mcp.Icon{icon}, info.Icons)
	case <-t.Context().Done():
		t.Fatal("the observer was not told of the recovered session")
	}
}
