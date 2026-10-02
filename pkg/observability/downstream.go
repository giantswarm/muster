package observability

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// AttrMCPServerName is the attribute key for the MCPServer a tool call was
// dispatched to. The same key labels the muster.downstream_tool_calls metric
// (exported as mcpserver_name) and the reconciler's muster_mcpserver_state
// metrics, so traces and metrics join on one value.
const AttrMCPServerName = "mcpserver.name"

// AttrDownstreamToolName is the attribute key for the aggregator-exposed name
// of the tool a call was dispatched to (x_<server>_<tool> or the family name),
// the same value as the tool label of muster.downstream_tool_calls. It sits on
// muster's own server and internal spans, where mcp.tool.name keeps naming
// the tool the client invoked (call_tool for meta-tool sessions).
const AttrDownstreamToolName = "muster.downstream.tool.name"

// AttrGenAIToolName is the OpenTelemetry GenAI semantic-convention key for
// the tool an MCP operation relates to. It is set on the client span to the
// backend, carrying the backend-native tool name sent on the wire.
const AttrGenAIToolName = "gen_ai.tool.name"

// DownstreamCall identifies the backend leg of a tool call.
type DownstreamCall struct {
	// Server is the MCPServer name.
	Server string
	// Tool is the backend-native tool name.
	Tool string
}

type downstreamCallKey struct{}

// ContextWithDownstreamCall returns ctx carrying call, for the client tracer
// to attribute the spans it opens toward the backend.
func ContextWithDownstreamCall(ctx context.Context, call DownstreamCall) context.Context {
	return context.WithValue(ctx, downstreamCallKey{}, call)
}

// DownstreamCallFromContext returns the DownstreamCall ctx carries, if any.
func DownstreamCallFromContext(ctx context.Context) (DownstreamCall, bool) {
	call, ok := ctx.Value(downstreamCallKey{}).(DownstreamCall)
	return call, ok
}

type requestSpanKey struct{}

// ContextWithRequestSpan returns ctx remembering span as the span of the
// inbound MCP request, so code below the tool-handler span can still
// attribute the request span.
func ContextWithRequestSpan(ctx context.Context, span trace.Span) context.Context {
	return context.WithValue(ctx, requestSpanKey{}, span)
}

// WithoutRequestSpan returns ctx with no request span. A caller that makes
// several dispatches under one request (a workflow's steps) uses it so no
// single dispatch labels the whole request.
func WithoutRequestSpan(ctx context.Context) context.Context {
	if ctx.Value(requestSpanKey{}) == nil {
		return ctx
	}
	return context.WithValue(ctx, requestSpanKey{}, nil)
}

// AnnotateDownstreamCall labels the current span and, when it differs, the
// request span with the MCPServer and the exposed tool name of a dispatch,
// and returns ctx carrying the DownstreamCall for the client span.
func AnnotateDownstreamCall(ctx context.Context, serverName, exposedTool, backendTool string) context.Context {
	attrs := []attribute.KeyValue{
		attribute.String(AttrMCPServerName, serverName),
		attribute.String(AttrDownstreamToolName, exposedTool),
	}
	current := trace.SpanFromContext(ctx)
	current.SetAttributes(attrs...)
	if request, ok := ctx.Value(requestSpanKey{}).(trace.Span); ok && request.SpanContext().SpanID() != current.SpanContext().SpanID() {
		request.SetAttributes(attrs...)
	}
	call := DownstreamCall{Server: serverName, Tool: backendTool}
	if record, ok := ctx.Value(dispatchRecordKey{}).(*DispatchRecord); ok {
		record.record(exposedTool, call)
	}
	return ContextWithDownstreamCall(ctx, call)
}

// DispatchRecord learns where one exposed tool's call was dispatched: the
// MCPServer and the backend-native tool name the aggregator resolved it to.
type DispatchRecord struct {
	exposedTool string

	mu       sync.Mutex
	call     DownstreamCall
	recorded bool
}

type dispatchRecordKey struct{}

// ContextWithDispatchRecord returns ctx carrying a DispatchRecord that the
// dispatch of exposedTool under ctx fills in. Only that tool's first dispatch
// is recorded: the dispatches a workflow's steps make under the same context
// are for other tools and leave it alone.
func ContextWithDispatchRecord(ctx context.Context, exposedTool string) (context.Context, *DispatchRecord) {
	record := &DispatchRecord{exposedTool: exposedTool}
	return context.WithValue(ctx, dispatchRecordKey{}, record), record
}

func (r *DispatchRecord) record(exposedTool string, call DownstreamCall) {
	if exposedTool != r.exposedTool {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recorded {
		r.call, r.recorded = call, true
	}
}

// Dispatched returns the backend leg the tool was dispatched to, and false
// when it was not dispatched to a backend (a core or workflow tool, or a call
// that failed before resolution).
func (r *DispatchRecord) Dispatched() (DownstreamCall, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.call, r.recorded
}
