package teleport

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metric label values for muster_transport_lookup_total.
const (
	resultResolved       = "resolved"
	resultSecretMissing  = "secret_missing"
	resultClusterUnknown = "cluster_unknown"
	resultNone           = "none"

	// Metric label values for muster_transport_secret_load_total.
	resultLoadOK    = "ok"
	resultLoadError = "error"
)

// transportLookupTotal counts dispatcher.ClientsFor calls partitioned by
// outcome. PLAN §6 TB-7. result="none" covers the spec.transport-unset path
// so we can audit how many CRs run through the direct-HTTPS bypass.
var transportLookupTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "muster_transport_lookup_total",
		Help: "Number of CR-driven transport-dispatcher lookups, by outcome.",
	},
	[]string{"result"},
)

// transportSecretLoadTotal counts tbot-identity Secret load attempts
// partitioned by secret name + outcome. PLAN §6 TB-7. Drives TB-12's
// MusterTransportSecretMissing alert.
var transportSecretLoadTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "muster_transport_secret_load_total",
		Help: "Number of tbot-identity Secret load attempts, by secret name and outcome.",
	},
	[]string{"secret", "result"},
)

// incLookup is the dispatcher's hook into transportLookupTotal. Tiny wrapper
// keeps callers free of label-value typos.
func incLookup(result string) {
	transportLookupTotal.WithLabelValues(result).Inc()
}

// incSecretLoad is the dispatcher's hook into transportSecretLoadTotal.
func incSecretLoad(secret, result string) {
	transportSecretLoadTotal.WithLabelValues(secret, result).Inc()
}

// resetMetricsForTest clears both metric vectors. Tests use this to assert on
// counter state without fighting cross-test pollution; promauto registers
// against the global registry so each test would otherwise observe whatever
// the previous test recorded.
func resetMetricsForTest() {
	transportLookupTotal.Reset()
	transportSecretLoadTotal.Reset()
}
