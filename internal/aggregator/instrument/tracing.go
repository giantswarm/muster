package instrument

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TracerName is the instrumentation scope written to spans emitted from
// this package and from the inner span in CallToolInternal.
const TracerName = "github.com/giantswarm/muster/internal/aggregator"

// AttrToolName is the OTEL attribute key carrying the MCP tool name.
const AttrToolName = "mcp.tool.name"

// Tracing returns a ToolHandlerMiddleware that opens a "tool.<name>"
// span (SpanKindInternal) around each tool call. Span status is set to
// codes.Error when the handler returns a non-nil error or a
// *mcp.CallToolResult with IsError == true.
func Tracing() server.ToolHandlerMiddleware {
	tracer := otel.Tracer(TracerName)
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name := req.Params.Name
			ctx, span := tracer.Start(ctx, "tool."+name,
				trace.WithSpanKind(trace.SpanKindInternal),
				trace.WithAttributes(attribute.String(AttrToolName, name)),
			)
			defer span.End()

			res, err := next(ctx, req)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return res, err
			}
			if res != nil && res.IsError {
				span.SetStatus(codes.Error, "tool result IsError")
			}
			return res, nil
		}
	}
}

// StartToolSpan opens a "tool.<name>" span (SpanKindInternal) and
// returns the new context plus an End function that finalizes the span
// based on the handler outcome. Used by CallToolInternal to capture the
// real tool name in addition to the meta-tool span opened by the
// Tracing middleware on the mcp-go layer.
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
