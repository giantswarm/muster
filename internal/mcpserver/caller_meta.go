package mcpserver

import (
	"context"
	"maps"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
)

// withCallerMeta relays opaque application entries on the current tool call.
// Request metadata already set by muster takes precedence.
func withCallerMeta(ctx context.Context, meta *mcp.Meta) *mcp.Meta {
	if len(api.CallerMeta(ctx)) == 0 {
		return meta
	}
	if meta != nil {
		copy := *meta
		copy.AdditionalFields = maps.Clone(meta.AdditionalFields)
		meta = &copy
	}
	for identifier, value := range api.CallerMeta(ctx) {
		if meta == nil {
			meta = &mcp.Meta{}
		}
		if _, present := meta.AdditionalFields[identifier]; present {
			continue
		}
		if meta.AdditionalFields == nil {
			meta.AdditionalFields = make(map[string]any)
		}
		meta.AdditionalFields[identifier] = value
	}
	return meta
}

// clientExtensions declares relay support with empty client settings.
func clientExtensions(extensions map[string]any) map[string]any {
	declared := api.RelayedExtensions(extensions)
	for identifier := range declared {
		declared[identifier] = map[string]any{}
	}
	return declared
}
