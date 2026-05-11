package instrument

import (
	"context"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// MeterName is the instrumentation scope for metrics emitted from this
// package. Matches TracerName so dashboards can join span and metric
// by service.name + scope.
const MeterName = TracerName

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
	m := otel.Meter(MeterName)
	calls, err := m.Int64Counter("muster.tool_calls",
		metric.WithDescription("Number of MCP tool calls handled by the muster aggregator."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		logging.Warn("Aggregator", "create muster.tool_calls counter: %v", err)
	}
	duration, err := m.Float64Histogram("muster.tool_call.duration",
		metric.WithDescription("Duration of MCP tool calls handled by the muster aggregator."),
		metric.WithUnit("s"),
	)
	if err != nil {
		logging.Warn("Aggregator", "create muster.tool_call.duration histogram: %v", err)
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
