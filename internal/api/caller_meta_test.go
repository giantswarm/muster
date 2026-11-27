package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCallerMeta(t *testing.T) {
	const identifier = "com.example/context"
	entries := map[string]any{identifier: map[string]any{"any-shape": "opaque"}, "progressToken": "caller", "tenant": "plain"}
	ctx, err := WithCallerMeta(t.Context(), entries)
	require.NoError(t, err)
	require.Equal(t, map[string]any{identifier: entries[identifier], "tenant": "plain"}, CallerMeta(ctx))
	delete(entries, identifier)
	require.Contains(t, CallerMeta(ctx), identifier)
	ctx, err = WithCallerMeta(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, CallerMeta(ctx))
}

func TestCallerMetaDoesNotRelayProtocolOrTracingFields(t *testing.T) {
	entries := map[string]any{
		"io.modelcontextprotocol/protocolVersion":    "caller",
		"io.modelcontextprotocol/clientInfo":         "caller",
		"io.modelcontextprotocol/clientCapabilities": "caller",
		"io.modelcontextprotocol/logLevel":           "caller",
		"io.modelcontextprotocol/serverInfo":         "caller",
		"io.modelcontextprotocol/subscriptionId":     "caller",
		"progressToken":                              "caller", "traceparent": "caller", "tracestate": "caller", "baggage": "caller",
		"io.giantswarm/tool-call-metadata": map[string]any{"any nested field": true},
		"org.example/other":                []any{"opaque"},
	}
	ctx, err := WithCallerMeta(t.Context(), entries)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"io.giantswarm/tool-call-metadata": entries["io.giantswarm/tool-call-metadata"],
		"org.example/other":                entries["org.example/other"],
	}, CallerMeta(ctx))
	require.Len(t, entries, 12)
}

func TestCallerMetaRejectsInvalidTopLevelNames(t *testing.T) {
	for _, key := range []string{"not a valid/key", "com..example/key", "com.example/a/b"} {
		_, err := WithCallerMeta(t.Context(), map[string]any{key: "private-value"})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private-value")
	}
}

func TestCallerMetaAcceptsOptionalPrefixesAndEmptyNames(t *testing.T) {
	entries := map[string]any{"": true, "session": "a1", "com.example/": map[string]any{"any nested field": true}}
	ctx, err := WithCallerMeta(t.Context(), entries)
	require.NoError(t, err)
	require.Equal(t, entries, CallerMeta(ctx))
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
