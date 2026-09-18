package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/giantswarm/muster/v5/pkg/logging"
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

// DefaultTimeout bounds an operation on a remote server whose definition
// sets no spec.timeout. It is that field's CRD default; the service layer's
// DefaultRemoteTimeout is derived from it.
const DefaultTimeout = 30 * time.Second

// operationContext bounds ctx by the client's timeout, DefaultTimeout when
// unset, and returns the budget applied. Every operation runs under it,
// the recovery handshake included: that one runs with the client's write
// lock held, so every other operation on the client, Close included, waits
// for it, and the caller's context alone may carry no deadline.
func (b *baseMCPClient) operationContext(ctx context.Context) (context.Context, context.CancelFunc, time.Duration) {
	timeout := b.timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	return ctx, cancel, timeout
}

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

// withSessionRecovery runs op under the client's timeout and, when it failed
// because the MCP session is gone, performs one fresh handshake and runs op
// a second time within the same budget. Any other failure is returned as is,
// except the budget running out, which is reported as the deadline it is.
func withSessionRecovery[T any](b *baseMCPClient, ctx context.Context, op func(context.Context) (T, error)) (T, error) {
	opCtx, cancel, timeout := b.operationContext(ctx)
	defer cancel()

	b.mu.RLock()
	generation := b.sessionGeneration
	b.mu.RUnlock()

	result, err := op(opCtx)
	if err == nil {
		return result, nil
	}
	if retry, rerr := b.recoverSession(opCtx, generation, err); retry {
		result, err = op(opCtx)
	} else {
		err = rerr
	}
	return result, timedOut(opCtx, ctx, timeout, err)
}

// timedOut replaces err with the deadline error when the operation's own
// budget ran out. mcp-go reports the cut as a failure of the POST ("transport
// error: failed to send request: Post ...: context deadline exceeded"), which
// reads as a network fault when the backend simply took longer than the
// server's spec.timeout allows. A caller's context that ended first is left
// as it is.
func timedOut(opCtx, ctx context.Context, timeout time.Duration, err error) error {
	if err == nil || !errors.Is(opCtx.Err(), context.DeadlineExceeded) || ctx.Err() != nil {
		return err
	}
	return fmt.Errorf("no answer within the server's timeout of %s: %w", timeout, context.DeadlineExceeded)
}

// recoverSession re-establishes the session after an operation started on
// the given generation failed with err. It reports whether the client is
// connected on a newer session and the operation should be retried, and
// otherwise the error to return: err itself, or err joined with the cause
// when the handshake failed, so a 401 from the backend still reads as an
// AuthRequiredError to callers that test for one.
//
// Concurrent callers that failed on the same dead session serialise on the
// write lock; the first performs the handshake, the others find the
// generation advanced and act on its result, whether it succeeded or not. A
// handshake that fails leaves the client disconnected with reconnectPending
// set, so the next operation tries again instead of failing until the
// service is restarted.
func (b *baseMCPClient) recoverSession(ctx context.Context, generation uint64, err error) (bool, error) {
	b.mu.RLock()
	reconnect := b.reconnect
	eligible := b.reconnectPending || (b.connected && b.sessionLostLocked(err))
	b.mu.RUnlock()
	if reconnect == nil || !eligible {
		return false, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.sessionGeneration != generation {
		if b.connected {
			return true, nil
		}
		if b.reconnectPending && b.recoveryErr != nil {
			return false, fmt.Errorf("%w; re-initialize failed: %w", err, b.recoveryErr)
		}
		return false, err
	}
	if !b.connected && !b.reconnectPending {
		// Closed in the meantime; an explicit close is final.
		return false, err
	}

	if b.client != nil {
		_ = b.client.Close()
	}
	b.client = nil
	b.connected = false
	b.negotiatedProtocolVersion = ""
	b.reconnectPending = true
	// Advanced before the attempt, so the callers queued behind this one do
	// not each repeat a handshake that has just failed against the same
	// backend; the next operation is the one that tries again.
	b.sessionGeneration++

	logging.Info("MCPClient", "MCP session lost (%v); re-initializing", err)
	if rerr := reconnect(ctx); rerr != nil {
		logging.Warn("MCPClient", "Re-initialize after lost MCP session failed: %v", rerr)
		b.recoveryErr = rerr
		return false, fmt.Errorf("%w; re-initialize failed: %w", err, rerr)
	}

	b.reconnectPending = false
	b.recoveryErr = nil
	return true, nil
}
