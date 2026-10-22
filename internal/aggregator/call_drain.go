package aggregator

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// callDrainGrace is how long a stopping aggregator lets in-flight tool calls
// finish before it interrupts the rest. It leaves the interrupted answers and
// the transport shutdown room inside the orchestrator's ShutdownTimeout.
const callDrainGrace = 10 * time.Second

// errReplicaStopping is the cause of a tool call interrupted because this
// muster replica is shutting down.
var errReplicaStopping = errors.New("muster replica is shutting down")

// interruptedText answers a tool call a stopping replica interrupted. It
// names the interruption, so a caller does not read the backend's
// cancellation (a wait's `timeout` verdict, say) as the tool's answer.
const interruptedText = "interrupted: the muster replica serving this call is shutting down (rolling update), " +
	"so the call did not finish and its outcome is unknown. Call the tool again to continue: " +
	"another replica serves it. muster never re-issues a call on its own, so check a write tool's " +
	"effect before repeating it."

// interruptedMetaKey marks an interrupted answer in its _meta for clients
// that act on it without reading the text.
const interruptedMetaKey = "giantswarm.io/interrupted"

// inFlightCalls tracks the tool calls an aggregator is serving, so a stop
// drains them: it refuses new calls, lets the in-flight ones finish within
// a grace period and interrupts the rest, each with an answer naming the
// interruption. A progress-kept call may run up to an hour, longer than any
// pod's termination grace period; its caller calls again and another
// replica serves it.
type inFlightCalls struct {
	mu       sync.Mutex
	stopping bool
	cancels  map[*context.CancelCauseFunc]struct{}
	wg       sync.WaitGroup
}

func newInFlightCalls() *inFlightCalls {
	return &inFlightCalls{cancels: make(map[*context.CancelCauseFunc]struct{})}
}

// middleware returns the ToolHandlerMiddleware that runs every tool call as
// an in-flight call: on a context the drain can interrupt, answered
// interrupted once it did, or at once while the replica is stopping.
func (c *inFlightCalls) middleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			if !c.add(&cancel) {
				return interruptedResult(), nil
			}
			defer c.done(&cancel)

			result, err := next(ctx, req)
			if errors.Is(context.Cause(ctx), errReplicaStopping) {
				return interruptedResult(), nil
			}
			return result, err
		}
	}
}

// add registers a call; it reports false once the drain started.
func (c *inFlightCalls) add(cancel *context.CancelCauseFunc) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopping {
		return false
	}
	c.cancels[cancel] = struct{}{}
	c.wg.Add(1)
	return true
}

func (c *inFlightCalls) done(cancel *context.CancelCauseFunc) {
	c.mu.Lock()
	delete(c.cancels, cancel)
	c.mu.Unlock()
	c.wg.Done()
}

// drain refuses new calls, waits up to grace for the in-flight ones, then
// interrupts those left and waits for their answers until ctx ends.
func (c *inFlightCalls) drain(ctx context.Context, grace time.Duration) {
	c.mu.Lock()
	c.stopping = true
	n := len(c.cancels)
	c.mu.Unlock()
	if n == 0 {
		return
	}
	logging.Info("Aggregator", "Draining %d in-flight tool calls for up to %v", n, grace)

	graceCtx, cancelGrace := context.WithTimeout(ctx, grace)
	defer cancelGrace()
	if waitDone(graceCtx, &c.wg) {
		return
	}

	c.mu.Lock()
	n = len(c.cancels)
	for cancel := range c.cancels {
		(*cancel)(errReplicaStopping)
	}
	c.mu.Unlock()
	logging.Info("Aggregator", "Interrupted %d tool calls still in flight", n)
	waitDone(ctx, &c.wg)
}

// waitDone waits for wg and reports whether it finished before ctx ended.
func waitDone(ctx context.Context, wg *sync.WaitGroup) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func interruptedResult() *mcp.CallToolResult {
	result := mcp.NewToolResultError(interruptedText)
	result.Meta = mcp.NewMetaFromMap(map[string]any{
		interruptedMetaKey: map[string]any{"reason": errReplicaStopping.Error(), "retryable": true},
	})
	return result
}
