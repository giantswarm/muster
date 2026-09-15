package testing

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/giantswarm/muster/internal/clock"
)

// Interval schedules an instance's lifecycle timers run on, selected by
// pre_configuration.intervals.
const (
	// IntervalsShort (the default) runs the reconnect backoff, the
	// orchestrator's retry and health ticks, the reconciler's resync and the
	// core catalogue's age in seconds through environment knobs, so a
	// scenario sees them act within its wait_for_state budgets.
	IntervalsShort = "short"
	// IntervalsProduction keeps every timer the controllable clock reaches on
	// its production default (a 30 s initial backoff capped at 2 min, 30 s
	// retry and health ticks, a 5 min catalogue age); a scenario moves them
	// with test_advance_clock. The reconciler's resync is controller-runtime's
	// and out of the clock's reach, so it stays shortened.
	IntervalsProduction = "production"
)

// instanceIntervals returns the interval schedule a pre-configuration asks
// for, IntervalsShort when it names none.
func instanceIntervals(config *MusterPreConfiguration) string {
	if config == nil || config.Intervals == "" {
		return IntervalsShort
	}
	return strings.ToLower(config.Intervals)
}

// validateIntervalsConfig rejects an intervals value the harness does not know.
func validateIntervalsConfig(config *MusterPreConfiguration) error {
	if config == nil || config.Intervals == "" {
		return nil
	}
	switch instanceIntervals(config) {
	case IntervalsShort, IntervalsProduction:
		return nil
	default:
		return fmt.Errorf("pre_configuration.intervals must be %q or %q, got %q", IntervalsShort, IntervalsProduction, config.Intervals)
	}
}

// instanceTiming is what startMusterProcess needs to know about time for one
// instance; a restart runs the new process on the same values.
type instanceTiming struct {
	// intervals is IntervalsShort or IntervalsProduction.
	intervals string
	// clockSocket is the Unix socket the process serves its clock control on.
	clockSocket string
}

// clockSocketPath names the clock control socket of the instance on port.
// It lives in the harness's temporary directory under a short name: a Unix
// socket path is limited to about a hundred bytes, which an instance's
// configuration directory (named after the scenario) can exceed.
func clockSocketPath(tempDir string, port int) string {
	return filepath.Join(tempDir, fmt.Sprintf("clock-%d.sock", port))
}

// timingEnv returns the environment that puts an instance's timers on the
// selected schedule and gives it a controllable clock.
func timingEnv(timing instanceTiming) []string {
	env := []string{
		clock.EnvControlSocket + "=" + timing.clockSocket,
		// Two resync ticks fit in a wait of a few seconds, so a scenario can
		// assert that a reconcile the reconciler must perform once -- the
		// stop of a suspended server -- stays done across them (production:
		// 30s). The resync is controller-runtime's; the clock does not reach
		// it, so it is shortened on both schedules.
		"MUSTER_RECONCILER_RESYNC_INTERVAL=2s",
	}
	if timing.intervals == IntervalsProduction {
		return env
	}
	return append(env,
		// Under parallel load a first connect to a mock endpoint can fail
		// transiently; with production timing that costs 30s initial backoff
		// plus up to a 30s orchestrator tick before the retry, blowing past
		// scenario wait_for_state budgets. Fast-retry inside the harness so
		// transient failures recover in seconds.
		"MUSTER_MCPSERVER_INITIAL_BACKOFF=1s",
		// A cap three times the initial backoff: the third and fourth failure
		// both wait 3s (instead of 4s and 8s), so a scenario sees the cap bite
		// within seconds and can assert on the exact schedule.
		"MUSTER_MCPSERVER_MAX_BACKOFF=3s",
		"MUSTER_ORCHESTRATOR_RETRY_INTERVAL=1s",
		// Three failed probes turn a server unhealthy, so a scenario sees the
		// health loop act on a dead backend within seconds. The probe's pings
		// pass the mock servers' outage gate uncounted (mock/outage.go), so a
		// scenario that arms N failed requests still sees N failed attempts.
		"MUSTER_ORCHESTRATOR_HEALTH_CHECK_INTERVAL=1s",
		// A 5 s delayed mock tool call ages the core catalogue past this, so
		// a scenario sees the read after it refresh the catalogue in the
		// background within seconds (production: 5 min).
		"MUSTER_CORE_CATALOGUE_MAX_AGE=3s",
	)
}
