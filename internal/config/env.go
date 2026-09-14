package config

import (
	"os"
	"time"
)

// DurationFromEnv reads a Go duration from the named environment variable,
// falling back to def when the variable is unset, unparsable or not positive.
//
// muster's lifecycle timings -- the reconnect backoff and its cap, the
// orchestrator's retry and health-probe ticks, the reconciler's resync -- are
// production-length defaults with one environment override each, so the
// integration test harness can run them in seconds instead of minutes. The
// knobs share this reader so they agree on what a malformed value means: the
// default, never a zero that would spin a ticker.
func DurationFromEnv(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}
