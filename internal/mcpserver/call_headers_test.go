package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// headerRecorder records the header on the requests that call a tool.
type headerRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (h *headerRecorder) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			h.mu.Lock()
			h.seen = append(h.seen, r.Header.Get("X-Muster-Caller-Kind"))
			h.mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

func (h *headerRecorder) last() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seen[len(h.seen)-1]
}

func echoServer() *mcpsrv.MCPServer {
	s := mcpsrv.NewMCPServer("echo", "1.0")
	s.AddTool(mcp.Tool{Name: "echo"}, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	return s
}

// Per-call headers reach the server on every HTTP-based connection type,
// and only on the call whose context carries them (M1-MU-S-05).
func TestCallHeadersReachTheServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	withHeader := WithCallHeaders(ctx, map[string]string{"X-Muster-Caller-Kind": "agent"})

	t.Run("streamable HTTP", func(t *testing.T) {
		rec := &headerRecorder{}
		srv := httptest.NewServer(rec.wrap(mcpsrv.NewStreamableHTTPServer(echoServer())))
		t.Cleanup(srv.Close)
		c := NewStreamableHTTPClientWithHeaders(srv.URL, nil)
		t.Cleanup(func() { _ = c.Close() })
		require.NoError(t, c.Initialize(ctx))
		_, err := c.CallTool(withHeader, "echo", nil)
		require.NoError(t, err)
		assert.Equal(t, "agent", rec.last())
		_, err = c.CallTool(ctx, "echo", nil)
		require.NoError(t, err)
		assert.Empty(t, rec.last())
	})

	t.Run("SSE", func(t *testing.T) {
		rec := &headerRecorder{}
		sse := mcpsrv.NewSSEServer(echoServer())
		srv := httptest.NewServer(rec.wrap(sse))
		t.Cleanup(srv.Close)
		c := NewSSEClientWithHeaders(srv.URL+"/sse", nil)
		t.Cleanup(func() { _ = c.Close() })
		require.NoError(t, c.Initialize(ctx))
		_, err := c.CallTool(withHeader, "echo", nil)
		require.NoError(t, err)
		assert.Equal(t, "agent", rec.last())
	})
}
