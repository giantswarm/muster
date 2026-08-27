package server

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/giantswarm/mcp-oauth/security"
)

const (
	// dexAudienceRefreshInterval is how often the refresher re-reads the set.
	// It bounds how long a newly registered MCPServer waits before its
	// requiredAudiences reach a login.
	dexAudienceRefreshInterval = 10 * time.Second

	// dexAudienceTimeout bounds one collector call, which lists MCPServers
	// through an uncached Kubernetes client.
	dexAudienceTimeout = 5 * time.Second

	// EventDexAudiencesChanged records a change in the cross-client audiences
	// muster requests from Dex. The set comes from MCPServer requiredAudiences,
	// so a change means an MCPServer write widened or narrowed the audience of
	// every ID token minted from that point on. Nothing else records that: the
	// set now changes without a muster restart, and mcp-oauth logs only the
	// audiences it rejects.
	EventDexAudiencesChanged = "dex_audiences_changed"
)

// dexAudienceResolver reports the Dex cross-client audiences a login requests.
//
// Resolve runs on the authorization request path and does no I/O.
// dex.Config.AudienceResolver takes no context, runs from request goroutines,
// and can run twice for one authorization request, so it must read cached state
// and return. A background refresher holds that state current instead.
//
// A read that fails leaves the last known set in place. An audience muster
// cannot confirm is not the same as an audience no MCPServer needs, and a token
// minted without one cannot be repaired without a new login.
//
// Nothing ages the last known set out. Across a long apiserver outage, muster
// keeps requesting an audience that no MCPServer needs any more, which mints a
// wider token than necessary. Dropping the set instead mints tokens that the
// backend rejects, and no refresh repairs those.
type dexAudienceResolver struct {
	collect  func(context.Context) ([]string, error)
	interval time.Duration
	timeout  time.Duration
	logger   *slog.Logger

	// baseCtx bounds every collector call and is cancelled by stop, so a
	// shutdown does not wait out timeout on a read that is in flight.
	baseCtx    context.Context
	baseCancel context.CancelFunc

	mu sync.RWMutex
	// haveKnown reports whether known holds the result of a successful read.
	// A successful read of zero audiences is a known empty set, not an
	// unknown one.
	haveKnown    bool
	known        []string
	haveReported bool
	reported     []string
	started      bool

	auditor *security.Auditor

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// newDexAudienceResolver returns a resolver over collect. The caller must call
// prime before serving requests and start to keep the set current.
func newDexAudienceResolver(collect func(context.Context) ([]string, error), logger *slog.Logger) *dexAudienceResolver {
	baseCtx, baseCancel := context.WithCancel(context.Background())

	return &dexAudienceResolver{
		collect:    collect,
		interval:   dexAudienceRefreshInterval,
		timeout:    dexAudienceTimeout,
		logger:     logger,
		baseCtx:    baseCtx,
		baseCancel: baseCancel,
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
}

// Resolve satisfies dex.Config.AudienceResolver.
//
// The caller receives a copy, because the Dex provider appends to the slice it
// receives.
func (r *dexAudienceResolver) Resolve() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return slices.Clone(r.known)
}

// prime reads the set once, synchronously. The caller runs it before the OAuth
// server serves its first request, so an early login does not race the
// refresher.
func (r *dexAudienceResolver) prime() {
	r.refresh()
}

// start records the primed set and refreshes it until stop returns. auditor may
// be nil.
func (r *dexAudienceResolver) start(auditor *security.Auditor) {
	r.mu.Lock()
	r.auditor = auditor
	r.started = true
	r.mu.Unlock()

	r.report()

	go func() {
		defer close(r.doneCh)

		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()

		for {
			select {
			case <-r.stopCh:
				return
			case <-ticker.C:
				r.refresh()
				r.report()
			}
		}
	}()
}

// stop ends the refresher and waits for it to return. It cancels a read that is
// in flight, so it returns without waiting out timeout.
func (r *dexAudienceResolver) stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		r.baseCancel()
	})

	r.mu.RLock()
	started := r.started
	r.mu.RUnlock()

	if started {
		<-r.doneCh
	}
}

// refresh reads a new set. A read that fails leaves the last known set in
// place, so a failing apiserver cannot narrow the audience of a minted token.
func (r *dexAudienceResolver) refresh() {
	ctx, cancel := context.WithTimeout(r.baseCtx, r.timeout)
	defer cancel()

	audiences, err := r.collect(ctx)

	r.mu.Lock()
	haveKnown, known := r.haveKnown, r.known
	if err == nil {
		r.haveKnown = true
		r.known = audiences
	}
	r.mu.Unlock()

	switch {
	case err == nil:
	case !haveKnown:
		r.logger.Error("Cannot read the cross-client audiences MCPServers require, and none are known yet",
			"error", err,
			"effect", "a login mints an ID token carrying muster's own client only, and a forwardToken backend rejects it")
	default:
		r.logger.Warn("Cannot read the cross-client audiences MCPServers require, serving the last known set",
			"error", err,
			"audiences", known)
	}
}

// report records the set on the first successful read and on each change. An
// unknown set is not reported: the failed read already carries its own line,
// and an empty set logged next to it reads as "no MCPServer needs an audience".
func (r *dexAudienceResolver) report() {
	r.mu.Lock()
	if !r.haveKnown || (r.haveReported && slices.Equal(r.reported, r.known)) {
		r.mu.Unlock()
		return
	}
	previous := r.reported
	current := slices.Clone(r.known)
	r.reported = current
	r.haveReported = true
	auditor := r.auditor
	r.mu.Unlock()

	r.logger.Info("Cross-client audiences requested from Dex",
		"audiences", current,
		"count", len(current))

	if auditor == nil {
		return
	}
	auditor.LogEvent(context.Background(), security.Event{
		Type: EventDexAudiencesChanged,
		Details: map[string]any{
			"audiences": current,
			"previous":  previous,
			"source":    "MCPServer requiredAudiences",
		},
	})
}
