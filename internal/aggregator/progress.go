package aggregator

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/v5/internal/mcpserver"
)

// ProgressRelay returns a ToolHandlerMiddleware that runs every tool call
// with a progress reporter on its context, so every downstream tool the call
// reaches gets a progressToken muster minted, and the call stays alive on
// that tool's notifications/progress (see internal/mcpserver/progress.go). A
// call whose _meta carries a progressToken has the progress relayed to the
// calling session under that token; a call without one gets the minted
// token downstream all the same, its progress kept inside muster: a
// downstream tool that blocks until a verdict and reports progress meanwhile
// works for a client that never asks for progress.
func ProgressRelay() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return next(mcpserver.WithProgressReporter(ctx, progressReporterFor(ctx, req)), req)
		}
	}
}

// progressReporterFor returns the reporter for one tool call: one relaying
// to the calling session under the caller's token, or one keeping the
// progress inside muster when the caller sent no token or no session can
// receive it.
func progressReporterFor(ctx context.Context, req mcp.CallToolRequest) *mcpserver.ProgressReporter {
	if req.Params.Meta == nil || req.Params.Meta.ProgressToken == nil {
		return mcpserver.NewProgressReporter(nil, nil)
	}
	srv := server.ServerFromContext(ctx)
	if srv == nil {
		return mcpserver.NewProgressReporter(nil, nil)
	}
	return mcpserver.NewProgressReporter(req.Params.Meta.ProgressToken, func(params map[string]any) error {
		return srv.SendNotificationToClient(ctx, string(mcp.MethodNotificationProgress), params)
	})
}
