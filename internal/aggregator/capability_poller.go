package aggregator

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// Periodic re-listing of downstream capabilities.
//
// The aggregator learns a server's tools at registration and afterwards only
// when the server says they changed (notifications/tools/list_changed, see
// notification_subscriber.go) or a definition change re-registers it. A
// backend redeployed behind its Service with a different tool set does
// neither: the new process has never seen muster's session, so it sends no
// notification, and the session recovery (internal/mcpserver) and the health
// probe (internal/orchestrator) heal the connection without re-listing. The
// stale list stays until the next restart: tools the new image dropped keep
// failing with "tool not found", tools it added stay invisible (issue #494).
//
// The poller re-lists every connected server on an interval and updates what
// changed, through the same refresh paths and the same singleflight keys as
// the notification-driven refresh, so a poll that overlaps a notification is
// one re-fetch. A server with a shared client is re-listed once; a server
// served per session is re-listed through each live pooled connection, into
// that session's capability store entry. Nothing is polled that is not
// connected: a server whose service is down, and a per-session server no
// session currently holds a connection to, cost nothing.
//
// A poll is not a burst. Its listings are walked over the interval, one every
// interval/n for n connections, in the order of the connection keys, so a
// connection keeps its slot from one poll to the next and each backend sees
// one listing per interval at a steady, low rate; a backend with a per-caller
// rate limit is not met by every pooled session's listing at once. Each
// listing asks only for what the server declared at its handshake
// (relistDeclared): a tools-only server costs one request. The walk runs on
// the process clock, so a clock the test harness advances by the interval
// makes every remaining listing due at once.

// CapabilityPollInterval is the time between two re-listings of every
// connected server's capabilities, and the span one poll's listings are
// spread over. Overridable via MUSTER_AGGREGATOR_CAPABILITY_POLL_INTERVAL (a
// Go duration). The ticker runs on the process clock (internal/clock), so the
// integration test harness reaches a poll by advancing the clock.
var CapabilityPollInterval = config.DurationFromEnv("MUSTER_AGGREGATOR_CAPABILITY_POLL_INTERVAL", 5*time.Minute)

// capabilityPollConcurrency bounds how many servers or sessions one poll
// re-lists at the same time. The walk starts one listing per spacing; the
// bound matters when many become due together, after a clock advanced by
// more than one spacing or behind listings slower than the spacing.
const capabilityPollConcurrency = 8

// runCapabilityPoller re-lists every connected server's capabilities every
// interval until the aggregator's context ends. Counted by a.wg.
func (a *AggregatorServer) runCapabilityPoller(interval time.Duration) {
	defer a.wg.Done()

	ticker := clock.NewTicker(interval)
	defer ticker.Stop()

	logging.Info("Aggregator", "Capability poller started (interval %s)", interval)
	for {
		select {
		case <-a.ctx.Done():
			logging.Debug("Aggregator", "Capability poller stopped")
			return
		case <-ticker.C:
			a.pollCapabilities(interval)
		}
	}
}

// pollJob is one listing of a poll: a server with a shared client, or a
// per-session server through one pooled connection. key orders the walk.
type pollJob struct {
	key string
	run func()
}

// pollCapabilities re-lists the capabilities of every connected server once:
// servers with a shared client through it, per-session servers through each
// live pooled connection. The listings are walked over interval, one every
// interval/n in key order, at most capabilityPollConcurrency at a time, and
// the call returns when all of them are done, so two polls never overlap; a
// poll whose walk outlasts the interval delays the next tick, which the
// ticker holds for it.
func (a *AggregatorServer) pollCapabilities(interval time.Duration) {
	jobs := a.pollJobs()
	if len(jobs) == 0 {
		return
	}
	walk := newPollWalk(len(jobs), interval)
	logging.Debug("Aggregator", "Capability poll: re-listing %d connections, one every %s", len(jobs), walk.spacing)

	ctx := a.refreshContext()
	var wg sync.WaitGroup
	slots := make(chan struct{}, capabilityPollConcurrency)
	defer wg.Wait()

	var pace *clock.Ticker
	if len(jobs) > 1 {
		pace = clock.NewTicker(walk.spacing)
		defer pace.Stop()
	}
	for started := 0; started < len(jobs); {
		for started < walk.due(clock.Now()) {
			if ctx.Err() != nil {
				return
			}
			slots <- struct{}{}
			wg.Add(1)
			go func(job pollJob) {
				defer wg.Done()
				defer func() { <-slots }()
				job.run()
			}(jobs[started])
			started++
		}
		if started == len(jobs) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-pace.C:
		}
	}
}

// pollJobs collects one job per connection to re-list, sorted by key: shared
// servers under their name, pooled sessions under server and session.
func (a *AggregatorServer) pollJobs() []pollJob {
	var jobs []pollJob
	for name, info := range a.registry.GetAllServers() {
		switch {
		case info.RequiresSessionAuth():
			for _, ps := range a.pooledSessions(name) {
				jobs = append(jobs, pollJob{
					key: name + "/" + ps.SessionID,
					run: func() { a.pollSessionCapabilities(name, ps) },
				})
			}
		case info.IsConnected():
			jobs = append(jobs, pollJob{key: name, run: func() { a.pollServerCapabilities(name) }})
		}
	}
	slices.SortFunc(jobs, func(x, y pollJob) int { return strings.Compare(x.key, y.key) })
	return jobs
}

// pollWalk spaces the n listings of one poll over an interval: listing i is
// due at start + i*spacing, so the first starts with the tick and the last
// before the next one.
type pollWalk struct {
	start   time.Time
	spacing time.Duration
	n       int
}

// newPollWalk plans n listings over interval from now on the process clock.
// The spacing is at least a millisecond: finer than that, a walk is a burst.
func newPollWalk(n int, interval time.Duration) pollWalk {
	return pollWalk{
		start:   clock.Now(),
		spacing: max(interval/time.Duration(n), time.Millisecond),
		n:       n,
	}
}

// due returns how many of the listings are due at now: the ones whose slot
// has passed, all of them once the clock has moved past the last slot. A
// clock advanced by more than one spacing makes every skipped listing due at
// once.
func (w pollWalk) due(now time.Time) int {
	if now.Before(w.start) {
		return 0
	}
	return min(w.n, int(now.Sub(w.start)/w.spacing)+1)
}

// pooledSessions returns the live pooled connections to serverName whose
// token, when one is tracked, has not expired: a listing through an expired
// exchanged token would only collect the 401 that the next tool call
// re-exchanges for. A session whose forwarded bearer has expired is ended
// here instead of listed (bearer_session.go).
func (a *AggregatorServer) pooledSessions(serverName string) []PooledSession {
	if a.connPool == nil {
		return nil
	}
	now := time.Now()
	var live []PooledSession
	for _, ps := range a.connPool.SessionsForServer(serverName) {
		if !ps.TokenExpiry.IsZero() && !now.Before(ps.TokenExpiry) {
			continue
		}
		if a.endExpiredBearerSession(ps.SessionID) {
			continue
		}
		live = append(live, ps)
	}
	return live
}

// pollServerCapabilities re-lists a server with a shared client, coalesced
// with a notification-driven refresh of the same server. A server that lost
// its connection since the walk was planned is left to its reconnect, which
// lists it.
func (a *AggregatorServer) pollServerCapabilities(serverName string) {
	if info, ok := a.registry.GetServerInfo(serverName); !ok || !info.IsConnected() {
		logging.Debug("Aggregator", "Capability poll: %s no longer connected, skipped", serverName)
		return
	}
	_, _, _ = a.notifRefreshGroup.Do(nonOAuthRefreshKey(serverName), func() (any, error) {
		a.refreshNonOAuthCapabilities(serverName, refreshByPoll)
		return nil, nil
	})
}

// pollSessionCapabilities re-lists a per-session server through one live
// pooled connection, into that session's capability store entry, coalesced
// with a notification-driven refresh of the same pair. A connection the pool
// evicted or replaced since the walk was planned is left alone: the next
// tool call of the session connects and lists anew. So is one whose session's
// forwarded bearer expired since: the session is ended instead.
func (a *AggregatorServer) pollSessionCapabilities(serverName string, ps PooledSession) {
	if a.endExpiredBearerSession(ps.SessionID) {
		logging.Debug("Aggregator", "Capability poll: session %s ended with its forwarded bearer, %s not listed",
			logging.TruncateIdentifier(ps.SessionID), serverName)
		return
	}
	if !a.connPool.Holds(ps.SessionID, serverName, ps.Client) {
		logging.Debug("Aggregator", "Capability poll: session %s no longer holds a connection to %s, skipped",
			logging.TruncateIdentifier(ps.SessionID), serverName)
		return
	}
	_, _, _ = a.notifRefreshGroup.Do(sessionRefreshKey(ps.SessionID, serverName), func() (any, error) {
		a.refreshSessionCapabilities(a.refreshContext(), serverName, ps.SessionID, ps.Client, refreshByPoll)
		return nil, nil
	})
}
