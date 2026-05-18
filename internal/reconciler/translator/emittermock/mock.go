package emittermock

import (
	"context"
	"sync"

	"github.com/giantswarm/muster/internal/reconciler/translator"
)

// Emitter is a ConfigEmitter that records every Emit call and optionally
// returns a configured error. It is safe for concurrent use.
type Emitter struct {
	mu    sync.Mutex
	calls []translator.Model
	err   error
}

// New returns a fresh Emitter that succeeds for every call.
func New() *Emitter {
	return &Emitter{}
}

// Emit records m and returns the configured error (nil by default). It
// honors context cancellation: a canceled ctx returns ctx.Err() without
// recording the call.
func (e *Emitter) Emit(ctx context.Context, m translator.Model) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, m)
	return e.err
}

// SetError configures the error returned by subsequent Emit calls. Pass
// nil to clear the error.
func (e *Emitter) SetError(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.err = err
}

// Calls returns a snapshot of every Model passed to Emit in order.
func (e *Emitter) Calls() []translator.Model {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]translator.Model, len(e.calls))
	copy(out, e.calls)
	return out
}

// Reset clears the recorded calls. The configured error is preserved.
func (e *Emitter) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = nil
}
