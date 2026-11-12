package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamableHTTPClient_RecoveryReportsTheNewServerInfo: when another
// process takes over behind the address, the OnHandshake handler is told the
// serverInfo that process declares before the OnSessionRecovered handler
// runs: through the fresh handshake of a lost 2025-11-25 session, and through
// the server/discover that follows the reopened list-changes stream of a
// 2026-07-28 connection.
func TestStreamableHTTPClient_RecoveryReportsTheNewServerInfo(t *testing.T) {
	icon := mcp.Icon{Src: "https://example.com/icon.svg"}
	for name, tc := range map[string]struct {
		versions []string
		// meetNewProcess makes the client meet the process now serving ts.
		meetNewProcess func(t *testing.T, c *StreamableHTTPClient, ts *httptest.Server)
	}{
		"2025-11-25": {
			versions: []string{mcp.LATEST_LEGACY_PROTOCOL_VERSION},
			meetNewProcess: func(t *testing.T, c *StreamableHTTPClient, _ *httptest.Server) {
				_, err := c.CallTool(t.Context(), "echo", nil)
				require.NoError(t, err, "the call re-initializes the lost session")
			},
		},
		"2026-07-28": {
			meetNewProcess: func(_ *testing.T, _ *StreamableHTTPClient, ts *httptest.Server) {
				ts.CloseClientConnections()
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			newBackend := func(version string, opts ...server.ServerOption) *server.StreamableHTTPServer {
				s := server.NewMCPServer("icon-test", version, append(opts, server.WithToolCapabilities(true))...)
				s.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return mcp.NewToolResultText("alive"), nil
				})
				return server.NewStreamableHTTPServer(s, server.WithStateful(true), server.WithStreamableHTTPProtocolVersions(tc.versions...))
			}
			var backend atomic.Pointer[server.StreamableHTTPServer]
			backend.Store(newBackend("0.9.0"))
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				backend.Load().ServeHTTP(w, r)
			}))
			defer ts.Close()

			handshakes := make(chan mcp.Implementation, 4)
			recovered := make(chan struct{}, 4)
			listening := make(chan struct{}, 4)
			c := NewStreamableHTTPClientWithHeaders(ts.URL, nil)
			c.OnHandshake(func(info mcp.Implementation) { handshakes <- info })
			c.OnSessionRecovered(func() { recovered <- struct{}{} })
			c.OnNotification(func(n mcp.JSONRPCNotification) {
				if n.Method == mcp.MethodNotificationSubscriptionsAcknowledged {
					listening <- struct{}{}
				}
			})
			require.NoError(t, c.Initialize(t.Context()))
			defer func() { _ = c.Close() }()
			first := receive(t, handshakes, "the first handshake")
			assert.Equal(t, "0.9.0", first.Version)
			assert.Empty(t, first.Icons)
			if tc.versions == nil {
				receive(t, listening, "the list-changes stream")
			}

			backend.Store(newBackend("1.0.0", server.WithIcons(icon)))
			tc.meetNewProcess(t, c, ts)

			info := receive(t, handshakes, "the new process's serverInfo")
			assert.Equal(t, "1.0.0", info.Version)
			assert.Equal(t, []mcp.Icon{icon}, info.Icons)
			receive(t, recovered, "the recovery")
		})
	}
}

// receive returns the next value of ch, failing the test when none arrives
// within 10 seconds.
func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}
