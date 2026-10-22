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
// the call reached. ProgressRelay sends every downstream tool call a
// progressToken and bridges its notifications/progress to a caller that sent
// one.
func mcpServerOptions() []server.ServerOption {
	return []server.ServerOption{
		server.WithToolHandlerMiddleware(RequestSpan()),
		mcpotel.WithServerTracing(otel.Tracer(observability.TracerName)),
		server.WithToolHandlerMiddleware(Logging()),
		server.WithToolHandlerMiddleware(Metrics()),
		server.WithToolHandlerMiddleware(ProgressRelay()),
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
// resources.subscribe is on for subscriptions/listen (MCP 2026-07-28), which
// the aggregator relays per user to the backend serving each URI
// (resource_subscriptions.go); a legacy initialize does not advertise it
// (hideLegacySubscribe). listChanged is on and backed by the notification
// subscriber.
func mcpServerCapabilityOptions() []server.ServerOption {
	return []server.ServerOption{
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithPromptCapabilities(true),
	}
}
