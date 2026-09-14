package observability_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/giantswarm/muster/pkg/observability"
)

// collectBounds records one observation on a histogram with the given unit
// through a MeterProvider carrying SecondsHistogramView, and returns the
// bucket boundaries the SDK ended up aggregating with.
func collectBounds(t *testing.T, unit string) []float64 {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithView(observability.SecondsHistogramView()),
	)
	t.Cleanup(func() { require.NoError(t, mp.Shutdown(t.Context())) })

	h, err := mp.Meter("test").Float64Histogram("test.duration", metric.WithUnit(unit))
	require.NoError(t, err)
	h.Record(t.Context(), 1)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	require.Len(t, rm.ScopeMetrics, 1)
	require.Len(t, rm.ScopeMetrics[0].Metrics, 1)

	hist, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, hist.DataPoints, 1)
	return hist.DataPoints[0].Bounds
}

// The boundaries are a published contract: docs/explanation/observability.md
// documents them and dashboard queries read the resulting le values.
func TestSecondsHistogramBoundaries(t *testing.T) {
	require.Equal(t, []float64{
		0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300,
	}, observability.SecondsHistogramBoundaries)
}

func TestSecondsHistogramViewAppliesToSecondsUnit(t *testing.T) {
	require.Equal(t, observability.SecondsHistogramBoundaries, collectBounds(t, "s"))
}

func TestSecondsHistogramViewLeavesOtherUnitsAlone(t *testing.T) {
	require.NotEqual(t, observability.SecondsHistogramBoundaries, collectBounds(t, "ms"))
}

func TestSecondsHistogramViewBoundariesAreNotAliased(t *testing.T) {
	view := observability.SecondsHistogramView()

	observability.SecondsHistogramBoundaries[0] = -1
	t.Cleanup(func() { observability.SecondsHistogramBoundaries[0] = 0.005 })

	stream, ok := view(sdkmetric.Instrument{
		Name: "test.duration",
		Kind: sdkmetric.InstrumentKindHistogram,
		Unit: "s",
	})
	require.True(t, ok)

	agg, ok := stream.Aggregation.(sdkmetric.AggregationExplicitBucketHistogram)
	require.True(t, ok)
	require.Equal(t, 0.005, agg.Boundaries[0])
}
