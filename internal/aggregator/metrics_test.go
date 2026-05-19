package aggregator

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func setupMeter(t *testing.T) *metric.ManualReader {
	t.Helper()
	r := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(r))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	return r
}

func TestMetrics(t *testing.T) {
	cases := []struct {
		name        string
		handler     server.ToolHandlerFunc
		toolName    string
		wantOutcome string
	}{
		{
			name: "ok outcome",
			handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			},
			toolName:    "x_kubernetes_list_pods",
			wantOutcome: outcomeOK,
		},
		{
			name: "error outcome",
			handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, errors.New("boom")
			},
			toolName:    "workflow_run",
			wantOutcome: outcomeError,
		},
		{
			name: "error_result outcome",
			handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{IsError: true}, nil
			},
			toolName:    "x_prom_query",
			wantOutcome: outcomeErrorResult,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := setupMeter(t)
			wrapped := Metrics()(tc.handler)
			_, _ = wrapped(context.Background(), callRequest(tc.toolName))

			var rm metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(context.Background(), &rm))

			var sawCounter, sawHistogram bool
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					switch m.Name {
					case "muster.tool_calls":
						sawCounter = true
						sum, ok := m.Data.(metricdata.Sum[int64])
						require.True(t, ok, "tool_calls should be a Sum")
						require.Len(t, sum.DataPoints, 1)
						dp := sum.DataPoints[0]
						require.Equal(t, int64(1), dp.Value)
						tool, _ := dp.Attributes.Value("tool")
						outcome, _ := dp.Attributes.Value("outcome")
						require.Equal(t, tc.toolName, tool.AsString())
						require.Equal(t, tc.wantOutcome, outcome.AsString())
					case "muster.tool_call.duration":
						sawHistogram = true
						hist, ok := m.Data.(metricdata.Histogram[float64])
						require.True(t, ok, "tool_call.duration should be a Histogram[float64]")
						require.Len(t, hist.DataPoints, 1)
						dp := hist.DataPoints[0]
						require.Equal(t, uint64(1), dp.Count)
						outcome, _ := dp.Attributes.Value("outcome")
						require.Equal(t, tc.wantOutcome, outcome.AsString())
					}
				}
			}
			require.True(t, sawCounter, "expected muster.tool_calls counter")
			require.True(t, sawHistogram, "expected muster.tool_call.duration histogram")
		})
	}
}

func TestClassify(t *testing.T) {
	require.Equal(t, outcomeOK, classify(&mcp.CallToolResult{}, nil))
	require.Equal(t, outcomeOK, classify(nil, nil))
	require.Equal(t, outcomeError, classify(nil, errors.New("x")))
	require.Equal(t, outcomeError, classify(&mcp.CallToolResult{IsError: true}, errors.New("x")))
	require.Equal(t, outcomeErrorResult, classify(&mcp.CallToolResult{IsError: true}, nil))
}

