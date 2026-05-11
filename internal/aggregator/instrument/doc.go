// Package instrument wraps MCP tool handlers with observability concerns:
// tracing, metrics, and structured logging.
//
// The three middlewares are server.ToolHandlerMiddleware values and plug
// directly into mark3labs/mcp-go via WithToolHandlerMiddleware. They are
// applied in this order at the composition root:
//
//	mcpserver.NewMCPServer(name, version,
//	    mcpserver.WithToolHandlerMiddleware(instrument.Logging()),         // outermost
//	    mcpserver.WithToolHandlerMiddleware(instrument.Metrics()),
//	    mcpserver.WithToolHandlerMiddleware(instrument.Tracing()),         // innermost wrapper
//	    mcpserver.WithToolHandlerMiddleware(responsecap.New(...)),
//	    mcpserver.WithToolHandlerMiddleware(timeout.New(0)),
//	)
//
// mcp-go applies middleware in reverse registration order, so the chain
// becomes Logging(Metrics(Tracing(responsecap(timeout(handler))))) — the
// outermost wrappers observe the final post-cap, post-timeout outcome
// the client sees.
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
//
// # Meter initialization
//
// InitMeter installs the global OTEL MeterProvider, mirroring
// mcp-toolkit/tracing.Init for traces. The toolkit v0.1.0 does not ship
// a meter helper; once it does, InitMeter is replaced and this file
// shrinks.
package instrument
