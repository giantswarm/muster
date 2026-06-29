package workflow

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
	"github.com/giantswarm/muster/pkg/observability"
)

// workflowMetrics holds the OTel instruments for workflow execution tracking.
// Exported via the Prometheus OTEL exporter these become
// muster_workflow_executions_total, muster_workflow_execution_duration_seconds
// and muster_workflow_execution_store_errors_total.
type workflowMetrics struct {
	executions  metric.Int64Counter
	duration    metric.Float64Histogram
	storeErrors metric.Int64Counter
}

// newWorkflowMetrics creates the workflow execution metrics instruments.
// Instrument creation failures are logged and leave the affected instrument
// nil; recording on a nil instrument is a no-op, so tracking never fails.
func newWorkflowMetrics() *workflowMetrics {
	m := otel.Meter(observability.TracerName)

	executions, err := m.Int64Counter("muster.workflow_executions",
		metric.WithDescription("Number of workflow executions tracked by muster."),
		metric.WithUnit("{execution}"),
	)
	if err != nil {
		logging.Warn("ExecutionTracker", "create muster.workflow_executions counter: %v", err)
	}

	duration, err := m.Float64Histogram("muster.workflow_execution.duration",
		metric.WithDescription("Duration of workflow executions tracked by muster."),
		metric.WithUnit("s"),
	)
	if err != nil {
		logging.Warn("ExecutionTracker", "create muster.workflow_execution.duration histogram: %v", err)
	}

	storeErrors, err := m.Int64Counter("muster.workflow_execution.store_errors",
		metric.WithDescription("Number of workflow execution records that failed to persist."),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		logging.Warn("ExecutionTracker", "create muster.workflow_execution.store_errors counter: %v", err)
	}

	return &workflowMetrics{
		executions:  executions,
		duration:    duration,
		storeErrors: storeErrors,
	}
}

// recordExecution records a single finished workflow execution, keyed by the
// workflow name and its terminal status.
func (m *workflowMetrics) recordExecution(ctx context.Context, workflow string, status api.WorkflowExecutionStatus, d time.Duration) {
	attrs := metric.WithAttributes(
		attribute.String("workflow", workflow),
		attribute.String("status", string(status)),
	)
	if m.executions != nil {
		m.executions.Add(ctx, 1, attrs)
	}
	if m.duration != nil {
		m.duration.Record(ctx, d.Seconds(), attrs)
	}
}

// recordStoreError increments the store-error counter for the given workflow so
// that persistence failures (which otherwise yield empty dashboards) become an
// observable signal.
func (m *workflowMetrics) recordStoreError(ctx context.Context, workflow string) {
	if m.storeErrors != nil {
		m.storeErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("workflow", workflow)))
	}
}
