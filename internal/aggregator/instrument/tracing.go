package instrument

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TracerName is the OpenTelemetry instrumentation scope used for every span
// emitted by muster — server-side mcp-go spans, the inner StartToolSpan, and
// the outbound mcp-go client spans on the muster → backend leg. Direction is
// captured by SpanKind (Server vs Client) and span name (tool.<name> only
// exists on the inbound leg), so a single scope keeps dashboard filtering
// readable.
const TracerName = "github.com/giantswarm/muster"

// AttrToolName is the OTEL attribute key carrying the MCP tool name.
const AttrToolName = "mcp.tool.name"

// StartToolSpan opens a "tool.<name>" span (SpanKindInternal) and returns
// the new context plus an End function that finalizes the span based on
// the handler outcome. Used by CallToolInternal to capture the backend-
// resolved tool name for calls that enter through the internal dispatch
// path (workflows, direct API) rather than mcp-go's tool handler chain.
func StartToolSpan(ctx context.Context, name string) (context.Context, func(*mcp.CallToolResult, error)) {
	tracer := otel.Tracer(TracerName)
	ctx, span := tracer.Start(ctx, "tool."+name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String(AttrToolName, name)),
	)
	end := func(res *mcp.CallToolResult, err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else if res != nil && res.IsError {
			span.SetStatus(codes.Error, "tool result IsError")
		}
		span.End()
	}
	return ctx, end
}
