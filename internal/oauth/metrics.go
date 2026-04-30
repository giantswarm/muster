package oauth

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metric label values for muster_token_exchange_total. PLAN §6 TB-8.
const (
	// resultSuccess is recorded when an exchange completed by talking to Dex
	// (cache miss followed by a successful response).
	resultSuccess = "success"
	// resultCacheHit is recorded when the exchange returned from the
	// in-process TokenExchangeCache without hitting the remote Dex.
	resultCacheHit = "cache_hit"
	// resultError is recorded when the exchange call returned an error,
	// including request validation failures, transport errors, and
	// post-exchange issuer-validation failures.
	resultError = "error"
)

// tokenExchangeTotal counts RFC 8693 token-exchange invocations partitioned by
// outcome. PLAN §6 TB-8. TB-12's MusterTokenExchangeFailures alert depends on
// the result="error" label.
var tokenExchangeTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "muster_token_exchange_total",
		Help: "Number of RFC 8693 token-exchange invocations, by outcome.",
	},
	[]string{"result"},
)

// tokenExchangeDuration tracks end-to-end latency of token-exchange calls,
// including cache lookups. PLAN §6 TB-8.
var tokenExchangeDuration = promauto.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "muster_token_exchange_duration_seconds",
		Help:    "End-to-end latency of RFC 8693 token-exchange invocations.",
		Buckets: prometheus.DefBuckets,
	},
)

// incTokenExchange is the wrapper for the counter so callers don't fight
// label-value typos. Result must be one of resultSuccess / resultCacheHit /
// resultError.
func incTokenExchange(result string) {
	tokenExchangeTotal.WithLabelValues(result).Inc()
}

// observeTokenExchangeDuration records the latency of a token-exchange call.
func observeTokenExchangeDuration(seconds float64) {
	tokenExchangeDuration.Observe(seconds)
}

// resetTokenExchangeMetricsForTest clears both metric vectors. Tests use this
// to assert counter state without cross-test pollution from the global
// registry.
func resetTokenExchangeMetricsForTest() {
	tokenExchangeTotal.Reset()
	// Histograms have no Reset(); recreate is unnecessary for the assertions
	// we make (we only check the counter). The histogram is left as-is.
}
