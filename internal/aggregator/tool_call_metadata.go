package aggregator

import (
	"context"
	"maps"
	"slices"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/giantswarm/muster/v5/internal/api"
	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
)

// ToolCallMetadataRelay returns a ToolHandlerMiddleware that keeps the
// tool-call metadata values of the caller's tool call on its context. Every
// backend call the tool call makes, directly or through a workflow's steps,
// sends that backend the values of the keys it accepts
// (withToolCallMetadataFor). No other caller `_meta` entry is passed on.
func ToolCallMetadataRelay() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if req.Params.Meta != nil {
				if values := api.ToolCallMetadataValues(req.Params.Meta.AdditionalFields); len(values) > 0 {
					ctx = api.WithToolCallMetadata(ctx, values)
				}
			}
			return next(ctx, req)
		}
	}
}

// withToolCallMetadataFor returns a context whose call to serverName may carry
// the caller's tool-call metadata values of the keys that server accepts.
func withToolCallMetadataFor(ctx context.Context, serverName string) context.Context {
	return internalmcp.WithToolCallMetadata(ctx, getUserSubjectFromContext(ctx), serverName, api.ToolCallMetadata(ctx))
}

// addToolCallMetadataHooks makes the initialize (2025-11-25) and
// server/discover (2026-07-28) results declare the tool-call metadata
// extension with the keys muster's backends accept
// (advertiseToolCallMetadata).
func (a *AggregatorServer) addToolCallMetadataHooks(hooks *server.Hooks) {
	hooks.AddAfterInitialize(func(ctx context.Context, _ any, _ *mcp.InitializeRequest, result *mcp.InitializeResult) {
		a.advertiseToolCallMetadata(ctx, &result.Capabilities)
	})
	hooks.AddAfterDiscover(func(ctx context.Context, _ any, _ *mcp.DiscoverRequest, result *mcp.DiscoverResult) {
		a.advertiseToolCallMetadata(ctx, &result.Capabilities)
	})
}

// advertiseToolCallMetadata declares in capabilities the tool-call metadata
// extension with the union of the keys accepted by the backends the caller of
// ctx sees and muster holds a connection to, its shared ones and the caller's
// session's own. A backend the session connects to later is counted at the
// caller's next initialize or server/discover. Without an accepted key the
// extension is not declared.
func (a *AggregatorServer) advertiseToolCallMetadata(ctx context.Context, capabilities *mcp.ServerCapabilities) {
	keys := a.acceptedToolCallMetadataKeys(ctx)
	if len(keys) == 0 {
		return
	}
	if _, declared := capabilities.Extensions[api.ToolCallMetadataExtension]; declared {
		return
	}
	extensions := make(map[string]any, len(capabilities.Extensions)+1)
	maps.Copy(extensions, capabilities.Extensions)
	extensions[api.ToolCallMetadataExtension] = map[string]any{"keys": keys}
	capabilities.Extensions = extensions
}

// acceptedToolCallMetadataKeys returns the sorted union of the metadata keys
// accepted by the backends the caller of ctx sees.
func (a *AggregatorServer) acceptedToolCallMetadataKeys(ctx context.Context) []string {
	clients := make(map[string]MCPClient)
	for name, info := range a.registry.GetAllServers() {
		if info.Client != nil && !info.RequiresSessionAuth() {
			clients[name] = info.Client
		}
	}
	if a.connPool != nil {
		if sessionID := getSessionIDFromContext(ctx); sessionID != "" {
			maps.Copy(clients, a.connPool.ClientsOfSession(sessionID))
		}
	}

	view := api.CallerVisibility(ctx)
	var keys []string
	for name, client := range clients {
		if !view.Sees(name) {
			continue
		}
		for _, key := range internalmcp.AcceptedToolCallMetadataKeys(client.ServerCapabilities()) {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	slices.Sort(keys)
	return keys
}
