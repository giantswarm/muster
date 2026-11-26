package aggregator

import (
	"context"
	"maps"
	"reflect"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/v5/internal/api"
)

// ExtensionMetaRelay keeps negotiated extension metadata on the current
// tool call's context, including calls made by its workflow steps.
func ExtensionMetaRelay() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var entries map[string]any
			if request.Params.Meta != nil {
				entries = request.Params.Meta.AdditionalFields
			}
			return next(api.WithCallerMeta(ctx, entries, callerCapabilities(ctx).Extensions), request)
		}
	}
}

func (a *AggregatorServer) addExtensionHooks(hooks *server.Hooks) {
	hooks.AddAfterInitialize(func(ctx context.Context, _ any, _ *mcp.InitializeRequest, result *mcp.InitializeResult) {
		a.advertiseExtensions(ctx, &result.Capabilities)
	})
	hooks.AddAfterDiscover(func(ctx context.Context, _ any, _ *mcp.DiscoverRequest, result *mcp.DiscoverResult) {
		a.advertiseExtensions(ctx, &result.Capabilities)
	})
}

func (a *AggregatorServer) advertiseExtensions(ctx context.Context, capabilities *mcp.ServerCapabilities) {
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
	a.awaitSessionBootstrap(ctx, sessionID)
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
