package mcpserver

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpotel "github.com/mark3labs/mcp-go/otel"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/giantswarm/muster/internal/aggregator/instrument"
)

// TestClientTracing_PostConstruction pins the wiring used in
// client_stdio.go / client_sse.go / client_streamable_http.go /
// client_dynamic_auth.go: build a *client.Client via one of mcp-go's
// transport-specific constructors (none of which accept ClientOption),
// then apply mcpotel.WithClientTracing on the returned client. The
// assertion catches an upstream ClientOption signature change (the
// option call would stop compiling, or compile silently with the
// wrong semantics) and any global tracer-provider plumbing regression
// that prevents otel.Tracer(instrument.TracerName) from reaching the
// recording SDK.
func TestClientTracing_PostConstruction(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	srv := server.NewMCPServer("trace-srv", "1.0")
	srv.AddTool(mcp.Tool{Name: "echo"}, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	c := client.NewClient(transport.NewInProcessTransport(srv))
	mcpotel.WithClientTracing(otel.Tracer(instrument.TracerName))(c)

	require.NoError(t, c.Start(t.Context()))
	t.Cleanup(func() { _ = c.Close() })

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1"}
	_, err := c.Initialize(t.Context(), initReq)
	require.NoError(t, err)

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "echo"
	_, err = c.CallTool(t.Context(), callReq)
	require.NoError(t, err)

	var sawClientSpan bool
	for _, sp := range rec.Ended() {
		if sp.SpanKind() == trace.SpanKindClient && sp.Name() == "mcp.tools/call" {
			sawClientSpan = true
		}
	}
	require.True(t, sawClientSpan,
		"expected an mcp.tools/call span of kind Client emitted by mcp-go/otel — "+
			"verify mcpotel.WithClientTracing still applies as a post-construction ClientOption")
}
