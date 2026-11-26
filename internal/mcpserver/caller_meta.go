package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
)

// withCallerMeta relays opaque extension entries accepted by this backend.
// Request metadata already set by muster takes precedence.
func withCallerMeta(ctx context.Context, meta *mcp.Meta, extensions map[string]any) *mcp.Meta {
	for identifier := range api.RelayedExtensions(extensions) {
		value, present := api.CallerMeta(ctx)[identifier]
		if !present {
			continue
		}
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
