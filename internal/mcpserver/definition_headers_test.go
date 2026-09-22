package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefinitionHeaders(t *testing.T) {
	t.Run("keeps every header but Authorization, canonicalised", func(t *testing.T) {
		got := DefinitionHeaders(map[string]string{
			"x-mcp-toolsets": "default,git",
			"authorization":  "Bearer shared-token",
			"Authorization":  "Bearer other",
			"X-Trace":        "1",
		})
		assert.Equal(t, map[string]string{"X-Mcp-Toolsets": "default,git", "X-Trace": "1"}, got)
	})
	t.Run("nothing left reads as nil", func(t *testing.T) {
		assert.Nil(t, DefinitionHeaders(nil))
		assert.Nil(t, DefinitionHeaders(map[string]string{}))
		assert.Nil(t, DefinitionHeaders(map[string]string{"Authorization": "Bearer x"}))
	})
	t.Run("returns a copy", func(t *testing.T) {
		in := map[string]string{"X-A": "1"}
		out := DefinitionHeaders(in)
		out["X-B"] = "2"
		assert.Equal(t, map[string]string{"X-A": "1"}, in)
	})
}

// headerRecordingServer is a streamable-http MCP server that keeps the headers
// of every POST it answers, so a client's handshake can be inspected.
type headerRecordingServer struct {
	*httptest.Server
	mu    sync.Mutex
	posts []http.Header
}

func newHeaderRecordingServer(t *testing.T) *headerRecordingServer {
	t.Helper()
	mcpServer := server.NewMCPServer("recorder", "0.0.1")
	mcpServer.AddTool(mcp.NewTool("noop"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	streamable := server.NewStreamableHTTPServer(mcpServer, server.WithStateful(true))
	rec := &headerRecordingServer{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			rec.mu.Lock()
			rec.posts = append(rec.posts, r.Header.Clone())
			rec.mu.Unlock()
		}
		streamable.ServeHTTP(w, r)
	}))
	t.Cleanup(rec.Close)
	return rec
}

// values of one header across the recorded POSTs, in order.
func (s *headerRecordingServer) values(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.posts))
	for _, h := range s.posts {
		out = append(out, h.Get(name))
	}
	return out
}

// The session-scoped clients the aggregator builds send the definition's
// headers on the handshake and on every call: the hosted GitHub MCP server
// decides its toolsets from X-MCP-Toolsets at the initialize (#1304).
func TestSessionClientsSendDefinitionHeaders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	headers := DefinitionHeaders(map[string]string{"X-MCP-Toolsets": "default,git", "Authorization": "Bearer never"})

	t.Run("dynamic auth client", func(t *testing.T) {
		srv := newHeaderRecordingServer(t)
		client := NewDynamicAuthClient(srv.URL+"/mcp", nil, "", "", "").WithHeaders(headers)
		require.NoError(t, client.Initialize(ctx))
		t.Cleanup(func() { _ = client.Close() })
		_, err := client.CallTool(ctx, "noop", nil)
		require.NoError(t, err)

		values := srv.values("X-MCP-Toolsets")
		require.GreaterOrEqual(t, len(values), 2, "the initialize and the call")
		for i, v := range values {
			assert.Equal(t, "default,git", v, "POST %d carries the header", i)
		}
		for _, v := range srv.values("Authorization") {
			assert.NotEqual(t, "Bearer never", v, "the definition's Authorization entry is not sent")
		}
	})

	t.Run("header-func client keeps the credential from the func", func(t *testing.T) {
		srv := newHeaderRecordingServer(t)
		client := NewStreamableHTTPClientWithHeaderFunc(srv.URL+"/mcp", func(context.Context) map[string]string {
			return map[string]string{"Authorization": "Bearer from-the-session"}
		}).WithHeaders(map[string]string{"X-MCP-Toolsets": "default,git", "Authorization": "Bearer static"})
		require.NoError(t, client.Initialize(ctx))
		t.Cleanup(func() { _ = client.Close() })
		_, err := client.CallTool(ctx, "noop", nil)
		require.NoError(t, err)

		for i, v := range srv.values("X-MCP-Toolsets") {
			assert.Equal(t, "default,git", v, "POST %d carries the static header", i)
		}
		for i, v := range srv.values("Authorization") {
			assert.Equal(t, "Bearer from-the-session", v, "POST %d: the header func wins over a static entry", i)
		}
	})
}
