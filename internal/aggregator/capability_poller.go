package aggregator

import (
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

// CapabilityPollInterval is the time between two re-listings of every
// connected server's capabilities. Overridable via
// MUSTER_AGGREGATOR_CAPABILITY_POLL_INTERVAL (a Go duration). The ticker runs
// on the process clock (internal/clock), so the integration test harness
// reaches a poll by advancing the clock.
var CapabilityPollInterval = config.DurationFromEnv("MUSTER_AGGREGATOR_CAPABILITY_POLL_INTERVAL", 5*time.Minute)

// capabilityPollConcurrency bounds how many servers or sessions one poll
// re-lists at the same time. An installation has close to a hundred servers
// and many pooled sessions; three listings each, all at once, would be a
// burst against every backend every interval.
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
			a.pollCapabilities()
		}
	}
}

// pollCapabilities re-lists the capabilities of every connected server once:
// servers with a shared client through it, per-session servers through each
// live pooled connection. The listings run capabilityPollConcurrency at a
// time and the call returns when all of them are done, so two polls never
// overlap.
func (a *AggregatorServer) pollCapabilities() {
	var jobs []func()
	for name, info := range a.registry.GetAllServers() {
		switch {
		case info.RequiresSessionAuth():
			for _, ps := range a.pooledSessions(name) {
				jobs = append(jobs, func() { a.pollSessionCapabilities(name, ps) })
			}
		case info.IsConnected():
			jobs = append(jobs, func() { a.pollServerCapabilities(name) })
		}
	}
	if len(jobs) == 0 {
		return
	}
	logging.Debug("Aggregator", "Capability poll: re-listing %d connections", len(jobs))

	ctx := a.refreshContext()
	var wg sync.WaitGroup
	slots := make(chan struct{}, capabilityPollConcurrency)
	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			job()
		}()
	}
	wg.Wait()
}

// pooledSessions returns the live pooled connections to serverName whose
// token, when one is tracked, has not expired: a listing through an expired
// exchanged token would only collect the 401 that the next tool call
// re-exchanges for.
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
		live = append(live, ps)
	}
	return live
}

// pollServerCapabilities re-lists a server with a shared client, coalesced
// with a notification-driven refresh of the same server.
func (a *AggregatorServer) pollServerCapabilities(serverName string) {
	_, _, _ = a.notifRefreshGroup.Do(nonOAuthRefreshKey(serverName), func() (any, error) {
		a.refreshNonOAuthCapabilities(serverName, refreshByPoll)
		return nil, nil
	})
}

// pollSessionCapabilities re-lists a per-session server through one live
// pooled connection, into that session's capability store entry, coalesced
// with a notification-driven refresh of the same pair.
func (a *AggregatorServer) pollSessionCapabilities(serverName string, ps PooledSession) {
	_, _, _ = a.notifRefreshGroup.Do(sessionRefreshKey(ps.SessionID, serverName), func() (any, error) {
		a.refreshSessionCapabilities(a.refreshContext(), serverName, ps.SessionID, ps.Client, refreshByPoll)
		return nil, nil
	})
}
