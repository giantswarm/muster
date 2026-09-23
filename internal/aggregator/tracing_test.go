package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/giantswarm/muster/v5/pkg/observability"
)

func callRequest(name string) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name}}
}

// TestServerSpans_CallToolNamesDownstream runs a call_tool request through
// the aggregator's server option chain with a handler that dispatches the
// way dispatchResolvedTool does, and checks both muster spans name the
// backend while mcp.tool.name keeps naming the invoked meta-tool.
func TestServerSpans_CallToolNamesDownstream(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	var dispatched observability.DownstreamCall
	srv := server.NewMCPServer("muster", "1.0", mcpServerOptions()...)
	srv.AddTool(mcp.Tool{Name: "call_tool"}, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx = observability.AnnotateDownstreamCall(ctx, "kubernetes", "x_kubernetes_list_pods", "list_pods")
		dispatched, _ = observability.DownstreamCallFromContext(ctx)
		return mcp.NewToolResultText("ok"), nil
	})

	c := client.NewClient(transport.NewInProcessTransport(srv))
	require.NoError(t, c.Start(t.Context()))
	t.Cleanup(func() { _ = c.Close() })
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1"}
	_, err := c.Initialize(t.Context(), initReq)
	require.NoError(t, err)
	_, err = c.CallTool(t.Context(), callRequest("call_tool"))
	require.NoError(t, err)

	require.Equal(t, observability.DownstreamCall{Server: "kubernetes", Tool: "list_pods"}, dispatched)

	spans := map[string]map[string]string{}
	for _, sp := range rec.Ended() {
		if sp.Name() != "mcp.tools/call" && sp.Name() != "tool.call_tool" {
			continue
		}
		attrs := map[string]string{}
		for _, kv := range sp.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
		spans[sp.Name()] = attrs
		if sp.Name() == "mcp.tools/call" {
			require.Equal(t, trace.SpanKindServer, sp.SpanKind())
		}
	}
	require.Len(t, spans, 2)
	for name, attrs := range spans {
		require.Equal(t, "call_tool", attrs[observability.AttrToolName], name)
		require.Equal(t, "kubernetes", attrs[observability.AttrMCPServerName], name)
		require.Equal(t, "x_kubernetes_list_pods", attrs[observability.AttrDownstreamToolName], name)
	}
}
