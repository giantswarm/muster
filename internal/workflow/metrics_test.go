package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/observability"
)

// These tests define the contract for workflow execution metrics (Phase 4 of
// #930): a counter and a duration histogram keyed by {workflow,status}, plus a
// store-error counter so empty dashboards become an observable signal. They are
// written test-first against an in-memory OTel reader and expect
// newWorkflowMetrics / recordExecution / recordStoreError to be added.

func setupWorkflowMeter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	return r
}

func TestWorkflowMetricsRecordExecution(t *testing.T) {
	reader := setupWorkflowMeter(t)
	m := newWorkflowMetrics()

	m.recordExecution(context.Background(), "alpha", api.WorkflowExecutionCompleted, 1500*time.Millisecond)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	var sawCounter, sawHistogram bool
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			switch mm.Name {
			case "muster.workflow_executions":
				sawCounter = true
				sum, ok := mm.Data.(metricdata.Sum[int64])
				require.True(t, ok, "workflow_executions should be a Sum[int64]")
				require.Len(t, sum.DataPoints, 1)
				dp := sum.DataPoints[0]
				require.Equal(t, int64(1), dp.Value)
				wf, _ := dp.Attributes.Value("workflow")
				status, _ := dp.Attributes.Value("status")
				require.Equal(t, "alpha", wf.AsString())
				require.Equal(t, string(api.WorkflowExecutionCompleted), status.AsString())
			case "muster.workflow_execution.duration":
				sawHistogram = true
				hist, ok := mm.Data.(metricdata.Histogram[float64])
				require.True(t, ok, "workflow_execution.duration should be a Histogram[float64]")
				require.Len(t, hist.DataPoints, 1)
				require.Equal(t, uint64(1), hist.DataPoints[0].Count)
			}
		}
	}
	require.True(t, sawCounter, "expected muster.workflow_executions counter")
	require.True(t, sawHistogram, "expected muster.workflow_execution.duration histogram")
}

func TestWorkflowMetricsRecordStoreError(t *testing.T) {
	reader := setupWorkflowMeter(t)
	m := newWorkflowMetrics()

	m.recordStoreError(context.Background(), "alpha")

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	var saw bool
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			if mm.Name == "muster.workflow_execution.store_errors" {
				saw = true
				sum, ok := mm.Data.(metricdata.Sum[int64])
				require.True(t, ok, "store_errors should be a Sum[int64]")
				require.Len(t, sum.DataPoints, 1)
				require.Equal(t, int64(1), sum.DataPoints[0].Value)
			}
		}
	}
	require.True(t, saw, "expected muster.workflow_execution.store_errors counter")
}

// The execution duration histogram carries unit "s", the shape
// observability.SecondsHistogramView matches on, and therefore aggregates with
// SecondsHistogramBoundaries rather than the SDK's millisecond defaults.
func TestWorkflowDurationHistogramUsesSecondsBuckets(t *testing.T) {
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(r),
		sdkmetric.WithView(observability.SecondsHistogramView()),
	)
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	newWorkflowMetrics().recordExecution(t.Context(), "alpha", api.WorkflowExecutionCompleted, 90*time.Second)

	var rm metricdata.ResourceMetrics
	require.NoError(t, r.Collect(t.Context(), &rm))

	var seen bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "muster.workflow_execution.duration" {
				continue
			}
			seen = true
			hist, ok := m.Data.(metricdata.Histogram[float64])
			require.True(t, ok)
			require.Len(t, hist.DataPoints, 1)
			require.Equal(t, observability.SecondsHistogramBoundaries(), hist.DataPoints[0].Bounds)
		}
	}
	require.True(t, seen, "expected muster.workflow_execution.duration histogram")
}
