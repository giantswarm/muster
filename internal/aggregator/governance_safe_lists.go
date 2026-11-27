package aggregator

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/pkg/logging"
	"github.com/giantswarm/muster/v5/pkg/observability"
)

// A safe-list entry of a governed server that names none of the tools the
// server lists is a typo or a renamed tool: the tool it meant asks agents
// (fail closed), but the list is wrong. muster checks each governed server's
// list against the tools it lists, logs every unmatched entry once per
// change, and publishes the count per server as the gauge
// muster.governance.safe_list_unmatched (muster_governance_safe_list_unmatched).

// safeListCheckInterval bounds how often a server's list is checked.
const safeListCheckInterval = time.Minute

// safeListReport is the last check per governed server.
type safeListReport struct {
	mu        sync.Mutex
	unmatched map[string][]string
	checkedAt map[string]time.Time
	gauge     sync.Once
}

// checkSafeLists checks the governed servers' safe lists against the tools
// they list now, each at most once per safeListCheckInterval. A server that
// lists no tools (not connected, signed out) is not judged.
func (a *AggregatorServer) checkSafeLists(ctx context.Context) {
	g := api.GetGovernance()
	if len(g.Servers) == 0 {
		return
	}
	r := &a.safeLists
	r.gauge.Do(func() { a.registerSafeListGauge() })
	now := clock.Now()
	for _, server := range g.Servers {
		if len(server.Safe) == 0 {
			continue
		}
		r.mu.Lock()
		due := now.Sub(r.checkedAt[server.Server]) >= safeListCheckInterval
		r.mu.Unlock()
		if !due {
			continue
		}
		tools, ok := a.ServerTools(ctx, server.Server)
		if !ok {
			continue
		}
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, tool.Name)
		}
		unmatched := g.UnmatchedSafe(server.Server, names)
		r.mu.Lock()
		if r.unmatched == nil {
			r.unmatched, r.checkedAt = map[string][]string{}, map[string]time.Time{}
		}
		changed := !slices.Equal(r.unmatched[server.Server], unmatched)
		r.unmatched[server.Server], r.checkedAt[server.Server] = unmatched, now
		r.mu.Unlock()
		if changed && len(unmatched) > 0 {
			logging.WarnWithAttrs("Governance", "safe-list entries name no tool of their governed server: the tools they meant ask agents until the list is fixed",
				slog.String("server", server.Server), slog.Any("unmatched", unmatched))
		}
	}
}

// UnmatchedSafeLists returns, per governed server checked so far, the
// safe-list entries that name none of its tools.
func (a *AggregatorServer) UnmatchedSafeLists() map[string][]string {
	r := &a.safeLists
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string][]string, len(r.unmatched))
	for server, unmatched := range r.unmatched {
		if len(unmatched) > 0 {
			out[server] = slices.Clone(unmatched)
		}
	}
	return out
}

func (a *AggregatorServer) registerSafeListGauge() {
	m := otel.Meter(observability.TracerName)
	_, err := m.Int64ObservableGauge("muster.governance.safe_list_unmatched",
		metric.WithDescription("Safe-list entries of a governed server that name none of its tools."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			report := a.UnmatchedSafeLists()
			for _, server := range slices.Sorted(maps.Keys(report)) {
				o.Observe(int64(len(report[server])), metric.WithAttributes(attribute.String("server", server)))
			}
			return nil
		}))
	if err != nil {
		logging.Warn("Governance", "create muster.governance.safe_list_unmatched gauge: %v", err)
	}
}
