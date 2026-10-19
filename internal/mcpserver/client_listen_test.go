package mcpserver

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newListenTestServer(t *testing.T, opts ...mcpserver.StreamableHTTPOption) string {
	t.Helper()
	srv := mcpserver.NewMCPServer("listen-test", "1", mcpserver.WithResourceCapabilities(true, false))
	ts := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv, opts...))
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp"
}

// TestListenMode_NegotiatesTheModernRevision pins that a client in listen mode
// speaks 2026-07-28 and holds a subscriptions/listen stream whose
// acknowledgement reaches its notification handler, while a legacy client
// stays on the initialize handshake.
func TestListenMode_NegotiatesTheModernRevision(t *testing.T) {
	url := newListenTestServer(t)

	legacy := NewStreamableHTTPClientWithHeaders(url, nil)
	require.NoError(t, legacy.Initialize(context.Background()))
	t.Cleanup(func() { _ = legacy.Close() })
	assert.False(t, mcp.IsModernProtocol(legacy.NegotiatedProtocolVersion()))
	assert.Error(t, legacy.ListenResources(context.Background(), []string{"test://a"}), "a legacy client cannot listen")

	c := NewStreamableHTTPClientWithHeaders(url, nil)
	c.UseSubscriptionsListen()
	acked := make(chan mcp.JSONRPCNotification, 1)
	c.OnNotification(func(n mcp.JSONRPCNotification) {
		if n.Method == mcp.MethodNotificationSubscriptionsAcknowledged {
			acked <- n
		}
	})
	require.NoError(t, c.Initialize(context.Background()))
	t.Cleanup(func() { _ = c.Close() })
	assert.Equal(t, mcp.ProtocolVersion20260728, c.NegotiatedProtocolVersion())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.ListenResources(ctx, []string{"test://a"}) }()
	<-acked
	cancel()
	assert.ErrorContains(t, <-done, "context canceled", "the stream ends with its context")
}

// TestListenMode_RefusesALegacyServer pins that a server without 2026-07-28
// is refused at Initialize rather than holding a stream that never delivers.
func TestListenMode_RefusesALegacyServer(t *testing.T) {
	url := newListenTestServer(t, mcpserver.WithStreamableHTTPProtocolVersions(mcp.LATEST_LEGACY_PROTOCOL_VERSION))

	c := NewStreamableHTTPClientWithHeaders(url, nil)
	c.UseSubscriptionsListen()
	err := c.Initialize(context.Background())
	require.ErrorIs(t, err, ErrSubscriptionsListenUnsupported)
}
