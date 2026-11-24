package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsToolCallMetadataKey(t *testing.T) {
	for key, want := range map[string]bool{
		"com.example/session":            true,
		"example/session":                true,
		"com.example-corp/session.v2":    true,
		"io.modelcontextprotocol/ui":     false,
		"dev.mcp/tasks":                  false,
		"tenant":                         false,
		"progressToken":                  false,
		"traceparent":                    false,
		"com.example/":                   false,
		"/session":                       false,
		"1com.example/session":           false,
		"com.example/-session":           false,
		"com..example/session":           false,
		"io.modelcontextprotocol.x/name": false,
	} {
		require.Equal(t, want, IsToolCallMetadataKey(key), key)
	}
}

func TestToolCallMetadataValues(t *testing.T) {
	require.Equal(t, map[string]any{"com.example/session": "a1"}, ToolCallMetadataValues(map[string]any{
		ToolCallMetadataExtension: map[string]any{"com.example/session": "a1", "tenant": "x", "io.modelcontextprotocol/ui": "y"},
		"com.example/session":     "top-level entries are not the extension's",
	}))
	for name, meta := range map[string]map[string]any{
		"no _meta":       nil,
		"no entry":       {"com.example/session": "a1"},
		"not an object":  {ToolCallMetadataExtension: "a1"},
		"no valid key":   {ToolCallMetadataExtension: map[string]any{"tenant": "x"}},
		"an empty entry": {ToolCallMetadataExtension: map[string]any{}},
	} {
		require.Nil(t, ToolCallMetadataValues(meta), name)
	}
}
