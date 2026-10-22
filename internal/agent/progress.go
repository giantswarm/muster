package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// MaxCallDuration bounds a tool call that progress keeps going: each
// notifications/progress of the call restarts the call timeout, up to this.
// It matches the aggregator's largest progress.maxDuration.
const MaxCallDuration = time.Hour

// Progress is one notifications/progress the aggregator sent for a tool call
// of this client.
type Progress struct {
	Progress float64
	Total    *float64
	Message  string
}

// SetProgressHandler registers a handler that receives the progress of every
// tool call of this client; nil drops it. The handler runs on the transport's
// notification goroutine, so it must not block for long.
func (c *Client) SetProgressHandler(handler func(Progress)) {
	c.progress.mu.Lock()
	c.progress.handler = handler
	c.progress.mu.Unlock()
}

// callProgress routes the aggregator's notifications/progress to the tool
// calls of this client in flight. Every tool call sends a progressToken of
// its own, so a tool that blocks until a verdict and reports progress
// meanwhile keeps the call alive past the call timeout. The zero value is
// ready to use.
type callProgress struct {
	next atomic.Uint64

	mu      sync.Mutex
	handler func(Progress)
	calls   map[string]func()
}

// open mints a progressToken for one call; every progress notification
// carrying it calls extend and the progress handler, until close is called.
func (p *callProgress) open(extend func()) (token string, close func()) {
	token = fmt.Sprintf("muster-cli-%d", p.next.Add(1))

	p.mu.Lock()
	if p.calls == nil {
		p.calls = make(map[string]func())
	}
	p.calls[token] = extend
	p.mu.Unlock()

	return token, func() {
		p.mu.Lock()
		delete(p.calls, token)
		p.mu.Unlock()
	}
}

// route hands a notifications/progress to the call its token belongs to and
// reports whether one did.
func (p *callProgress) route(notification mcp.JSONRPCNotification) bool {
	if notification.Method != string(mcp.MethodNotificationProgress) {
		return false
	}
	fields := notification.Params.AdditionalFields
	token, _ := fields["progressToken"].(string)

	p.mu.Lock()
	extend, known := p.calls[token]
	handler := p.handler
	p.mu.Unlock()
	if !known {
		return false
	}
	extend()

	if handler == nil {
		return true
	}
	update := Progress{}
	update.Progress, _ = fields["progress"].(float64)
	if total, ok := fields["total"].(float64); ok {
		update.Total = &total
	}
	update.Message, _ = fields["message"].(string)
	handler(update)
	return true
}

// errNoProgress wraps context.DeadlineExceeded, so a call cut for want of an
// answer or progress reads as the timeout it is.
var errNoProgress = fmt.Errorf("no answer or progress within the call timeout: %w", context.DeadlineExceeded)

// withProgressTimeout bounds ctx by timeout, which every call of extend
// restarts, and by MaxCallDuration (or timeout, when that is longer). cause
// returns why the context ended.
func withProgressTimeout(ctx context.Context, timeout time.Duration) (callCtx context.Context, extend func(), cause func() error, cancel context.CancelFunc) {
	capped, cancelCap := context.WithTimeout(ctx, max(timeout, MaxCallDuration))
	callCtx, cancelCall := context.WithCancelCause(capped)

	var mu sync.Mutex
	timer := time.AfterFunc(timeout, func() { cancelCall(errNoProgress) })
	extend = func() {
		mu.Lock()
		defer mu.Unlock()
		if timer.Stop() {
			timer.Reset(timeout)
		}
	}
	cause = func() error {
		if err := context.Cause(callCtx); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
	return callCtx, extend, cause, func() {
		mu.Lock()
		timer.Stop()
		mu.Unlock()
		cancelCall(context.Canceled)
		cancelCap()
	}
}
