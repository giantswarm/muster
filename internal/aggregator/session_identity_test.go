package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/internal/api"
)

// fakeClientSession is a minimal mcp-go ClientSession used to put a transport
// session ID into a context.
type fakeClientSession struct {
	id string
}

func (s *fakeClientSession) Initialize()       {}
func (s *fakeClientSession) Initialized() bool { return true }
func (s *fakeClientSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return make(chan mcp.JSONRPCNotification, 1)
}
func (s *fakeClientSession) SessionID() string { return s.id }

// withTransportSession puts a transport session into ctx the same way the
// mcp-go server does before invoking a tool handler.
func withTransportSession(ctx context.Context, sessionID string) context.Context {
	srv := mcpserver.NewMCPServer("test", "1.0.0")
	return srv.WithContext(ctx, &fakeClientSession{id: sessionID})
}

func TestGetSessionIDFromContext(t *testing.T) {
	t.Run("authenticated session wins over transport session", func(t *testing.T) {
		ctx := api.WithSessionID(context.Background(), "token-family-123")
		ctx = withTransportSession(ctx, "mcp-session-abc")

		assert.Equal(t, "token-family-123", getSessionIDFromContext(ctx))
	})

	t.Run("unauthenticated request is keyed by its transport session", func(t *testing.T) {
		ctx := api.WithSessionID(context.Background(), stdioDefaultUser)
		ctx = withTransportSession(ctx, "mcp-session-abc")

		assert.Equal(t, "mcp-session-abc", getSessionIDFromContext(ctx))
	})

	t.Run("two unauthenticated connections get different session keys", func(t *testing.T) {
		// SECURITY: this is what keeps a backend one client authenticated to
		// via core_auth_login out of every other client's session-scoped
		// capability store, connection pool, and token store.
		base := api.WithSessionID(context.Background(), stdioDefaultUser)

		first := getSessionIDFromContext(withTransportSession(base, "mcp-session-a"))
		second := getSessionIDFromContext(withTransportSession(base, "mcp-session-b"))

		assert.NotEqual(t, first, second)
	})

	t.Run("placeholder is kept when no transport session is available", func(t *testing.T) {
		// Background work derived from a request carries the injected
		// placeholder but no MCP session; it must still resolve to a key.
		ctx := api.WithSessionID(context.Background(), stdioDefaultUser)

		assert.Equal(t, stdioDefaultUser, getSessionIDFromContext(ctx))
	})

	t.Run("empty when nothing was injected", func(t *testing.T) {
		assert.Equal(t, "", getSessionIDFromContext(context.Background()))
	})
}
