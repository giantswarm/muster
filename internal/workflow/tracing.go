package workflow

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/giantswarm/muster/pkg/observability"
)

// startStepSpan opens a "workflow.step" span around a single tool invocation
// inside a workflow execution. The span sits between the workflow-level
// span the caller opens (if any) and the muster→backend client span the
// mcp-go transport opens, so a trace shows the workflow → step → backend
// hierarchy without having to attribute each span manually.
//
// kind: Internal. Attributes:
//
//   - workflow.name        — the parent workflow's name
//   - workflow.step.id     — the step ID from the definition
//   - mcp.tool.name        — the tool name being dispatched
//
// The returned end function records the final outcome on the span: any
// non-nil err sets StatusError; a tool result with IsError sets the same.
func startStepSpan(ctx context.Context, workflowName, stepID, toolName string) (context.Context, func(isError bool, err error)) {
	tracer := otel.Tracer(observability.TracerName)
	ctx, span := tracer.Start(ctx, "workflow.step",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("workflow.name", workflowName),
			attribute.String("workflow.step.id", stepID),
			attribute.String(observability.AttrToolName, toolName),
		),
	)
	end := func(isError bool, err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else if isError {
			span.SetStatus(codes.Error, "tool result IsError")
		}
		span.End()
	}
	return ctx, end
}
