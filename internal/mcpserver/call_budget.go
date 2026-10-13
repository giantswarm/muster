package mcpserver

import (
	"context"
	"errors"
	"sync"
	"time"
)

// MaxProgressCallDuration bounds a tool call whose server keeps reporting
// progress. The server's timeout bounds the wait for an answer or the next
// progress notification; this bounds the call as a whole, so a server that
// reports forever still releases the caller.
const MaxProgressCallDuration = time.Hour

var (
	// errBudgetSpent is the cause of an operation cut because the server's
	// timeout passed with neither an answer nor progress.
	errBudgetSpent = errors.New("server timeout passed without an answer or progress")
	// errCallTooLong is the cause of a call cut at MaxProgressCallDuration.
	errCallTooLong = errors.New("call reached the longest a call may run")
)

// callBudget is the time an operation has left. It ends the operation when
// the server's timeout passes with no progress from the server, and at
// MaxProgressCallDuration in any case. A nil *callBudget extends nothing.
type callBudget struct {
	timeout time.Duration

	mu    sync.Mutex
	timer *time.Timer
}

// extend gives the operation the full timeout again, counted from now. An
// operation already cut stays cut.
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
// MaxProgressCallDuration. cancel releases the timers.
func withCallBudget(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	capped, cancelCap := context.WithTimeoutCause(ctx, MaxProgressCallDuration, errCallTooLong)
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
