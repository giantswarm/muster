package orchestrator

import (
	"context"
	"time"

	"github.com/giantswarm/muster/internal/api"
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
// service on a fixed interval; the service counts the failures and turns
// unhealthy at mcpserver.HealthCheckFailureThreshold, which withdraws its
// tools, and the orchestrator restarts it. A restart that fails hands the
// server to the existing reconnect schedule.

// HealthCheckInterval is the time between two health probes of a connected
// MCPServer. Overridable via MUSTER_ORCHESTRATOR_HEALTH_CHECK_INTERVAL (a Go
// duration) so the integration test harness can observe the recovery
// without waiting for production-length ticks.
var HealthCheckInterval = durationFromEnv("MUSTER_ORCHESTRATOR_HEALTH_CHECK_INTERVAL", 30*time.Second)

// HealthCheckTimeout bounds one probe. A backend that does not answer a ping
// within it counts as a failed probe.
const HealthCheckTimeout = 10 * time.Second

// checkConnectedServersHealth probes every connected or running MCPServer
// service once, concurrently, and restarts the ones that report unhealthy.
// A service whose previous probe or restart is still running is skipped.
func (o *Orchestrator) checkConnectedServersHealth() {
	for _, svc := range o.registry.GetByType(services.TypeMCPServer) {
		checker, ok := svc.(services.HealthChecker)
		if !ok || !isActiveState(svc.GetState()) {
			continue
		}
		if !o.beginHealthCheck(svc.GetName()) {
			continue
		}

		o.retryWg.Add(1)
		go func(svc services.Service, checker services.HealthChecker) {
			defer o.retryWg.Done()
			defer o.endHealthCheck(svc.GetName())
			o.probeAndRecover(svc, checker)
		}(svc, checker)
	}
}

// probeAndRecover runs one health probe and restarts the service when the
// probe reports it unhealthy. Failures below the service's threshold are the
// service's to log; nothing is done here.
func (o *Orchestrator) probeAndRecover(svc services.Service, checker services.HealthChecker) {
	if o.ctx.Err() != nil {
		return
	}

	probeCtx, cancel := context.WithTimeout(o.ctx, HealthCheckTimeout)
	health, err := checker.CheckHealth(probeCtx)
	cancel()
	if health != services.HealthUnhealthy {
		return
	}

	logging.Warn("Orchestrator", "MCPServer %s failed %d consecutive health checks, restarting: %v",
		svc.GetName(), mcpserver.HealthCheckFailureThreshold, err)

	if err := svc.Restart(o.ctx); err != nil {
		if api.IsAuthRequiredError(err) {
			// Pending auth registration happens in the auth-required hook inside Start.
			return
		}
		// Start scheduled its own reconnect attempts; the retry loop takes it from here.
		logging.Warn("Orchestrator", "Restart of unhealthy MCPServer %s failed: %v", svc.GetName(), err)
		return
	}
	logging.Info("Orchestrator", "Restarted unhealthy MCPServer %s", svc.GetName())
}

// beginHealthCheck marks a probe in flight for the named service and reports
// whether the caller got the slot.
func (o *Orchestrator) beginHealthCheck(name string) bool {
	o.healthMu.Lock()
	defer o.healthMu.Unlock()
	if o.healthChecksInFlight == nil {
		o.healthChecksInFlight = make(map[string]struct{})
	}
	if _, running := o.healthChecksInFlight[name]; running {
		return false
	}
	o.healthChecksInFlight[name] = struct{}{}
	return true
}

func (o *Orchestrator) endHealthCheck(name string) {
	o.healthMu.Lock()
	delete(o.healthChecksInFlight, name)
	o.healthMu.Unlock()
}

// isActiveState reports whether a service is up: Running for a stdio server,
// Connected for a remote one. Only those are probed; a failed or unreachable
// server is the reconnect schedule's, and an auth_required one has no shared
// client to ping.
func isActiveState(state services.ServiceState) bool {
	return state == services.StateRunning || state == services.StateConnected
}
