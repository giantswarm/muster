package reconciler

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/pkg/logging"
)

// Metric attribute keys for the MCPServer state instruments.
const (
	// attrMCPServerName and attrMCPServerNamespace identify a single MCPServer.
	//
	// Deliberately not the bare "namespace": with a Prometheus Operator
	// ServiceMonitor, target metadata labels (namespace, pod, service, ...)
	// win over metric-borne labels of the same name unless honorLabels is set,
	// and the metric's own value is renamed to "exported_namespace". Prefixing
	// keeps the label the alert expressions select on stable regardless of how
	// the endpoint is scraped.
	attrMCPServerName      = "mcpserver.name"
	attrMCPServerNamespace = "mcpserver.namespace"

	// attrMCPServerState carries MCPServer.status.state verbatim, spaces
	// included ("Auth Required"), so a label value can be compared against
	// `kubectl get mcpserver` output without translation.
	attrMCPServerState = "state"
)

// stateKey identifies one MCPServer.
type stateKey struct {
	name      string
	namespace string
}

// mcpServerStateMetrics publishes the reconciler's view of
// MCPServer.status.state as OpenTelemetry metrics:
//
//   - muster.mcpserver.state (observable gauge) — 1 for the current state of
//     every MCPServer the reconciler has reconciled, with attributes
//     mcpserver.name, mcpserver.namespace and state. Exported through the
//     Prometheus exporter this becomes
//     muster_mcpserver_state{mcpserver_name, mcpserver_namespace, state}.
//   - muster.mcpserver.state_transitions (counter) — one increment per state
//     change, same attributes, where state is the state being entered.
//     Becomes muster_mcpserver_state_transitions_total.
//
// The gauge is asynchronous on purpose. Only the attribute sets observed during
// a collection cycle are exported (the SDK aggregates observable gauges with
// PrecomputedLastValue, which clears its values on every collection), so a
// deleted MCPServer stops reporting as soon as forget() drops it instead of
// pinning an alert on a series that no longer has a resource behind it. A
// synchronous gauge or a per-state boolean would keep the last written value
// forever, which is exactly the stale-alert failure mode to avoid.
//
// A single gauge carrying the state as an attribute is used rather than one
// boolean gauge per state: at any collection each MCPServer contributes exactly
// one series, so the previous state's series disappears on its own and the
// alert expression stays a plain equality match on the state label.
//
// The two instruments answer different questions. The gauge shows sustained
// breakage (what is broken right now, and for how long), the counter shows
// churn — a server that fails and recovers every few minutes never satisfies a
// `for:` window on the gauge but is obvious in the transition rate.
type mcpServerStateMetrics struct {
	// mu guards states. It is taken by both the reconcile path (record,
	// forget) and the metric collection callback (observe).
	mu     sync.Mutex
	states map[stateKey]musterv1alpha1.MCPServerStateValue

	// gauge and transitions are nil when instrument creation failed. Metrics
	// are best effort: muster must reconcile MCPServers whether or not it can
	// report on them.
	gauge        metric.Int64ObservableGauge
	transitions  metric.Int64Counter
	registration metric.Registration
}

// newMCPServerStateMetrics creates the MCPServer state instruments on meter and
// registers the gauge callback.
//
// The meter is a parameter rather than resolved from otel.Meter here so tests
// can collect from a reader of their own without swapping the global
// MeterProvider — the global one delegates instruments created before a
// provider is installed, which would pull every other reconciler in the
// process into the test's reader.
//
// Instrument or callback registration failures are logged and degrade to a
// no-op recorder rather than failing reconciler construction. When no metric
// exporter is configured the global provider is a no-op and every call here
// succeeds cheaply.
func newMCPServerStateMetrics(meter metric.Meter) *mcpServerStateMetrics {
	m := &mcpServerStateMetrics{
		states: make(map[stateKey]musterv1alpha1.MCPServerStateValue),
	}

	transitions, err := meter.Int64Counter("muster.mcpserver.state_transitions",
		metric.WithDescription("Number of MCPServer state transitions observed by the reconciler, labelled with the state entered."),
		metric.WithUnit("{transition}"),
	)
	if err != nil {
		logging.Warn("MCPServerReconciler", "create muster.mcpserver.state_transitions counter: %v", err)
	} else {
		m.transitions = transitions
	}

	gauge, err := meter.Int64ObservableGauge("muster.mcpserver.state",
		metric.WithDescription("Current state of each MCPServer known to the reconciler: 1 for the state the MCPServer is in, no series for the states it is not in."),
	)
	if err != nil {
		logging.Warn("MCPServerReconciler", "create muster.mcpserver.state gauge: %v", err)
		return m
	}
	m.gauge = gauge

	registration, err := meter.RegisterCallback(m.observe, gauge)
	if err != nil {
		logging.Warn("MCPServerReconciler", "register muster.mcpserver.state callback: %v", err)
		m.gauge = nil
		return m
	}
	m.registration = registration

	return m
}

// observe reports the current state of every known MCPServer. Called by the
// SDK on each metric collection.
func (m *mcpServerStateMetrics) observe(_ context.Context, observer metric.Observer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, state := range m.states {
		observer.ObserveInt64(m.gauge, 1, metric.WithAttributes(
			attribute.String(attrMCPServerName, key.name),
			attribute.String(attrMCPServerNamespace, key.namespace),
			attribute.String(attrMCPServerState, string(state)),
		))
	}
	return nil
}

// record stores the state the reconciler observed for one MCPServer and counts
// a transition when it differs from the previous observation.
//
// Safe to call repeatedly with an unchanged state — the periodic status resync
// does exactly that, and the retry-on-conflict loop in syncStatus can re-derive
// the same state several times for one reconcile pass. Only changes are counted.
//
// The first observation of an MCPServer counts as a transition into its initial
// state, so a server that comes up broken is visible in the counter and not
// only in the gauge. One increment per server per muster restart is well below
// any sensible flapping threshold.
func (m *mcpServerStateMetrics) record(ctx context.Context, key stateKey, state musterv1alpha1.MCPServerStateValue) {
	m.mu.Lock()
	previous, known := m.states[key]
	m.states[key] = state
	m.mu.Unlock()

	if known && previous == state {
		return
	}
	if m.transitions == nil {
		return
	}
	m.transitions.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrMCPServerName, key.name),
		attribute.String(attrMCPServerNamespace, key.namespace),
		attribute.String(attrMCPServerState, string(state)),
	))
}

// forget drops an MCPServer from the gauge, so a deleted resource stops
// reporting from the next collection onwards.
//
// The transition counter is intentionally left alone: it is cumulative, and
// deleting its series would make a recreate look like it had never failed.
func (m *mcpServerStateMetrics) forget(key stateKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, key)
}

// close unregisters the gauge callback. Idempotent.
func (m *mcpServerStateMetrics) close() error {
	m.mu.Lock()
	registration := m.registration
	m.registration = nil
	m.mu.Unlock()

	if registration == nil {
		return nil
	}
	return registration.Unregister()
}
