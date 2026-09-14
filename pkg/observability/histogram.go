package observability

import (
	"slices"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// SecondsHistogramBoundaries are the explicit bucket boundaries for every
// muster histogram recorded in seconds. The SDK default boundaries top out
// at 10000 and are spaced for milliseconds, which collapses second-scale
// observations into the first bucket.
//
// The range spans local meta-tool calls (single-digit milliseconds), remote
// backend calls dispatched through call_tool (seconds), and workflow
// executions that legitimately run for minutes.
var SecondsHistogramBoundaries = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300,
}

// SecondsHistogramView returns the sdkmetric.View that applies
// SecondsHistogramBoundaries to every histogram instrument with unit "s".
// Matching on unit rather than instrument name covers any seconds-unit
// histogram without enumerating them.
func SecondsHistogramView() sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{
			Kind: sdkmetric.InstrumentKindHistogram,
			Unit: "s",
		},
		sdkmetric.Stream{
			Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: slices.Clone(SecondsHistogramBoundaries),
			},
		},
	)
}
