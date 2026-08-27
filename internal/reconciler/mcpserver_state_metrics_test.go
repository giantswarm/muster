package reconciler

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
)

// metricHarness drives the reconciler against a real SDK MeterProvider with a
// ManualReader, so assertions run against the datapoints an exporter would see
// rather than against the reconciler's internal map.
type metricHarness struct {
	t          *testing.T
	reader     *metric.ManualReader
	reconciler *MCPServerReconciler
	manager    *MockMCPServerManager
	registry   *MockServiceRegistry
}

// newMetricHarness builds a reconciler whose instruments live on a private
// MeterProvider.
//
// The global provider is deliberately left alone. otel's global handle
// delegates instruments and callbacks created before a provider is installed,
// so calling otel.SetMeterProvider here would retro-register every reconciler
// built by every other test in this package onto this reader.
func newMetricHarness(t *testing.T) *metricHarness {
	t.Helper()

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	manager := NewMockMCPServerManager()
	registry := NewMockServiceRegistry()
	reconciler := NewMCPServerReconciler(NewMockOrchestratorAPI(), manager, registry)

	// Swap the constructor's global-meter instruments for ones on the private
	// provider, releasing the global callback registration first.
	if err := reconciler.Close(); err != nil {
		t.Fatalf("release global metric registration: %v", err)
	}
	reconciler.stateMetrics = newMCPServerStateMetrics(provider.Meter("test"))
	t.Cleanup(func() { _ = reconciler.Close() })

	return &metricHarness{
		t:          t,
		reader:     reader,
		reconciler: reconciler,
		manager:    manager,
		registry:   registry,
	}
}

// addServer registers an MCPServer definition. A nil state means the service
// was never created, modelling a server that has never connected.
func (h *metricHarness) addServer(name, serverType string, state *api.ServiceState) {
	h.t.Helper()
	h.manager.AddMCPServer(&api.MCPServerInfo{Name: name, Type: serverType})
	if state != nil {
		h.registry.AddService(name, &MockServiceInfo{
			Name:        name,
			ServiceType: api.TypeMCPServer,
			State:       *state,
		})
	}
}

func (h *metricHarness) reconcile(name string) {
	h.t.Helper()
	h.reconciler.Reconcile(context.Background(), ReconcileRequest{
		Type:      ResourceTypeMCPServer,
		Name:      name,
		Namespace: "muster-test",
	})
}

// states collects the muster.mcpserver.state gauge as name -> state label. A
// server absent from the result is not reporting at all.
func (h *metricHarness) states() map[string]string {
	h.t.Helper()
	return h.collect("muster.mcpserver.state")
}

// transitionCounts collects muster.mcpserver.state_transitions as
// "<name>/<state>" -> count.
func (h *metricHarness) transitionCounts() map[string]int64 {
	h.t.Helper()

	var collected metricdata.ResourceMetrics
	if err := h.reader.Collect(context.Background(), &collected); err != nil {
		h.t.Fatalf("collect metrics: %v", err)
	}

	counts := make(map[string]int64)
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "muster.mcpserver.state_transitions" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				h.t.Fatalf("muster.mcpserver.state_transitions is %T, want Sum[int64]", m.Data)
			}
			for _, point := range sum.DataPoints {
				name, _ := point.Attributes.Value(attrMCPServerName)
				state, _ := point.Attributes.Value(attrMCPServerState)
				counts[name.String()+"/"+state.String()] += point.Value
			}
		}
	}
	return counts
}

// collect returns name -> state for the named gauge, failing on a duplicate
// series for one server: exactly one state must be reported per MCPServer.
func (h *metricHarness) collect(instrument string) map[string]string {
	h.t.Helper()

	var collected metricdata.ResourceMetrics
	if err := h.reader.Collect(context.Background(), &collected); err != nil {
		h.t.Fatalf("collect metrics: %v", err)
	}

	states := make(map[string]string)
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != instrument {
				continue
			}
			gauge, ok := m.Data.(metricdata.Gauge[int64])
			if !ok {
				h.t.Fatalf("%s is %T, want Gauge[int64]", instrument, m.Data)
			}
			for _, point := range gauge.DataPoints {
				name, _ := point.Attributes.Value(attrMCPServerName)
				state, _ := point.Attributes.Value(attrMCPServerState)
				namespace, _ := point.Attributes.Value(attrMCPServerNamespace)

				if namespace.String() != "muster-test" {
					h.t.Errorf("%s: %s has namespace %q, want muster-test", instrument, name.String(), namespace.String())
				}
				if point.Value != 1 {
					h.t.Errorf("%s: %s has value %d, want 1", instrument, name.String(), point.Value)
				}
				if existing, dup := states[name.String()]; dup {
					h.t.Errorf("%s: %s reported twice (%q and %q); exactly one state per MCPServer expected",
						instrument, name.String(), existing, state.String())
				}
				states[name.String()] = state.String()
			}
		}
	}
	return states
}

func statePtr(state api.ServiceState) *api.ServiceState { return &state }

// TestStateGaugeReportsEveryKnownServer is the core of the detection gap this
// metric closes: a remote server whose endpoint is unreachable must be visible
// as Failed, alongside healthy servers and servers that never connected.
func TestStateGaugeReportsEveryKnownServer(t *testing.T) {
	h := newMetricHarness(t)

	h.addServer("healthy-remote", "streamable-http", statePtr(api.StateRunning))
	h.addServer("broken-remote", "streamable-http", statePtr(api.StateFailed))
	h.addServer("unreachable-remote", "streamable-http", statePtr(api.StateUnreachable))
	h.addServer("waiting-on-auth", "streamable-http", statePtr(api.StateAuthRequired))
	// No service entry: a definition the orchestrator has never started.
	h.addServer("never-connected", "streamable-http", nil)

	for _, name := range []string{"healthy-remote", "broken-remote", "unreachable-remote", "waiting-on-auth", "never-connected"} {
		h.reconcile(name)
	}

	want := map[string]string{
		"healthy-remote":     string(musterv1alpha1.MCPServerStateConnected),
		"broken-remote":      string(musterv1alpha1.MCPServerStateFailed),
		"unreachable-remote": string(musterv1alpha1.MCPServerStateFailed),
		"waiting-on-auth":    string(musterv1alpha1.MCPServerStateAuthRequired),
		"never-connected":    string(musterv1alpha1.MCPServerStateDisconnected),
	}
	got := h.states()
	if len(got) != len(want) {
		t.Fatalf("gauge reported %d servers (%v), want %d", len(got), got, len(want))
	}
	for name, state := range want {
		if got[name] != state {
			t.Errorf("%s reported as %q, want %q", name, got[name], state)
		}
	}
}

// TestStateGaugeMatchesCRDStatus pins the metric to the CRD field it is meant
// to mirror. If the two ever disagree, an alert can fire for a state nobody can
// see with kubectl, or stay silent for one they can.
func TestStateGaugeMatchesCRDStatus(t *testing.T) {
	h := newMetricHarness(t)

	updater := NewMockStatusUpdater()
	h.reconciler.WithStatusUpdater(updater, "muster-test")

	h.addServer("broken-remote", "streamable-http", statePtr(api.StateFailed))
	updater.AddMCPServer(&musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "broken-remote", Namespace: "muster-test"},
		Spec:       musterv1alpha1.MCPServerSpec{Type: "streamable-http"},
	})

	h.reconcile("broken-remote")

	written := updater.GetLastUpdatedMCPServer()
	if written == nil {
		t.Fatal("reconciler did not write MCPServer status")
	}
	if got := h.states()["broken-remote"]; got != string(written.Status.State) {
		t.Errorf("gauge reports %q but status.state is %q", got, written.Status.State)
	}
	if written.Status.State != musterv1alpha1.MCPServerStateFailed {
		t.Errorf("status.state is %q, want Failed", written.Status.State)
	}
}

// TestStateGaugeFollowsTransitions checks a server only ever reports its
// current state — the previous state's series must not linger, or a recovered
// server would keep a Failed alert firing.
func TestStateGaugeFollowsTransitions(t *testing.T) {
	h := newMetricHarness(t)

	h.addServer("flapper", "streamable-http", statePtr(api.StateFailed))
	h.reconcile("flapper")
	if got := h.states()["flapper"]; got != string(musterv1alpha1.MCPServerStateFailed) {
		t.Fatalf("after failure, gauge reports %q, want Failed", got)
	}

	h.registry.AddService("flapper", &MockServiceInfo{
		Name:        "flapper",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
	})
	h.reconcile("flapper")

	states := h.states()
	if got := states["flapper"]; got != string(musterv1alpha1.MCPServerStateConnected) {
		t.Errorf("after recovery, gauge reports %q, want Connected", got)
	}
	if len(states) != 1 {
		t.Errorf("gauge reports %d series (%v), want only the current state", len(states), states)
	}
}

// TestStateGaugeStopsReportingDeletedServer is the stale-series guarantee: a
// deleted MCPServer must drop out of the gauge, so deleting a broken server
// resolves its alert instead of pinning it forever.
func TestStateGaugeStopsReportingDeletedServer(t *testing.T) {
	h := newMetricHarness(t)

	h.addServer("doomed", "streamable-http", statePtr(api.StateFailed))
	h.addServer("survivor", "streamable-http", statePtr(api.StateRunning))
	h.reconcile("doomed")
	h.reconcile("survivor")

	if got := len(h.states()); got != 2 {
		t.Fatalf("gauge reports %d servers, want 2", got)
	}

	// Drop the definition, then reconcile: a missing definition is the delete
	// path, the same way the change detector drives it.
	h.manager.RemoveMCPServer("doomed")
	h.reconcile("doomed")

	states := h.states()
	if _, still := states["doomed"]; still {
		t.Errorf("deleted MCPServer still reports state %q", states["doomed"])
	}
	if got := states["survivor"]; got != string(musterv1alpha1.MCPServerStateConnected) {
		t.Errorf("survivor reports %q, want Connected", got)
	}
}

// TestStateTransitionsCounterCountsChangesOnly keeps the flapping signal
// meaningful: the periodic status resync reconciles unchanged servers
// repeatedly, and those passes must not inflate the counter.
func TestStateTransitionsCounterCountsChangesOnly(t *testing.T) {
	h := newMetricHarness(t)

	h.addServer("flapper", "streamable-http", statePtr(api.StateFailed))

	// Three reconciles with no state change: one transition into Failed.
	h.reconcile("flapper")
	h.reconcile("flapper")
	h.reconcile("flapper")

	if got := h.transitionCounts()["flapper/Failed"]; got != 1 {
		t.Errorf("Failed transitions after 3 unchanged reconciles: %d, want 1", got)
	}

	// Recover, then fail again: one more Failed transition, one Connected.
	h.registry.AddService("flapper", &MockServiceInfo{
		Name: "flapper", ServiceType: api.TypeMCPServer, State: api.StateRunning,
	})
	h.reconcile("flapper")
	h.registry.AddService("flapper", &MockServiceInfo{
		Name: "flapper", ServiceType: api.TypeMCPServer, State: api.StateFailed,
	})
	h.reconcile("flapper")

	counts := h.transitionCounts()
	if got := counts["flapper/Failed"]; got != 2 {
		t.Errorf("Failed transitions after a flap: %d, want 2", got)
	}
	if got := counts["flapper/Connected"]; got != 1 {
		t.Errorf("Connected transitions after a flap: %d, want 1", got)
	}
}

// TestStateMetricsRecordedWithoutStatusUpdater covers filesystem mode, where
// syncStatus returns early because there is no CRD to write to. The metric is
// the only external view of state there, so it must still be published.
func TestStateMetricsRecordedWithoutStatusUpdater(t *testing.T) {
	h := newMetricHarness(t)

	if h.reconciler.StatusUpdater != nil {
		t.Fatal("harness reconciler unexpectedly has a StatusUpdater")
	}

	h.addServer("local-stdio", "stdio", statePtr(api.StateFailed))
	h.reconcile("local-stdio")

	if got := h.states()["local-stdio"]; got != string(musterv1alpha1.MCPServerStateFailed) {
		t.Errorf("gauge reports %q, want Failed", got)
	}
}
