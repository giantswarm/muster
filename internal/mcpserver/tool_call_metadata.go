package mcpserver

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// Tool-call metadata (api.ToolCallMetadataExtension) through muster.
//
// muster declares the extension with no settings in its client
// capabilities.extensions to every backend: at the handshake of a 2025-11-25
// connection and on every tool call of a 2026-07-28 one. A backend declares
// the metadata keys it accepts as {"keys": [...]} in its initialize result on
// 2025-11-25, in its server/discover result on 2026-07-28; settings without a
// valid keys array accept nothing. On a tools/call muster sends a backend
// _meta[api.ToolCallMetadataExtension] holding the caller's values of the keys
// that backend accepts and allowed agrees to, and nothing when no key is left.
// An entry already on the call is kept as it is. muster relays the values
// unchanged; the backend validates them.

type toolCallMetadataForwardKey struct{}

type toolCallMetadataForward struct {
	caller  string
	backend string
	values  map[string]any
}

// WithToolCallMetadata returns a context whose tool calls to backend may carry
// the caller's tool-call metadata values of the keys backend accepts.
func WithToolCallMetadata(ctx context.Context, caller, backend string, values map[string]any) context.Context {
	if len(values) == 0 {
		return ctx
	}
	return context.WithValue(ctx, toolCallMetadataForwardKey{}, toolCallMetadataForward{caller: caller, backend: backend, values: values})
}

// allowed reports whether caller may send backend the value of metadata key.
// Every forward decision goes through it; negotiation alone decides today, so
// it allows everything.
func allowed(caller, backend, key string) bool {
	return true
}

// AcceptedToolCallMetadataKeys returns the metadata keys a server with
// capabilities accepts, sorted, or nil when it declares none.
func AcceptedToolCallMetadataKeys(capabilities mcp.ServerCapabilities) []string {
	settings, ok := capabilities.Extensions[api.ToolCallMetadataExtension].(map[string]any)
	if !ok {
		return nil
	}
	declared, ok := settings["keys"].([]any)
	if !ok {
		return nil
	}
	var keys []string
	for _, entry := range declared {
		if key, ok := entry.(string); ok && api.IsToolCallMetadataKey(key) && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// toolCallMetadataClientExtensions is what muster declares in its client
// capabilities.extensions to every backend.
func toolCallMetadataClientExtensions() map[string]any {
	return map[string]any{api.ToolCallMetadataExtension: map[string]any{}}
}

// forwardedToolCallMetadata returns the caller's values of ctx for the keys a
// backend accepts.
func forwardedToolCallMetadata(ctx context.Context, accepted []string) map[string]any {
	forward, ok := ctx.Value(toolCallMetadataForwardKey{}).(toolCallMetadataForward)
	if !ok {
		return nil
	}
	var out map[string]any
	for _, key := range accepted {
		value, ok := forward.values[key]
		if !ok || !allowed(forward.caller, forward.backend, key) {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(accepted))
		}
		out[key] = value
	}
	if len(out) > 0 {
		logging.DebugWithAttrs("MCPClient", "Forwarding the caller's tool-call metadata",
			slog.String("server", forward.backend), slog.Any("keys", slices.Sorted(maps.Keys(out))))
	}
	return out
}

// withToolCallMetadata adds to meta the tool-call metadata entry a backend
// accepting accepted receives, unless meta already carries one.
func withToolCallMetadata(ctx context.Context, meta *mcp.Meta, accepted []string) *mcp.Meta {
	values := forwardedToolCallMetadata(ctx, accepted)
	if len(values) == 0 {
		return meta
	}
	if meta == nil {
		meta = &mcp.Meta{}
	}
	if _, taken := meta.AdditionalFields[api.ToolCallMetadataExtension]; taken {
		return meta
	}
	if meta.AdditionalFields == nil {
		meta.AdditionalFields = make(map[string]any, 1)
	}
	meta.AdditionalFields[api.ToolCallMetadataExtension] = values
	return meta
}
