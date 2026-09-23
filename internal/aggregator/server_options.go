package aggregator

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	mcpotel "github.com/mark3labs/mcp-go/otel"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/giantswarm/muster/v5/pkg/observability"
)

// mcpServerOptions returns the OTEL option chain wired into the aggregator's
// mcp-go server. WithServerTracing installs the server-wide tracer and W3C
// propagator that emit mcp.<method> spans on every JSON-RPC dispatch and a
// tool.<name> span around each tool handler. The Logging and Metrics
// middlewares execute inside that tool-handler span, so log records pick up
// trace_id / span_id via the slog ↔ OTel bridge and histogram observations
// attach the active TraceID as an exemplar — the join Grafana uses to pivot
// from a latency bucket to the originating trace.
//
// RequestSpan is registered first so it wraps the tracing middleware and sees
// the mcp.tools/call server span, which dispatch then labels with the backend
// the call reached.
func mcpServerOptions() []server.ServerOption {
	return []server.ServerOption{
		server.WithToolHandlerMiddleware(RequestSpan()),
		mcpotel.WithServerTracing(otel.Tracer(observability.TracerName)),
		server.WithToolHandlerMiddleware(Logging()),
		server.WithToolHandlerMiddleware(Metrics()),
	}
}

// RequestSpan returns a ToolHandlerMiddleware that remembers the span active
// when the tool call enters the handler chain as the request span.
func RequestSpan() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return next(observability.ContextWithRequestSpan(ctx, trace.SpanFromContext(ctx)), req)
		}
	}
}

// mcpServerCapabilityOptions returns the capability advertisement for the
// aggregator's mcp-go server.
//
// resources/subscribe is off: mcp-go acknowledges a subscribe request whenever
// the capability is advertised, but the aggregator neither relays the
// subscription to the downstream server that owns the resource nor emits
// notifications/resources/updated, so a subscribing client waits forever.
// listChanged is on and backed by the notification subscriber.
func mcpServerCapabilityOptions() []server.ServerOption {
	return []server.ServerOption{
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, true),
		server.WithPromptCapabilities(true),
	}
}
