package aggregator

import (
	"context"
	"maps"
	"reflect"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/v5/internal/api"
)

// ToolCallMetaRelay keeps application metadata on the current
// tool call's context, including calls made by its workflow steps.
func ToolCallMetaRelay() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var entries map[string]any
			if request.Params.Meta != nil {
				entries = request.Params.Meta.AdditionalFields
			}
			ctx, err := api.WithCallerMeta(ctx, entries)
			if err != nil {
				return nil, server.NewHandlerError(invalidCallerMetadata{err})
			}
			return next(ctx, request)
		}
	}
}

type invalidCallerMetadata struct{ error }

func (e invalidCallerMetadata) JSONRPCError() mcp.JSONRPCError {
	return mcp.JSONRPCError{Error: mcp.JSONRPCErrorDetails{Code: mcp.INVALID_PARAMS, Message: e.Error()}}
}

func (a *AggregatorServer) addExtensionHooks(hooks *server.Hooks) {
	hooks.AddAfterInitialize(func(ctx context.Context, _ any, request *mcp.InitializeRequest, result *mcp.InitializeResult) {
		a.advertiseExtensions(ctx, &result.Capabilities, request.Params.Capabilities.Extensions)
	})
	hooks.AddAfterDiscover(func(ctx context.Context, _ any, _ *mcp.DiscoverRequest, result *mcp.DiscoverResult) {
		a.advertiseExtensions(ctx, &result.Capabilities, callerCapabilities(ctx).Extensions)
	})
}

func (a *AggregatorServer) advertiseExtensions(ctx context.Context, capabilities *mcp.ServerCapabilities, clientExtensions map[string]any) {
	if len(api.RelayedExtensions(clientExtensions)) > 0 {
		a.awaitSessionBootstrap(ctx, getSessionIDFromContext(ctx))
	}
	merged := a.backendExtensions(ctx)
	if len(merged) == 0 {
		return
	}
	maps.Copy(merged, capabilities.Extensions)
	capabilities.Extensions = merged
}

// backendExtensions preserves identical settings and advertises an empty
// object for conflicting settings. Their meaning belongs to the extension.
func (a *AggregatorServer) backendExtensions(ctx context.Context) map[string]any {
	sessionID := getSessionIDFromContext(ctx)
	clients := make(map[string]MCPClient)
	for name, info := range a.registry.GetAllServers() {
		if info.Client != nil && !info.RequiresSessionAuth() {
			clients[name] = info.Client
		}
	}
	if a.connPool != nil && sessionID != "" {
		maps.Copy(clients, a.connPool.ClientsOfSession(sessionID))
	}
	view := api.CallerVisibility(ctx)
	var merged map[string]any
	for name, client := range clients {
		if !view.Sees(name) {
			continue
		}
		for identifier, settings := range api.RelayedExtensions(client.ServerCapabilities().Extensions) {
			if merged == nil {
				merged = make(map[string]any)
			}
			if previous, present := merged[identifier]; present && !reflect.DeepEqual(previous, settings) {
				settings = map[string]any{}
			}
			merged[identifier] = settings
		}
	}
	return merged
}
