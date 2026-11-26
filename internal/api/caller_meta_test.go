package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCallerMeta(t *testing.T) {
	const identifier = "com.example/context"
	entries := map[string]any{identifier: map[string]any{"any-shape": "opaque"}, "progressToken": "caller", "tenant": "plain"}
	ctx := WithCallerMeta(t.Context(), entries, map[string]any{identifier: map[string]any{}})
	require.Equal(t, map[string]any{identifier: entries[identifier]}, CallerMeta(ctx))
	delete(entries, identifier)
	require.Contains(t, CallerMeta(ctx), identifier)
	require.Empty(t, CallerMeta(WithCallerMeta(ctx, nil, nil)))
}

func TestRelayedExtensions(t *testing.T) {
	for name, settings := range map[string]any{
		"no settings": nil,
		"string":      "not settings",
		"array":       []any{"value"},
	} {
		t.Run(name, func(t *testing.T) {
			require.Empty(t, RelayedExtensions(map[string]any{"com.example/context": settings}))
		})
	}
	declarations := map[string]any{
		"com.example/context":        map[string]any{"arbitrary": []any{1, "two"}},
		"com.example/other":          map[string]any{},
		"io.modelcontextprotocol/ui": map[string]any{},
		"dev.mcp/tasks":              map[string]any{},
		"progressToken":              map[string]any{},
		"com.example/":               map[string]any{},
		"com..example/context":       map[string]any{},
	}
	require.Equal(t, map[string]any{
		"com.example/context": map[string]any{"arbitrary": []any{1, "two"}},
		"com.example/other":   map[string]any{},
	}, RelayedExtensions(declarations))
}
