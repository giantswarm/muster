package aggregator

import (
	"context"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/pkg/logging"
)

// LogSubsystem tags the structured log lines emitted by this middleware,
// matching muster's pkg/logging subsystem convention.
const LogSubsystem = "MCP-Tool"

// LogMessage is the constant msg field on emitted lines. Log queries
// in Loki use this as the anchor (e.g. `msg = "tool call"`).
const LogMessage = "tool call"

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
