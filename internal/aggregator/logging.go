package aggregator

import (
	"context"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// LogSubsystem tags the structured log lines emitted by this middleware,
// matching muster's pkg/logging subsystem convention.
const LogSubsystem = "MCP-Tool"

// LogMessage is the constant msg field on emitted lines. Log queries
// in Loki use this as the anchor (e.g. `msg = "tool call"`).
const LogMessage = "tool call"

// DispatchLogMessage is the constant msg field of the line written once per
// tool call that ran: who called which tool on which MCPServer. For an agent
// session the boundary line's tool is always call_tool, so this is the line
// an audit or a proof counts.
const DispatchLogMessage = "tool dispatched"

// Logging returns a ToolHandlerMiddleware that emits one structured
// info-level log line per tool call with fields: tool, outcome,
// duration_s, error (when set).
func Logging() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			res, err := next(ctx, req)
			attrs := []slog.Attr{
				slog.String("tool", req.Params.Name),
				slog.String("outcome", classify(res, err)),
				slog.Float64("duration_s", time.Since(start).Seconds()),
			}
			if err != nil {
				attrs = append(attrs, slog.String("error", err.Error()))
			}
			logging.InfoWithAttrsCtx(ctx, LogSubsystem, LogMessage, attrs...)
			return res, err
		}
	}
}

// logDispatch writes the one info-level line per tool call that ran, from
// the same subsystem as the boundary line: subject (truncated, as every
// identity in the log), server (the MCPServer; absent for muster's own core
// and workflow tools, which reach no backend), tool (the aggregator-exposed
// name), outcome and duration_s, plus the transport session when the call
// came in over MCP. Arguments and results never appear, and neither does the
// error: the boundary line carries it, and a backend's error can quote what
// the call carried.
func logDispatch(ctx context.Context, serverName, toolName string, start time.Time, res *mcp.CallToolResult, err error) {
	attrs := []slog.Attr{
		slog.String("subject", logging.TruncateIdentifier(getUserSubjectFromContext(ctx))),
	}
	if serverName != "" {
		attrs = append(attrs, slog.String("server", serverName))
	}
	attrs = append(attrs,
		slog.String("tool", toolName),
		slog.String("outcome", classify(res, err)),
		slog.Float64("duration_s", time.Since(start).Seconds()),
	)
	if id := getTransportSessionID(ctx); id != "" {
		attrs = append(attrs, logging.TransportSessionID(id))
	}
	logging.InfoWithAttrsCtx(ctx, LogSubsystem, DispatchLogMessage, attrs...)
}

// finishDispatch closes the downstream leg of a tool call once it returned:
// the dispatch line and the downstream metrics, both attributed to the
// MCPServer the call reached and the exposed tool name.
func (a *AggregatorServer) finishDispatch(ctx context.Context, serverName, toolName string, start time.Time, res *mcp.CallToolResult, err error) {
	logDispatch(ctx, serverName, toolName, start, res, err)
	a.downstreamMetrics.record(ctx, serverName, toolName, start, res, err)
}
