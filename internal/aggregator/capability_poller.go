package aggregator

import (
	"time"

	"github.com/giantswarm/muster/pkg/logging"
)

// DefaultCapabilityPollInterval is how often the poller re-fetches
// capabilities from all connected downstream MCP servers. This catches
// silent redeployments that don't trigger a CR change or a
// notifications/tools/list_changed notification.
const DefaultCapabilityPollInterval = 5 * time.Minute

// runCapabilityPoller periodically re-fetches capabilities from every
// connected downstream MCP server and updates the registry/store when
// something changed.
//
// For non-authenticated servers (StatusConnected with a persistent
// ServerInfo.Client), it calls refreshNonOAuthCapabilities which
// deep-compares and updates ServerInfo.Tools/Resources/Prompts.
//
// For authenticated servers (StatusAuthRequired with per-session pooled
// clients), it iterates all active pooled sessions via
// SessionConnectionPool.GetSessionsForServer and calls
// refreshSessionCapabilities for each.
//
// All refresh calls are deduplicated via the shared notifRefreshGroup
// singleflight so that a poll overlapping with a notification-triggered
// refresh is coalesced.
func (a *AggregatorServer) runCapabilityPoller() {
	defer a.wg.Done()

	interval := a.config.CapabilityPollInterval
	if interval <= 0 {
		interval = DefaultCapabilityPollInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logging.Info("Aggregator", "Capability poller started (interval=%v)", interval)

	for {
		select {
		case <-a.ctx.Done():
			logging.Debug("Aggregator", "Capability poller stopped")
			return
		case <-ticker.C:
			a.pollAllServers()
		}
	}
}

// pollAllServers iterates all registered servers and re-fetches
// capabilities, using the existing refresh methods from
// notification_subscriber.go. Each refresh is deduplicated via the
// shared notifRefreshGroup singleflight.
func (a *AggregatorServer) pollAllServers() {
	servers := a.registry.GetAllServers()
	if len(servers) == 0 {
		return
	}

	logging.Debug("Aggregator", "Capability poll: checking %d servers", len(servers))

	for serverName, info := range servers {
		switch {
		case info.Status == StatusAuthRequired:
			a.pollAuthServer(serverName)

		case info.IsConnected() && info.Client != nil:
			a.pollNonAuthServer(serverName)
		}
	}
}

// pollNonAuthServer re-fetches capabilities for a non-authenticated
// server using the shared singleflight key "notif-caps/<serverName>".
func (a *AggregatorServer) pollNonAuthServer(serverName string) {
	sfKey := "notif-caps/" + serverName
	_, _, _ = a.notifRefreshGroup.Do(sfKey, func() (interface{}, error) {
		a.refreshNonOAuthCapabilities(serverName)
		return nil, nil
	})
}

// pollAuthServer iterates all active pooled sessions for an authenticated
// server and re-fetches capabilities for each. Servers with no active
// pooled clients are skipped.
func (a *AggregatorServer) pollAuthServer(serverName string) {
	if a.connPool == nil {
		return
	}

	sessions := a.connPool.GetSessionsForServer(serverName)
	if len(sessions) == 0 {
		logging.Debug("Aggregator", "Capability poll: no active sessions for auth server %s, skipping", serverName)
		return
	}

	ctx := a.refreshContext()

	for _, ps := range sessions {
		sfKey := ps.SessionID + "/" + serverName
		_, _, _ = a.notifRefreshGroup.Do(sfKey, func() (interface{}, error) {
			a.refreshSessionCapabilities(ctx, serverName, ps.SessionID, ps.Client)
			return nil, nil
		})
	}
}
