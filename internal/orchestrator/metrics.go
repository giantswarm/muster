package orchestrator

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/giantswarm/muster/internal/services"
	"github.com/giantswarm/muster/pkg/logging"
	"github.com/giantswarm/muster/pkg/observability"
)

// serviceMetrics holds OTel instruments for service lifecycle observability.
type serviceMetrics struct {
	transitions metric.Int64Counter
	up          metric.Int64ObservableGauge
}

// newServiceMetrics initialises the OTel instruments against the global
// MeterProvider. Failures are non-fatal: each instrument falls back to a
// no-op so the orchestrator continues operating without metrics.
func newServiceMetrics(registry services.ServiceRegistry) *serviceMetrics {
	m := otel.Meter(observability.TracerName)

	transitions, err := m.Int64Counter(
		"muster.service.state_transitions_total",
		metric.WithDescription("Total number of MCP service state transitions."),
		metric.WithUnit("{transition}"),
	)
	if err != nil {
		logging.Warn("Orchestrator", "create muster.service.state_transitions_total counter: %v", err)
	}

	up, err := m.Int64ObservableGauge(
		"muster.service.up",
		metric.WithDescription("1 if the MCP service is in a running/connected state, 0 otherwise."),
		metric.WithUnit("{service}"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			for _, svc := range registry.GetAll() {
				state := svc.GetState()
				var value int64
				if state == services.StateRunning || state == services.StateConnected {
					value = 1
				}
				o.Observe(value,
					metric.WithAttributes(
						attribute.String("service_name", svc.GetName()),
						attribute.String("service_type", string(svc.GetType())),
					),
				)
			}
			return nil
		}),
	)
	if err != nil {
		logging.Warn("Orchestrator", "create muster.service.up gauge: %v", err)
	}

	return &serviceMetrics{
		transitions: transitions,
		up:          up,
	}
}

// recordTransition emits one observation on the transitions counter.
func (m *serviceMetrics) recordTransition(ctx context.Context, serviceName, serviceType, fromState, toState string) {
	if m.transitions == nil {
		return
	}
	m.transitions.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("service_name", serviceName),
			attribute.String("service_type", serviceType),
			attribute.String("from_state", fromState),
			attribute.String("to_state", toState),
		),
	)
}
