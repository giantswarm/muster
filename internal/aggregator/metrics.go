package aggregator

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/giantswarm/muster/v5/pkg/logging"
	"github.com/giantswarm/muster/v5/pkg/observability"
)

const (
	// outcomeOK marks a tool call that returned (res, nil) with res
	// either nil or IsError=false.
	outcomeOK = "ok"
	// outcomeError marks a tool call where the handler returned a
	// non-nil Go error.
	outcomeError = "error"
	// outcomeErrorResult marks a tool call that returned (res, nil)
	// with res.IsError == true — a typed protocol-level error.
	outcomeErrorResult = "error_result"
)

// Metrics returns a ToolHandlerMiddleware that records:
//
//   - muster.tool_calls (counter) with attributes tool, outcome
//   - muster.tool_call.duration (histogram, unit "s") with the same
//     attributes
//
// Exported via the Prometheus OTEL exporter these become
// muster_tool_calls_total and muster_tool_call_duration_seconds.
func Metrics() server.ToolHandlerMiddleware {
	m := otel.Meter(observability.TracerName)
	calls, err := m.Int64Counter("muster.tool_calls",
		metric.WithDescription("Number of MCP tool calls handled by the muster aggregator."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		logging.Warn("Aggregator", "create muster.tool_calls counter: %v", err)
		return passthroughMiddleware()
	}
	duration, err := m.Float64Histogram("muster.tool_call.duration",
		metric.WithDescription("Duration of MCP tool calls handled by the muster aggregator."),
		metric.WithUnit("s"),
	)
	if err != nil {
		logging.Warn("Aggregator", "create muster.tool_call.duration histogram: %v", err)
		return passthroughMiddleware()
	}
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			res, err := next(ctx, req)
			outcome := classify(res, err)
			attrs := metric.WithAttributes(
				attribute.String("tool", req.Params.Name),
				attribute.String("outcome", outcome),
			)
			calls.Add(ctx, 1, attrs)
			duration.Record(ctx, time.Since(start).Seconds(), attrs)
			return res, err
		}
	}
}

// Session store backends, the values of the backend attribute of
// muster.session_store.backend.
const (
	sessionStoreBackendValkey = "valkey"
	sessionStoreBackendMemory = "memory"
)

// recordSessionStoreBackend publishes the backend the session auth and
// capability stores run on. Exported via the Prometheus OTEL exporter this is
// muster_session_store_backend{backend="valkey|memory"}: 1 for the backend in
// use, 0 for the other, so an alert can pin a muster whose stores are not
// where its configuration says.
func recordSessionStoreBackend(ctx context.Context, backend string) {
	gauge, err := otel.Meter(observability.TracerName).Int64Gauge("muster.session_store.backend",
		metric.WithDescription("Backend of the session auth and capability stores: 1 for the backend in use, 0 for the other."),
	)
	if err != nil {
		logging.Warn("Aggregator", "create muster.session_store.backend gauge: %v", err)
		return
	}
	for _, candidate := range []string{sessionStoreBackendValkey, sessionStoreBackendMemory} {
		var inUse int64
		if candidate == backend {
			inUse = 1
		}
		gauge.Record(ctx, inUse, metric.WithAttributes(attribute.String("backend", candidate)))
	}
}

func passthroughMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc { return next }
}

// attrDownstreamServer is the attribute key for the backend server a tool
// call was dispatched to. Exported as mcpserver_name, matching the
// reconciler's muster_mcpserver_state metrics so the two join on the same
// label (and sidestepping the Prometheus-Operator target-label rename a
// bare "namespace"-style key would suffer, see mcpserver_state_metrics.go).
const attrDownstreamServer = "mcpserver.name"

// downstreamMetrics records the downstream leg of a tool call: the dispatch
// to a backend MCP server once (server, tool) resolution has happened.
//
// The Metrics middleware above sits at the aggregator boundary and sees what
// clients invoke — which for agent sessions is almost exclusively the
// call_tool/filter_tools meta-tools, hiding the real tool behind
// tool="call_tool". This instrument attributes each dispatched call to the
// resolved backend server and the aggregator-exposed tool name, which is the
// dimension per-server / per-tool usage views need.
//
// Exported via the Prometheus OTEL exporter these become
// muster_downstream_tool_calls_total and
// muster_downstream_tool_call_duration_seconds.
type downstreamMetrics struct {
	calls    metric.Int64Counter
	duration metric.Float64Histogram
}

// newDownstreamMetrics creates the downstream instruments. It returns nil
// when instrument creation fails; record is nil-safe, so the failure mode is
// a silent no-op rather than a guard at every call site.
func newDownstreamMetrics() *downstreamMetrics {
	m := otel.Meter(observability.TracerName)
	calls, err := m.Int64Counter("muster.downstream_tool_calls",
		metric.WithDescription("Number of tool calls the muster aggregator dispatched to downstream MCP servers."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		logging.Warn("Aggregator", "create muster.downstream_tool_calls counter: %v", err)
		return nil
	}
	duration, err := m.Float64Histogram("muster.downstream_tool_call.duration",
		metric.WithDescription("Duration of tool calls the muster aggregator dispatched to downstream MCP servers."),
		metric.WithUnit("s"),
	)
	if err != nil {
		logging.Warn("Aggregator", "create muster.downstream_tool_call.duration histogram: %v", err)
		return nil
	}
	return &downstreamMetrics{calls: calls, duration: duration}
}

// record counts one dispatched call. toolName is the aggregator-exposed name
// (x_<server>_<tool> or the family name), not the backend-native one, so the
// label matches what list_tools shows users.
func (d *downstreamMetrics) record(ctx context.Context, serverName, toolName string, start time.Time, res *mcp.CallToolResult, err error) {
	if d == nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String(attrDownstreamServer, serverName),
		attribute.String("tool", toolName),
		attribute.String("outcome", classify(res, err)),
	)
	d.calls.Add(ctx, 1, attrs)
	d.duration.Record(ctx, time.Since(start).Seconds(), attrs)
}

// classify maps a (result, error) pair to the outcome label used as a
// metric attribute and a log field.
func classify(res *mcp.CallToolResult, err error) string {
	switch {
	case err != nil:
		return outcomeError
	case res != nil && res.IsError:
		return outcomeErrorResult
	default:
		return outcomeOK
	}
}
