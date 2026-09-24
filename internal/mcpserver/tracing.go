package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/client"
	mcpotel "github.com/mark3labs/mcp-go/otel"
	"github.com/mark3labs/mcp-go/tracing"
	"go.opentelemetry.io/otel"

	"github.com/giantswarm/muster/v5/pkg/observability"
)

// toolsCallClientSpan is the name mcp-go gives the client span of a
// tools/call request ("mcp." + method).
const toolsCallClientSpan = "mcp.tools/call"

// withClientTracing installs the OpenTelemetry tracer and W3C propagator on a
// backend client, as mcpotel.WithClientTracing does, with the tracer wrapped
// by downstreamTracer.
func withClientTracing(c *client.Client) {
	client.WithTracer(downstreamTracer{next: mcpotel.NewTracer(otel.Tracer(observability.TracerName))})(c)
	client.WithPropagator(mcpotel.NewPropagator())(c)
}

// downstreamTracer adds the observability.DownstreamCall the context carries
// to the client spans mcp-go opens: mcpserver.name on every one, and
// gen_ai.tool.name on the tools/call span, so the backend leg of a call made
// through call_tool names the backend and its tool.
type downstreamTracer struct {
	next tracing.Tracer
}

func (t downstreamTracer) Start(ctx context.Context, name string, kind tracing.SpanKind, attrs ...tracing.Attribute) (context.Context, tracing.Span) {
	if call, ok := observability.DownstreamCallFromContext(ctx); ok && kind == tracing.SpanKindClient {
		attrs = append(attrs, tracing.String(observability.AttrMCPServerName, call.Server))
		if name == toolsCallClientSpan {
			attrs = append(attrs, tracing.String(observability.AttrGenAIToolName, call.Tool))
		}
	}
	return t.next.Start(ctx, name, kind, attrs...)
}
