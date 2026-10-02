package aggregator

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/v5/internal/mcpserver"
)

// ProgressRelay returns a ToolHandlerMiddleware that bridges downstream
// progress to a caller that asked for it: a tool call whose _meta carries a
// progressToken runs with a reporter on its context, and every downstream
// tool the call reaches relays its notifications/progress to the calling
// session under that token (see internal/mcpserver/progress.go). A call
// without a token runs unchanged.
func ProgressRelay() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if req.Params.Meta == nil || req.Params.Meta.ProgressToken == nil {
				return next(ctx, req)
			}
			srv := server.ServerFromContext(ctx)
			if srv == nil {
				return next(ctx, req)
			}
			reporter := mcpserver.NewProgressReporter(req.Params.Meta.ProgressToken, func(params map[string]any) error {
				return srv.SendNotificationToClient(ctx, string(mcp.MethodNotificationProgress), params)
			})
			return next(mcpserver.WithProgressReporter(ctx, reporter), req)
		}
	}
}
