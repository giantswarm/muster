package instrument

import (
	"context"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const (
	testToolName       = "x_kubernetes_list_pods"
	durationMetricName = "muster.tool_call.duration"
)

// TestMCPServerOptions_HistogramExemplarAttachesToolHandlerSpan exercises the
// production option chain end-to-end: build an MCPServer with MCPServerOptions,
// register a tool handler, dispatch a tools/call through mcp-go's HandleMessage,
// and assert the duration histogram observation carries an exemplar with the
// tool-handler span's TraceID. The assertion fails if mcpotel.WithServerTracing
// is dropped from MCPServerOptions (no tool.<name> span is opened, so no span
// is active during histogram.Record and the SDK's TraceBased exemplar filter
// has no TraceID to attach).
func TestMCPServerOptions_HistogramExemplarAttachesToolHandlerSpan(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prevTP) })

	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	prevMP := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(prevMP) })

	srv := server.NewMCPServer("muster-aggregator-test", "test", MCPServerOptions()...)
	srv.AddTool(mcp.Tool{Name: testToolName}, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	initBody := fmt.Appendf(nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"clientInfo":{"name":"t","version":"1"},"capabilities":{}}}`,
		mcp.LATEST_PROTOCOL_VERSION,
	)
	require.NotNil(t, srv.HandleMessage(t.Context(), initBody))

	callBody := fmt.Appendf(nil,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":%q,"arguments":{}}}`,
		testToolName,
	)
	require.NotNil(t, srv.HandleMessage(t.Context(), callBody))

	var toolHandlerTraceID string
	for _, sp := range rec.Ended() {
		if sp.Name() == "tool."+testToolName {
			toolHandlerTraceID = sp.SpanContext().TraceID().String()
		}
	}
	require.NotEmpty(t, toolHandlerTraceID,
		"expected mcp-go to emit a tool.<name> span; if it did not, mcpotel.WithServerTracing is not wired")

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	var sawExemplar bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != durationMetricName {
				continue
			}
			hist := m.Data.(metricdata.Histogram[float64])
			for _, dp := range hist.DataPoints {
				for _, ex := range dp.Exemplars {
					if hexTraceID(ex.TraceID) == toolHandlerTraceID {
						sawExemplar = true
					}
				}
			}
		}
	}
	require.True(t, sawExemplar,
		"expected histogram exemplar carrying the tool-handler span TraceID — "+
			"MCPServerOptions must register mcpotel.WithServerTracing so the span is live during histogram.Record")
}

func hexTraceID(b []byte) string {
	const hexchars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, x := range b {
		out[i*2] = hexchars[x>>4]
		out[i*2+1] = hexchars[x&0x0f]
	}
	return string(out)
}
