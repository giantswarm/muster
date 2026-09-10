package orchestrator

import (
	"time"

	"github.com/giantswarm/muster/internal/services"
	"github.com/giantswarm/muster/internal/services/mcpserver"
	"github.com/giantswarm/muster/pkg/logging"
)

// Runtime health probing of connected MCPServers.
//
// A server that reached Connected/Running stayed there whatever happened to
// the connection afterwards: the service's CheckHealth existed but nothing
// called it once the initial connect had succeeded, so a backend that died,
// hung, or lost the MCP session kept its tools listed and every call failed
// (issues #493, #999). The orchestrator now probes every active MCPServer
// service on a fixed interval. The service owns the outcome: it counts the
// failures and, at mcpserver.HealthCheckFailureThreshold, closes its client
// and moves to Failed with a reconnect due at once. From there the retry loop
// -- the one place that restarts servers, with the concurrency bound and the
// state checks it already has -- reconnects it on its next tick, and a
// reconnect that fails follows the usual backoff.

// HealthCheckInterval is the time between two health probes of a connected
// MCPServer. Overridable via MUSTER_ORCHESTRATOR_HEALTH_CHECK_INTERVAL (a Go
// duration) so the integration test harness can observe the recovery
// without waiting for production-length ticks. Each probe is bounded by the
// server's own timeout (spec.timeout), see mcpserver.Service.CheckHealth.
var HealthCheckInterval = durationFromEnv("MUSTER_ORCHESTRATOR_HEALTH_CHECK_INTERVAL", 30*time.Second)

// checkConnectedServersHealth probes every connected or running MCPServer
// service once, concurrently. Nothing else happens here: a server the probes
// have failed is Failed with a reconnect due, and the retry loop takes it.
func (o *Orchestrator) checkConnectedServersHealth() {
	if o.ctx.Err() != nil {
		return
	}
	for _, svc := range o.registry.GetByType(services.TypeMCPServer) {
		checker, ok := svc.(services.HealthChecker)
		if !ok || !isActiveState(svc.GetState()) {
			continue
		}

		o.retryWg.Add(1)
		go func(name string, checker services.HealthChecker) {
			defer o.retryWg.Done()
			if health, err := checker.CheckHealth(o.ctx); health == services.HealthUnhealthy {
				logging.Warn("Orchestrator", "MCPServer %s failed %d consecutive health checks; reconnecting on the next retry tick: %v",
					name, mcpserver.HealthCheckFailureThreshold, err)
			}
		}(svc.GetName(), checker)
	}
}

// isActiveState reports whether a service is up: Running for a stdio server,
// Connected for a remote one. Only those are probed; a failed or unreachable
// server is the reconnect schedule's, and an auth_required one has no shared
// client to ping.
func isActiveState(state services.ServiceState) bool {
	return state == services.StateRunning || state == services.StateConnected
}
