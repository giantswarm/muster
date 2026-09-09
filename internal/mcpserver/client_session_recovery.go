package mcpserver

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/giantswarm/muster/pkg/logging"
)

// Session recovery for stateful streamable-http backends.
//
// A stateful server hands out an Mcp-Session-Id at initialize and expects it
// on every later request. When the server is redeployed, the new process does
// not know the old id: it answers 404, mcp-go clears the id from the transport
// and returns transport.ErrSessionTerminated, and from then on every request
// goes out with no session id at all -- which a stateful server rejects as
// well (the Python SDK with 400 "Missing session ID"). The standalone GET
// listener usually meets the 404 first, so by the time a tool call fails the
// transport already has no session and the error is not the typed one. Both
// shapes are recognised here. Nothing in mcp-go re-runs initialize, so the
// client stayed dead until an operator restarted the service (issue #999).

// sessionIDOf returns the session id the transport currently holds, or ""
// when the client does not expose one.
func sessionIDOf(c client.MCPClient) string {
	if c == nil {
		return ""
	}
	if s, ok := c.(interface{ GetSessionId() string }); ok {
		return s.GetSessionId()
	}
	return ""
}

// sessionLostLocked reports whether err means the backend no longer knows
// this client's MCP session. Caller must hold b.mu.
func (b *baseMCPClient) sessionLostLocked(err error) bool {
	if errors.Is(err, transport.ErrSessionTerminated) {
		return true
	}
	return b.hadSession && sessionIDOf(b.client) == ""
}

// withSessionRecovery runs op and, when it failed because the MCP session is
// gone, performs one fresh handshake and runs op a second time. Any other
// failure is returned as is.
func withSessionRecovery[T any](b *baseMCPClient, ctx context.Context, op func() (T, error)) (T, error) {
	b.mu.RLock()
	generation := b.sessionGeneration
	b.mu.RUnlock()

	result, err := op()
	if err == nil || !b.recoverSession(ctx, generation, err) {
		return result, err
	}
	return op()
}

// recoverSession re-establishes the session after an operation started on
// the given generation failed with err. It reports whether the client is
// connected on a newer session and the operation should be retried.
//
// Concurrent callers that failed on the same dead session serialise on the
// write lock; the first performs the handshake, the others find the
// generation advanced and retry on its result. A handshake that fails leaves
// the client disconnected with reconnectPending set, so the next operation
// tries again instead of failing until the service is restarted.
func (b *baseMCPClient) recoverSession(ctx context.Context, generation uint64, err error) bool {
	b.mu.RLock()
	reconnect := b.reconnect
	eligible := b.reconnectPending || (b.connected && b.sessionLostLocked(err))
	b.mu.RUnlock()
	if reconnect == nil || !eligible {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.sessionGeneration != generation {
		return b.connected
	}
	if !b.connected && !b.reconnectPending {
		// Closed in the meantime; an explicit close is final.
		return false
	}

	if b.client != nil {
		_ = b.client.Close()
	}
	b.client = nil
	b.connected = false
	b.negotiatedProtocolVersion = ""
	b.reconnectPending = true

	logging.Info("MCPClient", "MCP session lost (%v); re-initializing", err)
	if rerr := reconnect(ctx); rerr != nil {
		logging.Warn("MCPClient", "Re-initialize after lost MCP session failed: %v", rerr)
		return false
	}

	b.reconnectPending = false
	b.sessionGeneration++
	return true
}
