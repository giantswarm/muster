// Package instrument wraps MCP tool handlers with observability concerns:
// tracing, metrics, and structured logging.
//
// The three middlewares are server.ToolHandlerMiddleware values and plug
// directly into mark3labs/mcp-go via WithToolHandlerMiddleware. They are
// applied in this order at the composition root:
//
//	mcpserver.NewMCPServer(name, version,
//	    mcpserver.WithToolHandlerMiddleware(instrument.Tracing()),  // outermost
//	    mcpserver.WithToolHandlerMiddleware(instrument.Logging()),
//	    mcpserver.WithToolHandlerMiddleware(instrument.Metrics()),
//	)
//
// mcp-go applies middleware in reverse registration order, so the chain
// becomes Tracing(Logging(Metrics(handler))). Tracing is outermost so
// the span is active while Logging emits its line and Metrics records
// its observation — log records carry trace_id / span_id via the slog ↔
// OTel bridge, and histogram exemplars attach the local trace_id.
//
// # Aggregator meta-tool layer vs real tool layer
//
// Muster's aggregator exposes only meta-tools (call_tool, list_tools, …)
// to MCP clients; the actual workload tools (x_kubernetes_*, workflow_*,
// service_*) are dispatched inside call_tool via CallToolInternal. The
// middlewares above wrap the meta-tool layer; CallToolInternal opens a
// second, inner span with the real tool name so Tempo shows
// tool.call_tool → tool.x_kubernetes_list_pods → outbound.
//
// # Lifecycle
//
// Tracing is transitional: when mark3labs/mcp-go gains a native
// WithTracing option, tracing.go is deleted and the corresponding line
// in the composition root is removed. Metrics and Logging are permanent.
package instrument
