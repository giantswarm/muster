package aggregator

import (
	mcpotel "github.com/mark3labs/mcp-go/otel"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel"

	"github.com/giantswarm/muster/pkg/observability"
)

// mcpServerOptions returns the OTEL option chain wired into the aggregator's
// mcp-go server. WithServerTracing installs the server-wide tracer and W3C
// propagator that emit mcp.<method> spans on every JSON-RPC dispatch and a
// tool.<name> span around each tool handler. The Logging and Metrics
// middlewares execute inside that tool-handler span, so log records pick up
// trace_id / span_id via the slog ↔ OTel bridge and histogram observations
// attach the active TraceID as an exemplar — the join Grafana uses to pivot
// from a latency bucket to the originating trace.
func mcpServerOptions() []server.ServerOption {
	return []server.ServerOption{
		mcpotel.WithServerTracing(otel.Tracer(observability.TracerName)),
		server.WithToolHandlerMiddleware(Logging()),
		server.WithToolHandlerMiddleware(Metrics()),
	}
}
