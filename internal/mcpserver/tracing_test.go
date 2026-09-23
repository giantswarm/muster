package mcpserver

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

// startTracedClient installs a recording TracerProvider and returns an
// initialized client to an in-process "echo" server, traced with
// withClientTracing as the backend clients are: built by a
// transport-specific constructor (none accepts ClientOption), then
// configured. Initialization runs in initCtx.
func startTracedClient(t *testing.T, initCtx context.Context) (*client.Client, *tracetest.SpanRecorder) {
	t.Helper()
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
	withClientTracing(c)

	require.NoError(t, c.Start(initCtx))
	t.Cleanup(func() { _ = c.Close() })

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1"}
	_, err := c.Initialize(initCtx, initReq)
	require.NoError(t, err)
	return c, rec
}

func callEcho(t *testing.T, ctx context.Context, c *client.Client) {
	t.Helper()
	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "echo"
	_, err := c.CallTool(ctx, callReq)
	require.NoError(t, err)
}

func clientSpan(t *testing.T, rec *tracetest.SpanRecorder, name string) map[string]string {
	t.Helper()
	for _, sp := range rec.Ended() {
		if sp.SpanKind() == trace.SpanKindClient && sp.Name() == name {
			attrs := make(map[string]string)
			for _, kv := range sp.Attributes() {
				attrs[string(kv.Key)] = kv.Value.AsString()
			}
			return attrs
		}
	}
	require.Failf(t, "client span not found", "no ended %s span of kind Client", name)
	return nil
}

// TestClientTracing_PostConstruction catches an upstream ClientOption
// signature change and any global tracer-provider regression that keeps
// otel.Tracer(observability.TracerName) from reaching the recording SDK.
func TestClientTracing_PostConstruction(t *testing.T) {
	c, rec := startTracedClient(t, t.Context())
	callEcho(t, t.Context(), c)

	attrs := clientSpan(t, rec, toolsCallClientSpan)
	require.Equal(t, "tools/call", attrs["mcp.method"])
	require.NotContains(t, attrs, observability.AttrGenAIToolName)
	require.NotContains(t, attrs, observability.AttrMCPServerName)
}

func TestClientTracing_DownstreamCallAttributes(t *testing.T) {
	call := observability.DownstreamCall{Server: "kubernetes", Tool: "echo"}
	c, rec := startTracedClient(t, observability.ContextWithDownstreamCall(t.Context(), call))
	callEcho(t, observability.ContextWithDownstreamCall(t.Context(), call), c)

	attrs := clientSpan(t, rec, toolsCallClientSpan)
	require.Equal(t, "echo", attrs[observability.AttrGenAIToolName])
	require.Equal(t, "kubernetes", attrs[observability.AttrMCPServerName])

	var others int
	for _, sp := range rec.Ended() {
		if sp.SpanKind() != trace.SpanKindClient || sp.Name() == toolsCallClientSpan {
			continue
		}
		others++
		attrs := make(map[string]string)
		for _, kv := range sp.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
		require.Equal(t, "kubernetes", attrs[observability.AttrMCPServerName], sp.Name())
		require.NotContains(t, attrs, observability.AttrGenAIToolName,
			"%s does not relate to a tool", sp.Name())
	}
	require.Positive(t, others, "the handshake opens client spans of its own")
}
