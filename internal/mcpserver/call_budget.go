package mcpserver

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// errBudgetSpent is the cause of a tool call cut because the server's
	// timeout passed with neither an answer nor progress.
	errBudgetSpent = errors.New("server timeout passed without an answer or progress")
	// errCallTooLong is the cause of a tool call cut at the server's
	// maxCallDuration.
	errCallTooLong = errors.New("call reached the server's maxCallDuration")
)

// callBudget is the time a tool call has left. It ends the call when the
// server's timeout passes with no progress from the server, and at the
// server's maxCallDuration in any case. A nil *callBudget extends nothing.
type callBudget struct {
	timeout time.Duration

	mu    sync.Mutex
	timer *time.Timer
}

// extend gives the call the full timeout again, counted from now. A call
// already cut stays cut.
func (b *callBudget) extend() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer.Stop() {
		b.timer.Reset(b.timeout)
	}
}

type callBudgetKey struct{}

func callBudgetFrom(ctx context.Context) *callBudget {
	budget, _ := ctx.Value(callBudgetKey{}).(*callBudget)
	return budget
}

// withCallBudget bounds ctx by timeout, which progress extends, and by
// maxCallDuration. cancel releases the timers.
func withCallBudget(ctx context.Context, timeout, maxCallDuration time.Duration) (context.Context, context.CancelFunc) {
	capped, cancelCap := context.WithTimeoutCause(ctx, maxCallDuration, errCallTooLong)
	opCtx, cancelOp := context.WithCancelCause(capped)
	budget := &callBudget{timeout: timeout}
	budget.timer = time.AfterFunc(timeout, func() { cancelOp(errBudgetSpent) })
	return context.WithValue(opCtx, callBudgetKey{}, budget), func() {
		budget.mu.Lock()
		budget.timer.Stop()
		budget.mu.Unlock()
		cancelOp(context.Canceled)
		cancelCap()
	}
}
