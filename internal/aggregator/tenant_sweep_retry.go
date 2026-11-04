package aggregator

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// A sweep of an organization namespace that fails (the token store does not
// answer, the MCPServers cannot be listed) is retried with backoff until it
// succeeds. A deleted namespace is never adopted again, so waiting for its
// next adoption would leave its people's sign-ins in the store for good
// (giantswarm/muster#1766).
//
// Every attempt reads the owner record and lists the MCPServer objects
// again, so a retry acts on the namespace as it is then: a namespace created
// again with the same name has new UIDs, and its servers' sign-ins stay;
// only the issuer keys of the gone servers go. A retry names no removed
// server of its own, so the session marks of a server key that is live again
// stay too. Servers of muster's namespace are never swept.
//
// The retries live in memory, one per namespace, so they are bounded by the
// namespaces muster serves; a failure in a namespace that already waits for a
// retry makes that retry run once more after it succeeds. There is no cap: a
// retry that gave up would leave the sign-ins behind. A retry ends with
// muster: after a restart the owner record still names the gone servers (on
// Valkey), and the namespace's next adoption sweeps them, as before.

const (
	tenantSweepInitialBackoff = time.Second
	tenantSweepMaxBackoff     = 2 * time.Minute
)

// tenantSweepRetries runs the retries of the namespaces whose sweep failed.
// The zero value is ready to use.
type tenantSweepRetries struct {
	mu      sync.Mutex
	pending map[string]*tenantSweepRetry
	stopped bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// wait is the pause before an attempt; nil is sleepCtx. Tests pass one
	// that hands the pause to the test instead of sleeping.
	wait func(context.Context, time.Duration) error
}

// tenantSweepRetry is the retry of one namespace's sweep.
type tenantSweepRetry struct {
	// again is set when the sweep failed again while this retry ran: the
	// retry runs once more after it succeeds.
	again bool
}

// schedule retries the sweep of namespace, whose sweep for server failed
// with err, until it succeeds, unless a retry of the namespace is running
// already (that one runs once more) or muster is stopping. It logs the
// failure with what follows it.
func (r *tenantSweepRetries) schedule(namespace, server string, err error, sweep func(context.Context) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	attrs := []slog.Attr{
		slog.String("namespace", namespace),
		slog.String("server", server),
		slog.String("error", err.Error()),
	}
	if r.stopped {
		logging.WarnWithAttrs("Aggregator", "tenant_signins_sweep_not_retried", append(attrs,
			slog.String("reason", "muster is stopping; the namespace's next adoption sweeps it"))...)
		return
	}
	if retry, ok := r.pending[namespace]; ok {
		retry.again = true
		logging.WarnWithAttrs("Aggregator", "tenant_signins_sweep_failed", append(attrs,
			slog.String("retry", "the namespace's pending retry runs once more"))...)
		return
	}
	logging.WarnWithAttrs("Aggregator", "tenant_signins_sweep_failed", append(attrs,
		slog.Int("attempt", 1),
		slog.Duration("retry_in", tenantSweepInitialBackoff))...)
	if r.pending == nil {
		r.pending = make(map[string]*tenantSweepRetry)
	}
	if r.ctx == nil {
		r.ctx, r.cancel = context.WithCancel(context.Background())
	}
	retry := &tenantSweepRetry{}
	r.pending[namespace] = retry
	wait := r.wait
	if wait == nil {
		wait = sleepCtx
	}
	r.wg.Add(1)
	go r.run(r.ctx, namespace, retry, sweep, wait)
}

// run makes attempt 2 and onwards of a namespace's sweep (the first one
// failed), doubling the pause from tenantSweepInitialBackoff up to
// tenantSweepMaxBackoff.
func (r *tenantSweepRetries) run(ctx context.Context, namespace string, retry *tenantSweepRetry, sweep func(context.Context) error, wait func(context.Context, time.Duration) error) {
	defer r.wg.Done()
	backoff := tenantSweepInitialBackoff
	for attempt := 2; ; attempt++ {
		if err := wait(ctx, backoff); err != nil {
			r.abandon(namespace, attempt)
			return
		}
		attemptCtx, cancel := context.WithTimeout(ctx, tenantForgetTimeout)
		err := sweep(attemptCtx)
		cancel()
		if err != nil && ctx.Err() != nil {
			// muster stopped during the attempt: the failure is the stop's.
			r.abandon(namespace, attempt)
			return
		}
		if err != nil {
			backoff = min(backoff*2, tenantSweepMaxBackoff)
			logging.WarnWithAttrs("Aggregator", "tenant_signins_sweep_failed",
				slog.String("namespace", namespace),
				slog.Int("attempt", attempt),
				slog.Duration("retry_in", backoff),
				slog.String("error", err.Error()))
			continue
		}
		r.mu.Lock()
		again := retry.again
		retry.again = false
		if !again {
			delete(r.pending, namespace)
		}
		r.mu.Unlock()
		if !again {
			logging.InfoWithAttrs("Aggregator", "tenant_signins_sweep_retried",
				slog.String("namespace", namespace),
				slog.Int("attempts", attempt))
			return
		}
		backoff = tenantSweepInitialBackoff
	}
}

// abandon forgets a namespace's retry when muster stops before attempt.
func (r *tenantSweepRetries) abandon(namespace string, attempt int) {
	r.mu.Lock()
	delete(r.pending, namespace)
	r.mu.Unlock()
	logging.WarnWithAttrs("Aggregator", "tenant_signins_sweep_not_retried",
		slog.String("namespace", namespace),
		slog.Int("attempt", attempt),
		slog.String("reason", "muster is stopping; the namespace's next adoption sweeps it"))
}

// stop ends the retries and waits for them; none starts afterwards.
func (r *tenantSweepRetries) stop() {
	r.mu.Lock()
	r.stopped = true
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
	r.wg.Wait()
}
