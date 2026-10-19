package aggregator

import (
	"context"
	"encoding/json"
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
// production option chain end-to-end: build an MCPServer with mcpServerOptions,
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

	srv := server.NewMCPServer("muster-aggregator-test", "test", mcpServerOptions()...)
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
			"mcpServerOptions must register mcpotel.WithServerTracing so the span is live during histogram.Record")
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

// TestMCPServerCapabilityOptions_ResourceSubscribeForSubscriptionsListenOnly
// asserts resources.subscribe is advertised to a 2026-07-28 client, whose
// subscriptions/listen the aggregator relays, and hidden from a legacy
// initialize (hideLegacySubscribe), whose resources/subscribe mcp-go would
// acknowledge with no update ever following. listChanged stays on in both: it
// is backed by notification_subscriber.
func TestMCPServerCapabilityOptions_ResourceSubscribeForSubscriptionsListenOnly(t *testing.T) {
	hooks := &server.Hooks{}
	hooks.AddAfterInitialize(func(_ context.Context, _ any, _ *mcp.InitializeRequest, result *mcp.InitializeResult) {
		hideLegacySubscribe(result)
	})
	srv := server.NewMCPServer("muster-aggregator-test", "test", append(mcpServerCapabilityOptions(), server.WithHooks(hooks))...)

	initBody := fmt.Appendf(nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"clientInfo":{"name":"t","version":"1"},"capabilities":{}}}`,
		mcp.LATEST_LEGACY_PROTOCOL_VERSION,
	)
	raw, err := json.Marshal(srv.HandleMessage(t.Context(), initBody))
	require.NoError(t, err)

	var resp struct {
		Result mcp.InitializeResult `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))

	require.NotNil(t, resp.Result.Capabilities.Resources,
		"resources capability must still be advertised")
	require.False(t, resp.Result.Capabilities.Resources.Subscribe,
		"a legacy initialize must not advertise resources.subscribe: only subscriptions/listen is relayed")
	require.True(t, resp.Result.Capabilities.Resources.ListChanged,
		"resources.listChanged is wired via notification_subscriber and must stay on")
	require.NotNil(t, resp.Result.Capabilities.Tools)
	require.NotNil(t, resp.Result.Capabilities.Prompts)

	discoverBody := fmt.Appendf(nil,
		`{"jsonrpc":"2.0","id":2,"method":"server/discover","params":{"_meta":{%q:%q,%q:{"name":"t","version":"1"},%q:{}}}}`,
		mcp.MetaKeyProtocolVersion, mcp.ProtocolVersion20260728, mcp.MetaKeyClientInfo, mcp.MetaKeyClientCapabilities,
	)
	raw, err = json.Marshal(srv.HandleMessage(t.Context(), discoverBody))
	require.NoError(t, err)
	var discovered struct {
		Result struct {
			Capabilities mcp.ServerCapabilities `json:"capabilities"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &discovered), string(raw))
	require.NotNil(t, discovered.Result.Capabilities.Resources, string(raw))
	require.True(t, discovered.Result.Capabilities.Resources.Subscribe,
		"a 2026-07-28 client is told resource subscriptions are served")
}
